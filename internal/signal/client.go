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
	// ErrNotLinked when there is none. An account marked as unlinked is still returned (with
	// UnlinkedAt set).
	Account(ctx context.Context) (Account, error)

	// Connect starts receiving for the selected account. Events are delivered on Events until
	// Close. Connection changes arrive there as *Connection events; the client reconnects on its
	// own and reports StateFailed once it gives up. The connection outlives ctx (it only bounds
	// the setup), so that Close can shut it down gracefully. When the server logs the
	// device out (it was unlinked on the phone), the account is marked as unlinked and a
	// StateLoggedOut event carries UnlinkedError. On an account already marked, Connect fails
	// with it right away, without contacting the server.
	Connect(ctx context.Context) error

	// Events returns the channel of incoming events. It is unbuffered and closed by Close. An
	// event counts as handled (and is acked to the server) once it has been read from the
	// channel; events not read before Close are delivered again next time.
	Events() <-chan Event

	// Resolve returns recipients with their ACI filled in, in the same order. Recipients that
	// already have one are returned as they are. A number is looked up in the store first and
	// otherwise through contact discovery, which needs Connect (ErrNotConnected); the result is
	// cached in the store. A username (nickname.discriminator) is looked up by its hash and needs
	// no connection. Each recipient that has no Signal account fails with ErrNotOnSignal; the
	// errors of all recipients are joined.
	Resolve(ctx context.Context, recipients []Recipient) ([]Recipient, error)

	// Send sends a message. Only valid after Connect.
	Send(ctx context.Context, req SendRequest) (SendResult, error)

	// Devices lists all devices of the selected account as the server knows them. It needs
	// neither Connect nor the account lock. Like Connect, it fails with ErrDeviceUnlinked on an
	// account marked as unlinked, and marks the account when the server rejects the device.
	Devices(ctx context.Context) ([]Device, error)

	// Unlink removes this device from the selected account on the server (unless
	// opts.LocalOnly) and then deletes the account's local data. It takes the account lock, so
	// it fails with ErrAccountInUse while another process is connected. A device the server
	// already logged out (ErrDeviceUnlinked) counts as removed, and an account marked as unlinked
	// skips the server as with LocalOnly. It returns the removed account as recorded in
	// accounts.json.
	Unlink(ctx context.Context, opts UnlinkOptions) (Account, error)

	// Close shuts down gracefully: it waits for in-flight sends (later ones fail with
	// ErrClosed), makes sure the acks of events read from Events reach the server, disconnects
	// and releases the store.
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
