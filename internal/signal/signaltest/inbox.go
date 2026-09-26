package signaltest

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/cwbudde/go-signal/internal/signal"
)

// Push delivers evt on Events of the client that is connected for receiving (not with
// signal.SendOnly), after Incoming, as if it arrived while connected; an
// *signal.IdentityChanged changes the stored key as in Incoming. It blocks until the client's
// feeder took evt and reports false if there is no such client or it was closed first.
func (f *Fake) Push(evt signal.Event) bool {
	f.mu.Lock()

	var target *client

	for _, c := range f.clients {
		if c.connected != "" && !c.closed && c.fed != nil {
			target = c
		}
	}

	if changed, ok := evt.(*signal.IdentityChanged); ok && target != nil {
		f.changeIdentity(changed)
	}

	f.mu.Unlock()

	if target == nil {
		return false
	}

	select {
	case target.live <- evt:
		return true
	case <-target.done:
		return false
	}
}

// Inbox returns the entries stored in the inbox, in order.
func (f *Fake) Inbox() []signal.InboxEntry {
	f.mu.Lock()
	defer f.mu.Unlock()

	return slices.Clone(f.inbox)
}

// checkInbox fails like the real client's inbox methods; the caller holds c.fake.mu.
func (c *client) checkInbox() error {
	switch {
	case c.closed:
		return signal.ErrClosed
	case c.fake.InboxErr != nil:
		return c.fake.InboxErr
	}

	_, err := c.fake.account(c.opts)

	return err
}

func (c *client) InboxAdd(_ context.Context, entry signal.InboxEntry) (signal.InboxEntry, error) {
	c.fake.mu.Lock()
	defer c.fake.mu.Unlock()

	err := c.checkInbox()
	if err != nil {
		return signal.InboxEntry{}, err
	}

	switch entry.Event.(type) {
	case *signal.Connection, *signal.QueueEmpty, nil:
		return signal.InboxEntry{}, fmt.Errorf("%w: %T (fake)", signal.ErrNotStorable, entry.Event)
	}

	c.fake.inboxID++
	entry.ID = c.fake.inboxID
	entry.ReceivedAt = entry.ReceivedAt.UTC().Truncate(time.Millisecond)
	entry.Time = entry.Time.UTC().Truncate(time.Millisecond)
	c.fake.inbox = append(c.fake.inbox, entry)

	return entry, nil
}

func (c *client) InboxList(_ context.Context, query signal.InboxQuery) ([]signal.InboxEntry, error) {
	c.fake.mu.Lock()
	defer c.fake.mu.Unlock()

	err := c.checkInbox()
	if err != nil {
		return nil, err
	}

	var out []signal.InboxEntry

	for _, entry := range c.fake.inbox {
		if inboxMatches(entry, query) {
			out = append(out, entry)
		}
	}

	if query.Limit > 0 && len(out) > query.Limit {
		if query.Newest {
			out = out[len(out)-query.Limit:]
		} else {
			out = out[:query.Limit]
		}
	}

	return slices.Clip(out), nil
}

func inboxMatches(entry signal.InboxEntry, query signal.InboxQuery) bool {
	switch {
	case entry.ID <= query.After, query.Until > 0 && entry.ID > query.Until:
		return false
	case query.Chat != "" && entry.Chat.Key() != query.Chat:
		return false
	case !query.Since.IsZero() && entry.Time.Before(query.Since):
		return false
	case query.Unread && !entry.Unread:
		return false
	default:
		return true
	}
}

func (c *client) InboxChats(context.Context) ([]signal.InboxChat, error) {
	c.fake.mu.Lock()
	defer c.fake.mu.Unlock()

	err := c.checkInbox()
	if err != nil {
		return nil, err
	}

	chats := map[string]*signal.InboxChat{}

	for _, entry := range c.fake.inbox {
		key := entry.Chat.Key()
		if key == "" {
			continue
		}

		chat := chats[key]
		if chat == nil {
			chat = &signal.InboxChat{}
			chats[key] = chat
		}

		chat.Chat, chat.Last = entry.Chat, entry
		chat.Entries++

		if entry.Unread {
			chat.Unread++
		}
	}

	out := make([]signal.InboxChat, 0, len(chats))
	for _, chat := range chats {
		out = append(out, *chat)
	}

	slices.SortFunc(out, func(a, b signal.InboxChat) int { return int(b.Last.ID - a.Last.ID) })

	return out, nil
}

func (c *client) InboxMarkRead(_ context.Context, marks []signal.ReadMark) (int, error) {
	c.fake.mu.Lock()
	defer c.fake.mu.Unlock()

	err := c.checkInbox()
	if err != nil {
		return 0, err
	}

	marked := 0

	for i, entry := range c.fake.inbox {
		msg, ok := entry.Event.(*signal.Message)
		if !ok || !entry.Unread {
			continue
		}

		if slices.ContainsFunc(marks, func(mark signal.ReadMark) bool {
			return mark.Sender.ACI == msg.Sender.ACI && mark.Timestamp == msg.Timestamp
		}) {
			c.fake.inbox[i].Unread = false
			marked++
		}
	}

	return marked, nil
}

func (c *client) InboxPrune(_ context.Context, before time.Time, keep int) (int, error) {
	c.fake.mu.Lock()
	defer c.fake.mu.Unlock()

	err := c.checkInbox()
	if err != nil {
		return 0, err
	}

	count := len(c.fake.inbox)

	if !before.IsZero() {
		c.fake.inbox = slices.DeleteFunc(c.fake.inbox, func(entry signal.InboxEntry) bool {
			return entry.ReceivedAt.Before(before)
		})
	}

	if keep > 0 && len(c.fake.inbox) > keep {
		c.fake.inbox = slices.Clone(c.fake.inbox[len(c.fake.inbox)-keep:])
	}

	return count - len(c.fake.inbox), nil
}
