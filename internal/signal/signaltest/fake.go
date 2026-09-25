// Package signaltest provides an in-memory signal.Client for tests. It needs no cgo.
package signaltest

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/cwbudde/go-signal/internal/signal"
)

// LinkURI is the URI the fake passes to Link's onURI callback.
const LinkURI = "sgnl://linkdevice?uuid=fake&pub_key=fake"

// Fake is shared state behind the clients its Factory opens, so that e.g. a `link` followed by a
// `receive` in the same test see the same accounts. Like the real account lock, only one open
// client at a time can be connected per account (ErrAccountInUse). Set the exported fields before use.
//
// A remote unlink is simulated like the real client sees it: a signal.StateLoggedOut Connection
// in Incoming, or DevicesErr wrapping signal.ErrDeviceUnlinked, marks the account as unlinked in
// Linked (UnlinkedAt) and reports signal.UnlinkedError. Connect and Devices then fail with it
// right away, and Unlink skips the server as with LocalOnly.
type Fake struct {
	// Linked are the stored accounts, selected like the real client does (signal.SelectAccount).
	// Link adds LinkAs, replacing an entry with the same ACI.
	Linked []signal.Account
	// LinkAs is the account Link creates.
	LinkAs signal.Account
	// Incoming is delivered on Events after Connect, in order. Like the real client, Events is
	// unbuffered, so an event counts as delivered (Delivered) once the consumer has taken it.
	// A StateLoggedOut Connection marks the account as unlinked; its Err becomes UnlinkedError.
	Incoming []signal.Event
	// Devices is what Devices returns for any account.
	Devices []signal.Device
	// InUse simulates another process holding the account lock: Connect and Unlink fail.
	InUse bool

	// OpenErr, LinkErr, ConnectErr, SendErr and DevicesErr make the respective call fail.
	OpenErr    error
	LinkErr    error
	ConnectErr error
	SendErr    error
	DevicesErr error
	// UnlinkErr makes removing the device on the server fail; Unlink with LocalOnly ignores it.
	UnlinkErr error

	mu        sync.Mutex
	opened    []signal.Options
	sent      []signal.SendRequest
	connects  []string
	unlinks   []UnlinkCall
	delivered int
	nextTS    uint64
	clients   []*client
}

// Factory is a signal.Factory that opens clients on f.
func (f *Fake) Factory(_ context.Context, opts signal.Options) (signal.Client, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.opened = append(f.opened, opts)

	if f.OpenErr != nil {
		return nil, f.OpenErr
	}

	cli := &client{fake: f, opts: opts, events: make(chan signal.Event), done: make(chan struct{})}
	f.clients = append(f.clients, cli)

	return cli, nil
}

// Opened returns the options of every Factory call.
func (f *Fake) Opened() []signal.Options {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([]signal.Options(nil), f.opened...)
}

// Sent returns every successful SendRequest.
func (f *Fake) Sent() []signal.SendRequest {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([]signal.SendRequest(nil), f.sent...)
}

// Connects returns the ACI of the account each successful Connect used.
func (f *Fake) Connects() []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([]string(nil), f.connects...)
}

// Delivered returns the number of Incoming events the consumers have taken from Events.
func (f *Fake) Delivered() int {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.delivered
}

// UnlinkCall records a successful Unlink.
type UnlinkCall struct {
	ACI       string
	LocalOnly bool
}

// Unlinks returns every successful Unlink.
func (f *Fake) Unlinks() []UnlinkCall {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([]UnlinkCall(nil), f.unlinks...)
}

// AllClosed reports whether every opened client has been closed.
func (f *Fake) AllClosed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()

	for _, cli := range f.clients {
		if !cli.closed {
			return false
		}
	}

	return true
}

// account mirrors the real client's selection; the caller holds f.mu.
func (f *Fake) account(opts signal.Options) (signal.Account, error) {
	acc, err := signal.SelectAccount(f.Linked, opts.Account)
	if err != nil {
		return signal.Account{}, fmt.Errorf("fake: %w", err)
	}

	return acc, nil
}

// markUnlinked mirrors the real client recording a logout in accounts.json and returns the
// error it reports; the caller holds f.mu.
func (f *Fake) markUnlinked(acc signal.Account) error {
	for i := range f.Linked {
		if f.Linked[i].ACI == acc.ACI && !f.Linked[i].Unlinked() {
			f.Linked[i].UnlinkedAt = time.Now().UTC().Truncate(time.Second)
		}
	}

	return unlinkedError(acc)
}

// unlinkedError is the error the real client reports for an unlinked acc.
func unlinkedError(acc signal.Account) error {
	return fmt.Errorf("%w (fake)", signal.UnlinkedError(acc))
}

// checkInUse mirrors the account lock; the caller holds f.mu.
func (f *Fake) checkInUse(aci string) error {
	if f.InUse {
		return fmt.Errorf("%w (fake)", signal.ErrAccountInUse)
	}

	for _, other := range f.clients {
		if other.connected == aci && !other.closed {
			return fmt.Errorf("%w (fake)", signal.ErrAccountInUse)
		}
	}

	return nil
}

type client struct {
	fake      *Fake
	opts      signal.Options
	events    chan signal.Event
	done      chan struct{} // closed by Close; stops the feeder
	fed       chan struct{} // closed when the feeder started by Connect exits
	connected string        // ACI of the connected account
	closed    bool
}

func (c *client) Link(_ context.Context, _ string, onURI func(string)) (signal.Account, error) {
	c.fake.mu.Lock()
	defer c.fake.mu.Unlock()

	if c.fake.LinkErr != nil {
		return signal.Account{}, c.fake.LinkErr
	}

	onURI(LinkURI)

	acc := c.fake.LinkAs
	c.fake.Linked = slices.DeleteFunc(c.fake.Linked, func(old signal.Account) bool { return old.ACI == acc.ACI })
	c.fake.Linked = append(c.fake.Linked, acc)
	// Like the real client, stay on the new account.
	c.opts.Account = acc.ACI

	return acc, nil
}

func (c *client) Account(context.Context) (signal.Account, error) {
	c.fake.mu.Lock()
	defer c.fake.mu.Unlock()

	return c.fake.account(c.opts)
}

func (c *client) Connect(context.Context) error {
	c.fake.mu.Lock()
	defer c.fake.mu.Unlock()

	acc, err := c.fake.account(c.opts)
	if err != nil {
		return err
	}

	if acc.Unlinked() {
		return unlinkedError(acc)
	}

	if c.fake.ConnectErr != nil {
		return c.fake.ConnectErr
	}

	err = c.fake.checkInUse(acc.ACI)
	if err != nil {
		return err
	}

	c.connected = acc.ACI
	c.fake.connects = append(c.fake.connects, acc.ACI)

	incoming := make([]signal.Event, 0, len(c.fake.Incoming))
	for _, evt := range c.fake.Incoming {
		if conn, ok := evt.(*signal.Connection); ok && conn.State == signal.StateLoggedOut {
			evt = &signal.Connection{State: signal.StateLoggedOut, Err: c.fake.markUnlinked(acc)}
		}

		incoming = append(incoming, evt)
	}

	c.fed = make(chan struct{})
	go c.feed(incoming)

	return nil
}

func (c *client) Events() <-chan signal.Event {
	return c.events
}

func (c *client) Send(_ context.Context, req signal.SendRequest) (signal.SendResult, error) {
	c.fake.mu.Lock()
	defer c.fake.mu.Unlock()

	if c.connected == "" {
		return signal.SendResult{}, signal.ErrNotConnected
	}

	if c.fake.SendErr != nil {
		return signal.SendResult{}, c.fake.SendErr
	}

	c.fake.sent = append(c.fake.sent, req)
	c.fake.nextTS++

	res := signal.SendResult{Timestamp: c.fake.nextTS}
	for _, rcpt := range req.Recipients {
		res.Results = append(res.Results, signal.RecipientResult{Recipient: rcpt, Unidentified: true})
	}

	return res, nil
}

func (c *client) Devices(context.Context) ([]signal.Device, error) {
	c.fake.mu.Lock()
	defer c.fake.mu.Unlock()

	acc, err := c.fake.account(c.opts)
	if err != nil {
		return nil, err
	}

	if acc.Unlinked() {
		return nil, unlinkedError(acc)
	}

	if errors.Is(c.fake.DevicesErr, signal.ErrDeviceUnlinked) {
		return nil, c.fake.markUnlinked(acc)
	}

	if c.fake.DevicesErr != nil {
		return nil, c.fake.DevicesErr
	}

	return append([]signal.Device(nil), c.fake.Devices...), nil
}

func (c *client) Unlink(_ context.Context, opts signal.UnlinkOptions) (signal.Account, error) {
	c.fake.mu.Lock()
	defer c.fake.mu.Unlock()

	acc, err := c.fake.account(c.opts)
	if err != nil {
		return signal.Account{}, err
	}

	err = c.fake.checkInUse(acc.ACI)
	if err != nil {
		return signal.Account{}, err
	}

	// The server already removed a device marked as unlinked.
	opts.LocalOnly = opts.LocalOnly || acc.Unlinked()

	if !opts.LocalOnly && c.fake.UnlinkErr != nil {
		return signal.Account{}, c.fake.UnlinkErr
	}

	c.fake.Linked = slices.DeleteFunc(c.fake.Linked, func(old signal.Account) bool { return old.ACI == acc.ACI })
	c.fake.unlinks = append(c.fake.unlinks, UnlinkCall{ACI: acc.ACI, LocalOnly: opts.LocalOnly})

	return acc, nil
}

func (c *client) Close() error {
	c.fake.mu.Lock()
	if c.closed {
		c.fake.mu.Unlock()

		return nil
	}

	c.closed = true
	fed := c.fed
	c.fake.mu.Unlock()

	close(c.done)

	if fed != nil {
		<-fed
	}

	close(c.events)

	return nil
}

// feed delivers incoming on Events until Close.
func (c *client) feed(incoming []signal.Event) {
	defer close(c.fed)

	for _, evt := range incoming {
		select {
		case c.events <- evt:
			c.fake.mu.Lock()
			c.fake.delivered++
			c.fake.mu.Unlock()
		case <-c.done:
			return
		}
	}
}
