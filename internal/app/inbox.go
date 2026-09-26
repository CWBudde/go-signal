package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"sync"
	"time"

	"github.com/cwbudde/go-signal/internal/signal"
)

// Limits of the inbox queries.
const (
	// DefaultMessagesLimit is how many entries List and Wait return unless asked otherwise.
	DefaultMessagesLimit = 50
	// MaxMessagesLimit caps the entries of one List or Wait.
	MaxMessagesLimit = 200
	// DefaultWait and MaxWait are Wait's default and longest timeout.
	DefaultWait = 30 * time.Second
	MaxWait     = 60 * time.Second
)

var (
	// ErrInvalidCursor means that a cursor wasn't returned by List or Wait.
	ErrInvalidCursor = errors.New("invalid cursor")
	// ErrUnknownEntry means that the inbox has no entry with the ID (any more).
	ErrUnknownEntry = errors.New("no such inbox entry")
	// ErrNoAttachment means that an inbox entry has no attachment with the number.
	ErrNoAttachment = errors.New("no such attachment")
)

// InboxOptions configures an Inbox.
type InboxOptions struct {
	// MaxAge and MaxCount bound what the inbox keeps: entries received longer than MaxAge ago
	// and all but the newest MaxCount entries are deleted. Zero means no limit.
	MaxAge   time.Duration
	MaxCount int
	// Added, if set, is called by Run for every entry it stored.
	Added func(ctx context.Context, entry signal.InboxEntry)
}

// Inbox keeps the events that a receive loop (Run) takes from the client in the store, where MCP
// clients read them (List, Wait) at their own pace. The client must be connected for receiving
// (not signal.SendOnly). Its methods are safe for concurrent use.
type Inbox struct {
	app  *App
	opts InboxOptions

	mu      sync.Mutex
	changed chan struct{} // closed and replaced whenever Run stored an entry
}

// Inbox returns an inbox on the App's client.
func (a *App) Inbox(opts InboxOptions) *Inbox {
	return &Inbox{app: a, opts: opts, changed: make(chan struct{})}
}

// Run stores the events from events (the client's Events) in the inbox until events is closed
// (nil), ctx ends, or the connection is lost for good (see LostConnection), and deletes old
// entries as InboxOptions say. Messages, edits, deletes, reactions, unsupported content,
// decryption failures and identity changes are stored; a read sync from another of our devices
// marks the messages it names as read; typing indicators, receipts and connection changes are
// dropped. An event counts as received once it has been read from events, so an error from the
// store ends Run: the events not read yet stay on the server.
func (i *Inbox) Run(ctx context.Context, events <-chan signal.Event) error {
	i.prune(ctx)

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("inbox: %w", ctx.Err())
		case evt, ok := <-events:
			if !ok {
				return nil
			}

			err := LostConnection(evt)
			if err != nil {
				return err
			}

			// The event is received: store it even if ctx ends meanwhile.
			err = i.store(context.WithoutCancel(ctx), evt)
			if err != nil {
				return err
			}
		}
	}
}

// MessagesRequest selects inbox entries for List and Wait.
type MessagesRequest struct {
	// Chat is the key of the chat (see signal.Chat.Key and ResolveChat); empty means all chats.
	Chat string
	// Cursor, returned by an earlier List or Wait, selects the entries after it.
	Cursor string
	// Since selects the entries whose time (see signal.InboxEntry.Time) is not before it.
	Since time.Time
	// Limit caps the entries: zero means DefaultMessagesLimit, and at most MaxMessagesLimit.
	Limit int
}

// MessagesPage is the result of List and Wait.
type MessagesPage struct {
	// Entries are sorted from old to new.
	Entries []signal.InboxEntry
	// Cursor selects the entries after these in the next List or Wait.
	Cursor string
	// More reports that more entries match after Cursor.
	More bool
}

// List returns inbox entries that req selects. With a cursor or Since, it returns the oldest
// entries after the cursor (and not before Since), so that paging with the returned cursor goes
// through all of them; without either, it returns the newest entries. More reports whether
// further entries follow.
func (i *Inbox) List(ctx context.Context, req MessagesRequest) (MessagesPage, error) {
	after, err := parseCursor(req.Cursor)
	if err != nil {
		return MessagesPage{}, err
	}

	limit := req.Limit
	if limit <= 0 {
		limit = DefaultMessagesLimit
	}

	limit = min(limit, MaxMessagesLimit)
	query := signal.InboxQuery{After: after, Chat: req.Chat, Since: req.Since, Limit: limit + 1}

	newest := req.Cursor == "" && req.Since.IsZero()
	if newest {
		query.Limit, query.Newest = limit, true
	}

	entries, err := i.app.client.InboxList(ctx, query)
	if err != nil {
		return MessagesPage{}, fmt.Errorf("inbox: %w", err)
	}

	page := MessagesPage{Entries: entries}
	if len(entries) > limit {
		page.Entries, page.More = entries[:limit], true
	}

	switch {
	case len(page.Entries) > 0:
		page.Cursor = FormatCursor(page.Entries[len(page.Entries)-1].ID)
	case req.Cursor != "":
		page.Cursor = req.Cursor
	default:
		page.Cursor, err = i.latestCursor(ctx)
	}

	return page, err
}

// Wait waits up to timeout (zero means DefaultWait, at most MaxWait) for entries that req
// selects after its cursor, or after the newest entry when it has none, and returns them like
// List. When none arrive in time, it returns no entries and the cursor to wait on next.
func (i *Inbox) Wait(ctx context.Context, req MessagesRequest, timeout time.Duration) (MessagesPage, error) {
	if timeout <= 0 {
		timeout = DefaultWait
	}

	timer := time.NewTimer(min(timeout, MaxWait))
	defer timer.Stop()

	if req.Cursor == "" {
		cursor, err := i.latestCursor(ctx)
		if err != nil {
			return MessagesPage{}, err
		}

		req.Cursor = cursor
	}

	for {
		// Take the channel before looking, so that an entry stored in between isn't missed.
		i.mu.Lock()
		changed := i.changed
		i.mu.Unlock()

		page, err := i.List(ctx, req)
		if err != nil || len(page.Entries) > 0 {
			return page, err
		}

		select {
		case <-changed:
		case <-timer.C:
			return page, nil
		case <-ctx.Done():
			return MessagesPage{}, fmt.Errorf("inbox: %w", ctx.Err())
		}
	}
}

// Chats summarizes the inbox by chat, newest chat first.
func (i *Inbox) Chats(ctx context.Context) ([]signal.InboxChat, error) {
	chats, err := i.app.client.InboxChats(ctx)
	if err != nil {
		return nil, fmt.Errorf("inbox: %w", err)
	}

	return chats, nil
}

// MarkReadRequest is the input of MarkRead.
type MarkReadRequest struct {
	// Chat is the key of the chat (see ResolveChat); empty means all chats.
	Chat string
	// Cursor limits it to the entries up to the cursor; empty means all.
	Cursor string
}

// MarkReadResult is the output of MarkRead.
type MarkReadResult struct {
	// Messages is how many messages were marked as read.
	Messages int
	// Senders is how many users got a read receipt.
	Senders int
}

// MarkRead sends read receipts for the unread incoming messages that req selects, one receipt
// per sender, and marks them as read in the inbox; our other devices mark them as read, too.
// This is the only way go-signal sends read receipts from the inbox. A failed receipt leaves its
// sender's messages unread and doesn't stop the others; the errors are joined.
func (i *Inbox) MarkRead(ctx context.Context, req MarkReadRequest) (MarkReadResult, error) {
	until, err := parseCursor(req.Cursor)
	if err != nil {
		return MarkReadResult{}, err
	}

	entries, err := i.app.client.InboxList(ctx, signal.InboxQuery{Chat: req.Chat, Until: until, Unread: true})
	if err != nil {
		return MarkReadResult{}, fmt.Errorf("inbox: %w", err)
	}

	receipts := i.app.ReadReceipts()
	for _, entry := range entries {
		receipts.Add(entry.Event)
	}

	var (
		res   MarkReadResult
		marks []signal.ReadMark
		errs  []error
	)

	for _, p := range receipts.pending {
		err := i.app.client.SendReceipt(ctx, p.sender, signal.ReceiptRead, p.timestamps)
		if err != nil {
			errs = append(errs, fmt.Errorf("read receipt to %s: %w", p.sender, err))

			continue
		}

		res.Senders++

		for _, ts := range p.timestamps {
			marks = append(marks, signal.ReadMark{Sender: p.sender, Timestamp: ts})
		}
	}

	if len(marks) > 0 {
		res.Messages, err = i.app.client.InboxMarkRead(ctx, marks)
		if err != nil {
			errs = append(errs, fmt.Errorf("inbox: %w", err))
		}
	}

	return res, errors.Join(errs...)
}

// AttachmentRequest is the input of Attachment.
type AttachmentRequest struct {
	// ID is the inbox entry's ID (see FormatCursor), a message with attachments.
	ID string
	// Number is the attachment's number within the message, from 1.
	Number int
	// Dir is where the file is saved (see SaveAttachments).
	Dir string
}

// AttachmentResult is the output of Attachment.
type AttachmentResult struct {
	Attachment signal.Attachment
	// Path is the file the attachment was saved to.
	Path string
	// Data is the attachment's content.
	Data []byte
}

// Attachment downloads an attachment of a message in the inbox and saves it to a new file in
// req.Dir, named like SaveAttachments does.
func (i *Inbox) Attachment(ctx context.Context, req AttachmentRequest) (AttachmentResult, error) {
	msg, err := i.message(ctx, req.ID)
	if err != nil {
		return AttachmentResult{}, err
	}

	if req.Number < 1 || req.Number > len(msg.Attachments) {
		return AttachmentResult{}, fmt.Errorf("%w: entry %s has no attachment %d", ErrNoAttachment, req.ID, req.Number)
	}

	att := msg.Attachments[req.Number-1]

	err = PrepareDownloadDir(req.Dir)
	if err != nil {
		return AttachmentResult{}, err
	}

	data, err := i.app.client.Download(ctx, att)
	if err != nil {
		return AttachmentResult{}, fmt.Errorf("download: %w", err)
	}

	path, err := writeNewFile(req.Dir, AttachmentFilename(msg.Timestamp, req.Number, att), data)
	if err != nil {
		return AttachmentResult{}, fmt.Errorf("save: %w", err)
	}

	return AttachmentResult{Attachment: att, Path: path, Data: data}, nil
}

func (i *Inbox) store(ctx context.Context, evt signal.Event) error {
	switch evt := evt.(type) {
	case *signal.ReadSync:
		_, err := i.app.client.InboxMarkRead(ctx, evt.Messages)
		if err != nil {
			slog.WarnContext(ctx, "inbox: read sync not applied", "error", err)
		}

		return nil
	case *signal.Typing, *signal.Receipt, *signal.Connection, *signal.QueueEmpty:
		return nil
	}

	now := i.app.now()

	entry, err := i.app.client.InboxAdd(ctx, signal.InboxEntry{
		ReceivedAt: now, Time: eventTime(evt, now), Chat: eventChat(evt), Event: evt, Unread: isIncoming(evt),
	})
	if err != nil {
		return fmt.Errorf("inbox: store event: %w", err)
	}

	i.prune(ctx)

	i.mu.Lock()
	close(i.changed)
	i.changed = make(chan struct{})
	i.mu.Unlock()

	if i.opts.Added != nil {
		i.opts.Added(ctx, entry)
	}

	return nil
}

// prune deletes old entries; a failure is only logged, since the entries stay usable.
func (i *Inbox) prune(ctx context.Context) {
	if i.opts.MaxAge <= 0 && i.opts.MaxCount <= 0 {
		return
	}

	var before time.Time
	if i.opts.MaxAge > 0 {
		before = i.app.now().Add(-i.opts.MaxAge)
	}

	deleted, err := i.app.client.InboxPrune(ctx, before, max(i.opts.MaxCount, 0))
	if err != nil {
		slog.WarnContext(ctx, "inbox: old entries not deleted", "error", err)

		return
	}

	if deleted > 0 {
		slog.DebugContext(ctx, "inbox: old entries deleted", "count", deleted)
	}
}

// latestCursor is the cursor after the newest entry of any chat, so that nothing older follows.
func (i *Inbox) latestCursor(ctx context.Context) (string, error) {
	latest, err := i.app.client.InboxList(ctx, signal.InboxQuery{Limit: 1, Newest: true})
	if err != nil {
		return "", fmt.Errorf("inbox: %w", err)
	}

	if len(latest) == 0 {
		return FormatCursor(0), nil
	}

	return FormatCursor(latest[0].ID), nil
}

// message returns the message of the inbox entry with the ID (see FormatCursor).
func (i *Inbox) message(ctx context.Context, entryID string) (*signal.Message, error) {
	id, err := parseCursor(entryID)
	if err != nil || id == 0 {
		return nil, fmt.Errorf("%w %q", ErrUnknownEntry, entryID)
	}

	entries, err := i.app.client.InboxList(ctx, signal.InboxQuery{After: id - 1, Until: id})
	if err != nil {
		return nil, fmt.Errorf("inbox: %w", err)
	}

	if len(entries) == 0 {
		return nil, fmt.Errorf("%w %q", ErrUnknownEntry, entryID)
	}

	msg, ok := entries[0].Event.(*signal.Message)
	if !ok {
		return nil, fmt.Errorf("%w: entry %s is no message", ErrNoAttachment, entryID)
	}

	return msg, nil
}

// FormatCursor returns the cursor (and entry ID) for an inbox entry's ID.
func FormatCursor(id int64) string {
	return strconv.FormatInt(id, 10)
}

func parseCursor(cursor string) (int64, error) {
	if cursor == "" {
		return 0, nil
	}

	id, err := strconv.ParseInt(cursor, 10, 64)
	if err != nil || id < 0 {
		return 0, fmt.Errorf("%w %q", ErrInvalidCursor, cursor)
	}

	return id, nil
}

// ResolveChat turns a chat argument into the chat: a user or group:<id> as ParseRecipient takes
// them (numbers and usernames are resolved to the ACI, self is note-to-self), or else a group ID
// or title as ResolveGroup takes them.
func (a *App) ResolveChat(ctx context.Context, arg string) (signal.Chat, error) {
	_, err := ParseRecipient(arg)
	if err != nil {
		groupID, groupErr := a.ResolveGroup(ctx, arg)
		if groupErr != nil {
			return signal.Chat{}, fmt.Errorf("chat %q is neither a user nor a known group: %w", arg, groupErr)
		}

		return signal.Chat{GroupID: groupID}, nil
	}

	targets, err := a.ResolveRecipients(ctx, []string{arg})
	if err != nil {
		return signal.Chat{}, err
	}

	if targets[0].IsGroup() {
		return signal.Chat{GroupID: targets[0].GroupID}, nil
	}

	return signal.Chat{Recipient: targets[0].Recipient}, nil
}

// LostConnection returns the error of evt if it reports a connection that is lost for good (a
// *signal.Connection with StateLoggedOut, which wraps signal.ErrDeviceUnlinked, or StateFailed),
// and nil for every other event.
func LostConnection(evt signal.Event) error {
	conn, ok := evt.(*signal.Connection)
	if !ok {
		return nil
	}

	switch conn.State {
	case signal.StateLoggedOut:
		switch {
		case errors.Is(conn.Err, signal.ErrDeviceUnlinked):
			return conn.Err
		case conn.Err == nil:
			return signal.ErrDeviceUnlinked
		default:
			return fmt.Errorf("%w: %w", signal.ErrDeviceUnlinked, conn.Err)
		}
	case signal.StateFailed:
		if conn.Err == nil {
			return signal.ErrConnectionFailed
		}

		return conn.Err
	case signal.StateConnected, signal.StateDisconnected, signal.StateError:
	}

	return nil
}

// eventChat is the chat an inbox entry belongs to: the envelope's chat, the 1:1 chat with the
// user of a decryption failure or identity change, else none.
func eventChat(evt signal.Event) signal.Chat {
	switch evt := evt.(type) {
	case *signal.DecryptionFailure:
		return signal.Chat{Recipient: evt.Sender}
	case *signal.IdentityChanged:
		return signal.Chat{Recipient: evt.Recipient}
	}

	if env, ok := envelopeOf(evt); ok {
		return env.Chat
	}

	return signal.Chat{}
}

// eventTime is when evt happened as far as known: its sender's timestamp, else received.
func eventTime(evt signal.Event, received time.Time) time.Time {
	var sent uint64

	switch evt := evt.(type) {
	case *signal.DecryptionFailure:
		sent = evt.Timestamp
	case *signal.IdentityChanged:
		if !evt.Time.IsZero() {
			return evt.Time
		}
	default:
		if env, ok := envelopeOf(evt); ok {
			sent = env.Timestamp
		}
	}

	if sent == 0 || sent > math.MaxInt64 {
		return received
	}

	return time.UnixMilli(int64(sent))
}

// isIncoming reports whether evt is a message from another user, which is unread until marked.
func isIncoming(evt signal.Event) bool {
	_, ok := incomingMessage(evt)

	return ok
}

func envelopeOf(evt signal.Event) (signal.Envelope, bool) {
	switch evt := evt.(type) {
	case *signal.Message:
		return evt.Envelope, true
	case *signal.Edit:
		return evt.Envelope, true
	case *signal.Delete:
		return evt.Envelope, true
	case *signal.Reaction:
		return evt.Envelope, true
	case *signal.Typing:
		return evt.Envelope, true
	case *signal.Unsupported:
		return evt.Envelope, true
	default:
		return signal.Envelope{}, false
	}
}
