//go:build cgo

package signal

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cwbudde/go-signal/internal/store"
	"github.com/rs/zerolog"
	"go.mau.fi/mautrix-signal/pkg/signalmeow"
	"go.mau.fi/mautrix-signal/pkg/signalmeow/events"
	mstore "go.mau.fi/mautrix-signal/pkg/signalmeow/store"
)

// ackGrace gives signalmeow time to ack the last envelopes before the websockets close.
// Phase 3.1 replaces it with a proper drain.
const ackGrace = time.Second

// eventBuffer decouples signalmeow's receive loop from a slow consumer.
const eventBuffer = 64

var (
	errUnexpectedState = errors.New("unexpected provisioning state")
	errProvisioning    = errors.New("provisioning ended without device data")
	errAlreadyStarted  = errors.New("client is already connected")
)

// Open opens the store in opts.DataDir and returns a signalmeow-backed Client.
// It is the default Factory.
func Open(ctx context.Context, opts Options) (Client, error) {
	log := opts.logger()
	zlog := NewZerologBridge(log)

	data, err := store.Open(zlog.WithContext(ctx), opts.DataDir, zlog)
	if err != nil {
		return nil, fmt.Errorf("open store: %w", err)
	}

	return &meowClient{
		opts:   opts,
		log:    log,
		zlog:   zlog,
		data:   data,
		events: make(chan Event, eventBuffer),
		done:   make(chan struct{}),
	}, nil
}

type meowClient struct {
	opts Options
	log  *slog.Logger
	zlog zerolog.Logger
	data *store.Store

	cli    *signalmeow.Client
	ownACI string
	events chan Event
	done   chan struct{} // closed by Close; unblocks pending emits

	// mu guards events against being closed while an emit is in flight.
	mu        sync.RWMutex
	closed    bool
	closeOnce sync.Once
	acked     atomic.Bool
}

func (c *meowClient) Link(ctx context.Context, deviceName string, onURI func(string)) (Account, error) {
	ctx = c.zlog.WithContext(ctx)

	for resp := range signalmeow.PerformProvisioning(ctx, c.data.Devices, deviceName, false) {
		if resp.Err != nil {
			return Account{}, fmt.Errorf("provisioning: %w", resp.Err)
		}

		switch resp.State {
		case signalmeow.StateProvisioningURLReceived:
			onURI(resp.ProvisioningURL)
		case signalmeow.StateProvisioningDataReceived:
			return accountFromDevice(resp.ProvisioningData), nil
		case signalmeow.StateProvisioningError:
			return Account{}, fmt.Errorf("%w: %v", errUnexpectedState, resp.State)
		default:
			return Account{}, fmt.Errorf("%w: %v", errUnexpectedState, resp.State)
		}
	}

	return Account{}, errProvisioning
}

func (c *meowClient) Account(ctx context.Context) (Account, error) {
	device, err := c.device(ctx)
	if err != nil {
		return Account{}, err
	}

	return accountFromDevice(&device.DeviceData), nil
}

func (c *meowClient) Connect(ctx context.Context) error {
	if c.cli != nil {
		return errAlreadyStarted
	}

	ctx = c.zlog.WithContext(ctx)

	device, err := c.device(ctx)
	if err != nil {
		return err
	}

	c.ownACI = device.ACI.String()
	c.cli = signalmeow.NewClient(device, c.zlog, c.handle)

	statuses, err := c.cli.StartReceiveLoops(ctx)
	if err != nil {
		c.cli = nil

		return fmt.Errorf("connect: %w", err)
	}

	go c.forwardStatuses(statuses)

	return nil
}

func (c *meowClient) Events() <-chan Event {
	return c.events
}

func (c *meowClient) Send(context.Context, SendRequest) (SendResult, error) {
	if c.cli == nil {
		return SendResult{}, ErrNotConnected
	}

	return SendResult{}, fmt.Errorf("send: %w (Phase 3.3)", ErrNotImplemented)
}

func (c *meowClient) Close() error {
	var err error

	c.closeOnce.Do(func() {
		if c.cli != nil {
			if c.acked.Load() {
				time.Sleep(ackGrace)
			}

			close(c.done)

			stopErr := c.cli.StopReceiveLoops()
			if stopErr != nil {
				c.log.Debug("stop receive loops", "error", stopErr)
			}
		} else {
			close(c.done)
		}

		c.mu.Lock()
		c.closed = true
		close(c.events)
		c.mu.Unlock()

		err = c.data.Close()
	})

	if err != nil {
		return fmt.Errorf("close store: %w", err)
	}

	return nil
}

// device returns the device selected by opts.Account, or the first one.
func (c *meowClient) device(ctx context.Context) (*mstore.Device, error) {
	devices, err := c.data.Devices.GetAllDevices(ctx)
	if err != nil {
		return nil, fmt.Errorf("load devices: %w", err)
	}

	for _, device := range devices {
		if !device.IsDeviceLoggedIn() {
			continue
		}

		want := c.opts.Account
		if want == "" || want == device.Number || want == device.ACI.String() {
			return device, nil
		}
	}

	if c.opts.Account != "" {
		return nil, fmt.Errorf("%w: %s", ErrAccountNotFound, c.opts.Account)
	}

	return nil, ErrNotLinked
}

// handle is signalmeow's event handler. Its return value decides whether the envelope is acked,
// so it only returns true once the event has been handed to the consumer.
func (c *meowClient) handle(raw events.SignalEvent) bool {
	evt := convertEvent(raw, c.ownACI)
	if evt == nil {
		c.log.Debug("ignoring event", "type", fmt.Sprintf("%T", raw))

		return true
	}

	if !c.emit(evt) {
		return false
	}

	c.acked.Store(true)

	return true
}

func (c *meowClient) forwardStatuses(statuses <-chan signalmeow.SignalConnectionStatus) {
	for status := range statuses {
		c.log.Debug("connection status", "event", status.Event.String(), "error", status.Err)

		if evt := convertStatus(status); evt != nil {
			c.emit(evt)
		}
	}
}

// emit delivers evt to the consumer. It reports false if the client was closed first.
func (c *meowClient) emit(evt Event) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.closed {
		return false
	}

	select {
	case c.events <- evt:
		return true
	case <-c.done:
		return false
	}
}

func accountFromDevice(data *mstore.DeviceData) Account {
	return Account{
		Number:   data.Number,
		ACI:      data.ACI.String(),
		PNI:      data.PNI.String(),
		DeviceID: data.DeviceID,
	}
}
