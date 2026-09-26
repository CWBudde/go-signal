package signaltest

import (
	"context"
	"fmt"
	"slices"

	"github.com/cwbudde/go-signal/internal/signal"
)

// BlockCall records a successful SetBlocked.
type BlockCall struct {
	Recipients []signal.Recipient
	Blocked    bool
	// List is the complete blocked list sent to our other devices: every contact blocked
	// afterwards, in the order of Contacts.
	List []signal.Recipient
}

// Blocks returns every successful SetBlocked, in order.
func (f *Fake) Blocks() []BlockCall {
	f.mu.Lock()
	defer f.mu.Unlock()

	return slices.Clone(f.blocks)
}

func (c *client) Contacts(context.Context) ([]signal.Contact, error) {
	c.fake.mu.Lock()
	defer c.fake.mu.Unlock()

	acc, err := c.checkContacts()
	if err != nil {
		return nil, err
	}

	return slices.DeleteFunc(slices.Clone(c.fake.Contacts), func(contact signal.Contact) bool {
		return contact.ACI != "" && contact.ACI == acc.ACI
	}), nil
}

func (c *client) Contact(_ context.Context, rcpt signal.Recipient) (signal.Contact, error) {
	c.fake.mu.Lock()
	defer c.fake.mu.Unlock()

	_, err := c.checkContacts()
	if err != nil {
		return signal.Contact{}, err
	}

	i := c.fake.contactIndex(rcpt)
	if i < 0 {
		return signal.Contact{}, fmt.Errorf("%s: %w (fake)", rcpt, signal.ErrUnknownContact)
	}

	return c.fake.Contacts[i], nil
}

func (c *client) SetBlocked(_ context.Context, recipients []signal.Recipient, blocked bool) error {
	c.fake.mu.Lock()
	defer c.fake.mu.Unlock()

	err := c.checkSetBlocked(recipients)
	if err != nil {
		return err
	}

	for _, rcpt := range recipients {
		i := c.fake.contactIndex(signal.Recipient{ACI: rcpt.ACI})
		if i < 0 {
			c.fake.Contacts = append(c.fake.Contacts, signal.Contact{Recipient: rcpt})
			i = len(c.fake.Contacts) - 1
		}

		c.fake.Contacts[i].Blocked = blocked
	}

	var list []signal.Recipient

	for _, contact := range c.fake.Contacts {
		if contact.Blocked {
			list = append(list, contact.Recipient)
		}
	}

	c.fake.blocks = append(c.fake.blocks, BlockCall{Recipients: slices.Clone(recipients), Blocked: blocked, List: list})

	return nil
}

// checkSetBlocked fails like the real client for a SetBlocked it can't make; the caller holds
// c.fake.mu.
func (c *client) checkSetBlocked(recipients []signal.Recipient) error {
	switch {
	case c.closed:
		return signal.ErrClosed
	case c.connected == "":
		return signal.ErrNotConnected
	case c.lost != nil:
		return fmt.Errorf("block: %w", c.lost)
	case c.fake.SetBlockedErr != nil:
		return c.fake.SetBlockedErr
	}

	for _, rcpt := range recipients {
		if rcpt.ACI == "" {
			return fmt.Errorf("%s: %w (fake)", rcpt, signal.ErrUnresolvable)
		}
	}

	return nil
}

// checkContacts fails like the real client for a store read it can't make and returns the
// selected account; the caller holds c.fake.mu.
func (c *client) checkContacts() (signal.Account, error) {
	if c.closed {
		return signal.Account{}, signal.ErrClosed
	}

	acc, err := c.fake.account(c.opts)
	if err != nil {
		return signal.Account{}, err
	}

	if c.fake.ContactsErr != nil {
		return signal.Account{}, c.fake.ContactsErr
	}

	return acc, nil
}

// contactIndex finds rcpt in Contacts by ACI, else PNI, else number, like the real store; -1
// if it isn't there. The caller holds f.mu.
func (f *Fake) contactIndex(rcpt signal.Recipient) int {
	var match func(signal.Contact) bool

	switch {
	case rcpt.ACI != "":
		match = func(contact signal.Contact) bool { return contact.ACI == rcpt.ACI }
	case rcpt.PNI != "":
		match = func(contact signal.Contact) bool { return contact.PNI == rcpt.PNI }
	case rcpt.Number != "":
		match = func(contact signal.Contact) bool { return contact.Number == rcpt.Number }
	default:
		return -1
	}

	return slices.IndexFunc(f.Contacts, match)
}
