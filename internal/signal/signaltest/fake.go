// Package signaltest provides an in-memory signal.Client for tests. It needs no cgo.
package signaltest

import (
	"context"
	"fmt"
	"sync"

	"github.com/cwbudde/go-signal/internal/signal"
)

// LinkURI is the URI the fake passes to Link's onURI callback.
const LinkURI = "sgnl://linkdevice?uuid=fake&pub_key=fake"

// Fake is shared state behind the clients its Factory opens, so that e.g. a `link` followed by a
// `receive` in the same test see the same account. Set the exported fields before use.
type Fake struct {
	// Linked is the stored account; nil means not linked. Link sets it to LinkAs.
	Linked *signal.Account
	// LinkAs is the account Link creates.
	LinkAs signal.Account
	// Incoming is delivered on Events right after Connect, in order. Set it before Factory.
	Incoming []signal.Event

	// OpenErr, LinkErr, ConnectErr and SendErr make the respective call fail.
	OpenErr    error
	LinkErr    error
	ConnectErr error
	SendErr    error

	mu      sync.Mutex
	opened  []signal.Options
	sent    []signal.SendRequest
	nextTS  uint64
	clients []*client
}

// Factory is a signal.Factory that opens clients on f.
func (f *Fake) Factory(_ context.Context, opts signal.Options) (signal.Client, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.opened = append(f.opened, opts)

	if f.OpenErr != nil {
		return nil, f.OpenErr
	}

	cli := &client{fake: f, opts: opts, events: make(chan signal.Event, len(f.Incoming))}
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
	if f.Linked == nil {
		if opts.Account != "" {
			return signal.Account{}, fmt.Errorf("%w: %s", signal.ErrAccountNotFound, opts.Account)
		}

		return signal.Account{}, signal.ErrNotLinked
	}

	acc := *f.Linked
	if opts.Account != "" && opts.Account != acc.Number && opts.Account != acc.ACI {
		return signal.Account{}, fmt.Errorf("%w: %s", signal.ErrAccountNotFound, opts.Account)
	}

	return acc, nil
}

type client struct {
	fake      *Fake
	opts      signal.Options
	events    chan signal.Event
	connected bool
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
	c.fake.Linked = &acc

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

	_, err := c.fake.account(c.opts)
	if err != nil {
		return err
	}

	if c.fake.ConnectErr != nil {
		return c.fake.ConnectErr
	}

	c.connected = true

	for _, evt := range c.fake.Incoming {
		c.events <- evt
	}

	return nil
}

func (c *client) Events() <-chan signal.Event {
	return c.events
}

func (c *client) Send(_ context.Context, req signal.SendRequest) (signal.SendResult, error) {
	c.fake.mu.Lock()
	defer c.fake.mu.Unlock()

	if !c.connected {
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

func (c *client) Close() error {
	c.fake.mu.Lock()
	defer c.fake.mu.Unlock()

	if !c.closed {
		c.closed = true
		close(c.events)
	}

	return nil
}
