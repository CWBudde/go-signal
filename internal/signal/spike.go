//go:build cgo

package signal

// Phase 1.4 spike: link and receive straight on top of signalmeow. Phase 2.1 replaces this with
// the Client facade.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/cwbudde/go-signal/internal/store"
	"go.mau.fi/mautrix-signal/pkg/signalmeow"
	"go.mau.fi/mautrix-signal/pkg/signalmeow/events"
	"go.mau.fi/mautrix-signal/pkg/signalmeow/protobuf/signalpb"
)

// ackGrace gives signalmeow time to ack the last envelope before the websockets close.
const ackGrace = time.Second

var (
	errUnexpectedState = errors.New("unexpected provisioning state")
	errProvisioning    = errors.New("provisioning ended without device data")
)

// Link provisions this device as a secondary device. It calls onURL with the sgnl://linkdevice
// URI to show to the user, then blocks until the phone has scanned it and the device is stored.
func Link(ctx context.Context, dataDir, deviceName string, onURL func(string)) (*Account, error) {
	zlog := NewZerologBridge(slog.Default())
	ctx = zlog.WithContext(ctx)

	data, err := store.Open(ctx, dataDir, zlog)
	if err != nil {
		return nil, fmt.Errorf("open store: %w", err)
	}
	defer closeStore(data)

	for resp := range signalmeow.PerformProvisioning(ctx, data.Devices, deviceName, false) {
		if resp.Err != nil {
			return nil, fmt.Errorf("provisioning: %w", resp.Err)
		}

		switch resp.State {
		case signalmeow.StateProvisioningURLReceived:
			onURL(resp.ProvisioningURL)
		case signalmeow.StateProvisioningDataReceived:
			data := resp.ProvisioningData

			return &Account{
				Number:   data.Number,
				ACI:      data.ACI.String(),
				PNI:      data.PNI.String(),
				DeviceID: data.DeviceID,
			}, nil
		case signalmeow.StateProvisioningError:
			return nil, fmt.Errorf("%w: %v", errUnexpectedState, resp.State)
		default:
			return nil, fmt.Errorf("%w: %v", errUnexpectedState, resp.State)
		}
	}

	return nil, errProvisioning
}

// Receive connects the first linked account and calls onEvent for every signalmeow event and
// connection status change. It returns after the first data message, or when ctx is done.
func Receive(ctx context.Context, dataDir string, onEvent func(any)) error {
	zlog := NewZerologBridge(slog.Default())
	ctx = zlog.WithContext(ctx)

	data, err := store.Open(ctx, dataDir, zlog)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer closeStore(data)

	devices, err := data.Devices.GetAllDevices(ctx)
	if err != nil {
		return fmt.Errorf("load devices: %w", err)
	}

	if len(devices) == 0 || !devices[0].IsDeviceLoggedIn() {
		return ErrNotLinked
	}

	recv := &receiver{
		onEvent:    onEvent,
		gotMessage: make(chan struct{}, 1),
		loggedOut:  make(chan error, 1),
	}
	cli := signalmeow.NewClient(devices[0], zlog, recv.handle)

	statuses, err := cli.StartReceiveLoops(ctx)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}

	defer func() {
		err := cli.StopReceiveLoops()
		if err != nil {
			slog.Debug("stop receive loops", "error", err)
		}
	}()

	return recv.wait(ctx, statuses)
}

// receiver turns signalmeow's callbacks into the spike's stop conditions.
type receiver struct {
	onEvent    func(any)
	gotMessage chan struct{}
	loggedOut  chan error
}

func (r *receiver) handle(evt events.SignalEvent) bool {
	r.onEvent(evt)

	switch evt := evt.(type) {
	case *events.ChatEvent:
		if _, ok := evt.Event.(*signalpb.DataMessage); ok {
			notify(r.gotMessage, struct{}{})
		}
	case *events.LoggedOut:
		notify(r.loggedOut, evt.Error)
	}

	return true
}

func (r *receiver) wait(ctx context.Context, statuses <-chan signalmeow.SignalConnectionStatus) error {
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("receive: %w", ctx.Err())
		case <-r.gotMessage:
			time.Sleep(ackGrace)

			return nil
		case err := <-r.loggedOut:
			return loggedOutError(err)
		case status, ok := <-statuses:
			if !ok {
				return nil
			}

			r.onEvent(status)

			if status.Event == signalmeow.SignalConnectionEventLoggedOut {
				return loggedOutError(status.Err)
			}
		}
	}
}

// notify does a non-blocking send; the receiver only cares about the first value.
func notify[T any](ch chan<- T, v T) {
	select {
	case ch <- v:
	default:
	}
}

func loggedOutError(cause error) error {
	if cause == nil {
		return ErrLoggedOut
	}

	return fmt.Errorf("%w: %w", ErrLoggedOut, cause)
}

func closeStore(data *store.Store) {
	err := data.Close()
	if err != nil {
		slog.Warn("close store", "error", err)
	}
}
