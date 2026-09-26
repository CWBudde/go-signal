//go:build cgo

package signal

import (
	"context"
	"time"

	"github.com/cwbudde/go-signal/internal/store"
)

func (c *meowClient) InboxAdd(ctx context.Context, entry InboxEntry) (InboxEntry, error) {
	event, err := marshalEvent(entry.Event, entry.Chat)
	if err != nil {
		return InboxEntry{}, err
	}

	rec := store.InboxRecord{
		ReceivedAt: entry.ReceivedAt, Time: entry.Time, Chat: entry.Chat.Key(), Unread: entry.Unread, Event: event,
	}

	if msg, ok := entry.Event.(*Message); ok {
		rec.Sender, rec.Timestamp = msg.Sender.ACI, msg.Timestamp
	}

	err = c.withStore(ctx, func(data *store.Store) error {
		rec.ID, err = data.AddInboxRecord(ctx, rec)

		return err //nolint:wrapcheck // store wraps it
	})
	if err != nil {
		return InboxEntry{}, err //nolint:wrapcheck // store wraps it
	}

	entry.ID = rec.ID

	return entry, nil
}

func (c *meowClient) InboxList(ctx context.Context, query InboxQuery) ([]InboxEntry, error) {
	var recs []store.InboxRecord

	err := c.withStore(ctx, func(data *store.Store) error {
		var err error

		recs, err = data.InboxRecords(ctx, store.InboxFilter{
			After: query.After, Until: query.Until, Chat: query.Chat, Since: query.Since, Unread: query.Unread,
			Limit: query.Limit, Newest: query.Newest,
		})

		return err //nolint:wrapcheck // store wraps it
	})
	if err != nil {
		return nil, err
	}

	entries := make([]InboxEntry, 0, len(recs))
	for _, rec := range recs {
		entries = append(entries, inboxEntry(rec))
	}

	return entries, nil
}

func (c *meowClient) InboxChats(ctx context.Context) ([]InboxChat, error) {
	var recs []store.InboxChatRecord

	err := c.withStore(ctx, func(data *store.Store) error {
		var err error

		recs, err = data.InboxChats(ctx)

		return err //nolint:wrapcheck // store wraps it
	})
	if err != nil {
		return nil, err
	}

	chats := make([]InboxChat, 0, len(recs))
	for _, rec := range recs {
		last := inboxEntry(rec.Last)
		chats = append(chats, InboxChat{Chat: last.Chat, Entries: rec.Entries, Unread: rec.Unread, Last: last})
	}

	return chats, nil
}

func (c *meowClient) InboxMarkRead(ctx context.Context, marks []ReadMark) (int, error) {
	storeMarks := make([]store.InboxMark, 0, len(marks))
	for _, mark := range marks {
		storeMarks = append(storeMarks, store.InboxMark{Sender: mark.Sender.ACI, Timestamp: mark.Timestamp})
	}

	marked := 0

	err := c.withStore(ctx, func(data *store.Store) error {
		var err error

		marked, err = data.MarkInboxRead(ctx, storeMarks)

		return err //nolint:wrapcheck // store wraps it
	})

	return marked, err
}

func (c *meowClient) InboxPrune(ctx context.Context, before time.Time, keep int) (int, error) {
	deleted := 0

	err := c.withStore(ctx, func(data *store.Store) error {
		var err error

		deleted, err = data.PruneInbox(ctx, before, keep)

		return err //nolint:wrapcheck // store wraps it
	})

	return deleted, err
}

// withStore runs use on the selected account's open store. Close waits for it like for a send.
func (c *meowClient) withStore(ctx context.Context, use func(*store.Store) error) error {
	if !c.begin(&c.sending) {
		return ErrClosed
	}
	defer c.sending.Done()

	_, err := c.storeDevice(ctx)
	if err != nil {
		return err
	}

	return use(c.data)
}

// inboxEntry converts a stored record. Its chat is stored with the event: the record only has
// the chat's key.
func inboxEntry(rec store.InboxRecord) InboxEntry {
	evt, chat := unmarshalEvent(rec.Event)

	return InboxEntry{
		ID: rec.ID, ReceivedAt: rec.ReceivedAt, Time: rec.Time, Chat: chat, Event: evt, Unread: rec.Unread,
	}
}
