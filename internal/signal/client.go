package signal

import (
	"context"
	"log/slog"
)

// Client is the facade that cmd/ (and later internal/app) talks to. The signalmeow-backed
// implementation needs cgo; signaltest.Fake implements it for tests without cgo.
//
// A Client is used by a single command: Open it, then either Link a new account or Connect an
// existing one, read Events, and Close it.
type Client interface {
	// Link provisions a new secondary device. It calls onURI with the sgnl://linkdevice URI to
	// show to the user, then blocks until the phone has scanned it and the account is stored.
	Link(ctx context.Context, deviceName string, onURI func(uri string)) (Account, error)

	// Account returns the selected linked account without connecting. It fails with
	// ErrNotLinked when there is none.
	Account(ctx context.Context) (Account, error)

	// Connect starts receiving for the selected account. Events are delivered on Events until
	// Close. Connection changes arrive there as *Connection events.
	Connect(ctx context.Context) error

	// Events returns the channel of incoming events. It is closed by Close. An event counts as
	// handled (and is acked to the server) once it has been read from the channel.
	Events() <-chan Event

	// Send sends a message. Only valid after Connect.
	Send(ctx context.Context, req SendRequest) (SendResult, error)

	// Devices lists all devices of the selected account as the server knows them. It needs
	// neither Connect nor the account lock.
	Devices(ctx context.Context) ([]Device, error)

	// Unlink removes this device from the selected account on the server (unless
	// opts.LocalOnly) and then deletes the account's local data. It takes the account lock, so
	// it fails with ErrAccountInUse while another process is connected. A device the server
	// already logged out (ErrLoggedOut) counts as removed. It returns the removed account as
	// recorded in accounts.json.
	Unlink(ctx context.Context, opts UnlinkOptions) (Account, error)

	// Close disconnects and releases the store.
	Close() error
}

// Options configures a Client.
type Options struct {
	// DataDir holds the account store.
	DataDir string
	// Account selects the account by number or ACI; empty selects the first one.
	Account string
	// Logger receives the client's (and signalmeow's) logs; nil means slog.Default().
	Logger *slog.Logger
}

// Factory opens a Client. The root command takes one so that tests can inject a fake.
type Factory func(ctx context.Context, opts Options) (Client, error)

func (o Options) logger() *slog.Logger {
	if o.Logger != nil {
		return o.Logger
	}

	return slog.Default()
}
