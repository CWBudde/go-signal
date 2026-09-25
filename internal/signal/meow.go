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
	"github.com/google/uuid"
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

// Open opens the data dir opts.DataDir and returns a signalmeow-backed Client. The account
// database is opened on first use. It is the default Factory.
func Open(_ context.Context, opts Options) (Client, error) {
	log := opts.logger()

	dir, err := store.OpenDir(opts.DataDir, log)
	if err != nil {
		return nil, fmt.Errorf("open data dir: %w", err)
	}

	return &meowClient{
		opts:   opts,
		log:    log,
		zlog:   NewZerologBridge(log),
		dir:    dir,
		events: make(chan Event, eventBuffer),
		done:   make(chan struct{}),
	}, nil
}

type meowClient struct {
	opts Options
	log  *slog.Logger
	zlog zerolog.Logger
	dir  *store.Dir

	// data is the open database of the account dataACI; lock is held once connected or linked.
	data    *store.Store
	dataACI string
	lock    *store.Lock

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

	links := c.dir.NewLinkStore(c.zlog)
	defer func() {
		err := links.Close()
		if err != nil {
			c.log.Warn("close link store", "error", err)
		}
	}()

	for resp := range signalmeow.PerformProvisioning(ctx, links, deviceName, false) {
		if resp.Err != nil {
			return Account{}, fmt.Errorf("provisioning: %w", resp.Err)
		}

		switch resp.State {
		case signalmeow.StateProvisioningURLReceived:
			onURI(resp.ProvisioningURL)
		case signalmeow.StateProvisioningDataReceived:
			return c.finishLink(links, resp.ProvisioningData)
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

	if c.lock == nil {
		c.lock, err = c.dir.Lock(c.dataACI)
		if err != nil {
			return fmt.Errorf("%w: %s", err, device.Number)
		}
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

		err = c.release()
	})

	return err
}

// release closes the account database and releases the lock, if held.
func (c *meowClient) release() error {
	var errs []error

	if c.data != nil {
		errs = append(errs, c.data.Close())
	}

	if c.lock != nil {
		errs = append(errs, c.lock.Unlock())
	}

	c.data, c.lock, c.dataACI = nil, nil, ""

	return errors.Join(errs...)
}

// finishLink records the new account in accounts.json (next to any others) and keeps its
// database and lock.
func (c *meowClient) finishLink(links *store.LinkStore, data *mstore.DeviceData) (Account, error) {
	acc := accountFromDevice(data)

	err := c.dir.PutAccount(store.AccountEntry{
		Number:   acc.Number,
		ACI:      acc.ACI,
		PNI:      acc.PNI,
		DeviceID: acc.DeviceID,
		LinkedAt: time.Now().UTC().Truncate(time.Second),
	})
	if err != nil {
		return Account{}, fmt.Errorf("record account: %w", err)
	}

	err = c.release()
	if err != nil {
		return Account{}, err
	}

	c.data, c.lock = links.Take()
	c.dataACI = acc.ACI
	// Stay on the new account even when others are linked too.
	c.opts.Account = acc.ACI

	return acc, nil
}

// device returns the logged-in device of the selected account, opening its database if needed.
func (c *meowClient) device(ctx context.Context) (*mstore.Device, error) {
	acc, err := c.selectAccount()
	if err != nil {
		return nil, err
	}

	if c.dataACI != acc.ACI {
		err = c.release()
		if err != nil {
			return nil, err
		}

		c.data, err = c.dir.OpenAccount(ctx, acc.ACI, c.zlog)
		if err != nil {
			return nil, fmt.Errorf("open account: %w", err)
		}

		c.dataACI = acc.ACI
	}

	aci, err := uuid.Parse(acc.ACI)
	if err != nil {
		return nil, fmt.Errorf("accounts.json: invalid ACI %q: %w", acc.ACI, err)
	}

	device, err := c.data.Devices.DeviceByACI(ctx, aci)
	if err != nil {
		return nil, fmt.Errorf("load device: %w", err)
	}

	if device == nil || !device.IsDeviceLoggedIn() {
		return nil, fmt.Errorf("%w: %s has no device data", ErrNotLinked, acc.Number)
	}

	return device, nil
}

// selectAccount resolves opts.Account against accounts.json (see SelectAccount).
func (c *meowClient) selectAccount() (Account, error) {
	entries, err := c.dir.Accounts()
	if err != nil {
		return Account{}, fmt.Errorf("load accounts: %w", err)
	}

	accounts := make([]Account, 0, len(entries))
	for _, entry := range entries {
		accounts = append(accounts, Account{
			Number: entry.Number, ACI: entry.ACI, PNI: entry.PNI, DeviceID: entry.DeviceID,
		})
	}

	return SelectAccount(accounts, c.opts.Account)
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
