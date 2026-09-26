package app_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/go-signal/internal/signal/signaltest"
)

const pngType = "image/png"

func aliceUser() signal.Recipient { return signal.Recipient{ACI: aliceACI, Number: aliceNumber} }

func bobUser() signal.Recipient { return signal.Recipient{ACI: bobACI} }

func aliceChat() signal.Chat { return signal.Chat{Recipient: aliceUser()} }

func groupChat() signal.Chat { return signal.Chat{GroupID: groupID} }

func incoming(sender signal.Recipient, chat signal.Chat, ts uint64, body string) *signal.Message {
	return &signal.Message{Envelope: signal.Envelope{Sender: sender, Chat: chat, Timestamp: ts}, Body: body}
}

// runInbox stores events with a new inbox on a and returns the inbox once Run has returned.
func runInbox(t *testing.T, a *app.App, opts app.InboxOptions, events ...signal.Event) *app.Inbox {
	t.Helper()

	inbox := a.Inbox(opts)

	err := inbox.Run(t.Context(), feed(events...))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	return inbox
}

// feed returns a closed channel that holds events.
func feed(events ...signal.Event) <-chan signal.Event {
	out := make(chan signal.Event, len(events))
	for _, evt := range events {
		out <- evt
	}

	close(out)

	return out
}

func entryIDs(entries []signal.InboxEntry) []int64 {
	ids := make([]int64, 0, len(entries))
	for _, entry := range entries {
		ids = append(ids, entry.ID)
	}

	return ids
}

func TestInboxRun(t *testing.T) {
	t.Parallel()

	fake := directory()
	own := signal.Recipient{ACI: testAccount().ACI}
	first := incoming(aliceUser(), aliceChat(), 1000, "hi")
	sync := &signal.Message{Envelope: signal.Envelope{Sender: own, Chat: groupChat(), Timestamp: 2000, Sync: true}}
	changed := &signal.IdentityChanged{Recipient: bobUser(), NewFingerprint: "05bb"}

	var added []int64

	runInbox(t, open(t, fake), app.InboxOptions{Added: func(_ context.Context, entry signal.InboxEntry) {
		added = append(added, entry.ID)
	}},
		first,
		incoming(aliceUser(), aliceChat(), 1001, "second"),
		&signal.Typing{Envelope: first.Envelope, Started: true},
		&signal.Receipt{Sender: aliceUser(), Type: signal.ReceiptRead, Timestamps: []uint64{1}},
		&signal.QueueEmpty{},
		&signal.Connection{State: signal.StateConnected},
		sync,
		// Read on the phone: the first message is read.
		&signal.ReadSync{Messages: []signal.ReadMark{{Sender: aliceUser(), Timestamp: 1000}}},
		changed,
	)

	entries := fake.Inbox()
	if len(entries) != 4 || !slices.Equal(added, entryIDs(entries)) {
		t.Fatalf("stored %+v, added %v; want the two messages, the sync transcript and the identity change",
			entries, added)
	}

	checkEntry(t, entries[0], first, aliceChat(), false)
	checkEntry(t, entries[1], entries[1].Event, aliceChat(), true)
	checkEntry(t, entries[2], sync, groupChat(), false)
	checkEntry(t, entries[3], changed, signal.Chat{Recipient: bobUser()}, false)

	if !entries[0].Time.Equal(time.UnixMilli(1000)) {
		t.Errorf("first entry at %v, want the message's timestamp", entries[0].Time)
	}
}

// checkEntry checks the event, chat and unread flag of an inbox entry.
func checkEntry(t *testing.T, entry signal.InboxEntry, evt signal.Event, chat signal.Chat, unread bool) {
	t.Helper()

	if entry.Event != evt || entry.Chat != chat || entry.Unread != unread {
		t.Errorf("entry %d: %+v, want %+v in %+v, unread %v", entry.ID, entry, evt, chat, unread)
	}
}

func TestInboxRunLost(t *testing.T) {
	t.Parallel()

	a := open(t, directory())

	err := a.Inbox(app.InboxOptions{}).Run(t.Context(), feed(&signal.Connection{State: signal.StateLoggedOut}))
	if !errors.Is(err, signal.ErrDeviceUnlinked) {
		t.Errorf("logged out: %v, want ErrDeviceUnlinked", err)
	}

	err = a.Inbox(app.InboxOptions{}).Run(t.Context(), feed(&signal.Connection{State: signal.StateFailed}))
	if !errors.Is(err, signal.ErrConnectionFailed) {
		t.Errorf("failed: %v, want ErrConnectionFailed", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	err = a.Inbox(app.InboxOptions{}).Run(ctx, make(chan signal.Event))
	if !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled: %v, want context.Canceled", err)
	}
}

func TestInboxRunStoreError(t *testing.T) {
	t.Parallel()

	fake := directory()
	fake.InboxErr = errBoom

	err := open(t, fake).Inbox(app.InboxOptions{}).Run(t.Context(), feed(incoming(aliceUser(), aliceChat(), 1, "hi")))
	if !errors.Is(err, errBoom) {
		t.Errorf("Run: %v, want the store's error", err)
	}
}

func TestInboxPrune(t *testing.T) {
	t.Parallel()

	fake := directory()

	client, err := fake.Factory(t.Context(), signal.Options{})
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = client.Close() })

	runInbox(t, app.New(client), app.InboxOptions{MaxCount: 2},
		incoming(aliceUser(), aliceChat(), 1, "1"),
		incoming(aliceUser(), aliceChat(), 2, "2"),
		incoming(aliceUser(), aliceChat(), 3, "3"))

	if got := entryIDs(fake.Inbox()); !slices.Equal(got, []int64{2, 3}) {
		t.Errorf("kept %v, want the newest two", got)
	}

	// Entries older than MaxAge go when the next inbox starts.
	later := app.New(client, app.WithClock(func() time.Time { return time.Now().Add(48 * time.Hour) }))
	runInbox(t, later, app.InboxOptions{MaxAge: 24 * time.Hour})

	if got := fake.Inbox(); len(got) != 0 {
		t.Errorf("kept %+v, want none", got)
	}
}

func TestInboxList(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	fake := directory()
	inbox := runInbox(t, open(t, fake), app.InboxOptions{},
		incoming(aliceUser(), aliceChat(), 1, "1"),
		incoming(bobUser(), groupChat(), 2, "2"),
		incoming(aliceUser(), aliceChat(), 3, "3"),
		incoming(aliceUser(), aliceChat(), 4, "4"),
	)

	tests := []struct {
		name   string
		req    app.MessagesRequest
		ids    []int64
		cursor string
		more   bool
	}{
		{"newest without cursor", app.MessagesRequest{Limit: 2}, []int64{3, 4}, "4", false},
		{"first page", app.MessagesRequest{Cursor: "0", Limit: 3}, []int64{1, 2, 3}, "3", true},
		{"second page", app.MessagesRequest{Cursor: "3", Limit: 3}, []int64{4}, "4", false},
		{"nothing new keeps the cursor", app.MessagesRequest{Cursor: "4"}, []int64{}, "4", false},
		{"chat", app.MessagesRequest{Chat: groupChat().Key()}, []int64{2}, "2", false},
		{"since", app.MessagesRequest{Since: time.UnixMilli(3)}, []int64{3, 4}, "4", false},
		// A chat without entries gets the cursor after the newest entry.
		{"empty chat", app.MessagesRequest{Chat: bobACI}, []int64{}, "4", false},
	}

	for _, test := range tests {
		page, err := inbox.List(ctx, test.req)
		if err != nil || !slices.Equal(entryIDs(page.Entries), test.ids) || page.Cursor != test.cursor ||
			page.More != test.more {
			t.Errorf("%s: %+v, %v; want %v, cursor %s, more %v", test.name, page, err, test.ids, test.cursor, test.more)
		}
	}

	_, err := inbox.List(ctx, app.MessagesRequest{Cursor: "x"})
	if !errors.Is(err, app.ErrInvalidCursor) {
		t.Errorf("bad cursor: %v, want ErrInvalidCursor", err)
	}
}

func TestInboxWait(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	a := open(t, directory())
	inbox := runInbox(t, a, app.InboxOptions{}, incoming(aliceUser(), aliceChat(), 1, "old"))

	// Nothing arrives: an empty page with the cursor after the newest entry.
	page, err := inbox.Wait(ctx, app.MessagesRequest{}, 10*time.Millisecond)
	if err != nil || len(page.Entries) != 0 || page.Cursor != "1" {
		t.Fatalf("timeout: %+v, %v", page, err)
	}

	events := make(chan signal.Event)
	done := make(chan error, 1)

	go func() { done <- inbox.Run(ctx, events) }()

	type result struct {
		page app.MessagesPage
		err  error
	}

	waited := make(chan result, 1)

	go func() {
		page, err := inbox.Wait(ctx, app.MessagesRequest{Cursor: page.Cursor, Chat: aliceChat().Key()}, time.Minute)
		waited <- result{page, err}
	}()

	// A message in another chat doesn't end the wait; one in the chat does.
	events <- incoming(bobUser(), groupChat(), 2, "group")

	events <- incoming(aliceUser(), aliceChat(), 3, "new")

	close(events)

	res := <-waited
	if res.err != nil || len(res.page.Entries) != 1 || res.page.Entries[0].ID != 3 || res.page.Cursor != "3" {
		t.Errorf("wait: %+v, %v; want the new message", res.page, res.err)
	}

	err = <-done
	if err != nil {
		t.Errorf("Run: %v", err)
	}
}

func TestInboxMarkRead(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	fake := directory()
	own := signal.Recipient{ACI: testAccount().ACI}
	inbox := runInbox(t, connected(t, fake), app.InboxOptions{},
		incoming(aliceUser(), aliceChat(), 1, "1"),
		incoming(aliceUser(), aliceChat(), 2, "2"),
		incoming(bobUser(), groupChat(), 3, "3"),
		&signal.Message{Envelope: signal.Envelope{Sender: own, Chat: aliceChat(), Timestamp: 4, Sync: true}},
		incoming(aliceUser(), aliceChat(), 5, "5"),
	)

	res, err := inbox.MarkRead(ctx, app.MarkReadRequest{Chat: aliceChat().Key(), Cursor: "4"})
	if err != nil || res != (app.MarkReadResult{Messages: 2, Senders: 1}) {
		t.Fatalf("MarkRead = %+v, %v", res, err)
	}

	want := []signaltest.ReceiptCall{{Sender: aliceUser(), Type: signal.ReceiptRead, Timestamps: []uint64{1, 2}}}
	if got := fake.Receipts(); !receiptsEqual(got, want) {
		t.Errorf("receipts %+v, want %+v", got, want)
	}

	// Everything else: Bob's group message and Alice's newest.
	res, err = inbox.MarkRead(ctx, app.MarkReadRequest{})
	if err != nil || res != (app.MarkReadResult{Messages: 2, Senders: 2}) {
		t.Fatalf("MarkRead all = %+v, %v", res, err)
	}

	res, err = inbox.MarkRead(ctx, app.MarkReadRequest{})
	if err != nil || res != (app.MarkReadResult{}) || len(fake.Receipts()) != 3 {
		t.Errorf("MarkRead again = %+v, %v, receipts %+v; want nothing", res, err, fake.Receipts())
	}
}

func receiptsEqual(a, b []signaltest.ReceiptCall) bool {
	return slices.EqualFunc(a, b, func(x, y signaltest.ReceiptCall) bool {
		return x.Sender == y.Sender && x.Type == y.Type && slices.Equal(x.Timestamps, y.Timestamps)
	})
}

func TestInboxMarkReadError(t *testing.T) {
	t.Parallel()

	fake := directory()
	fake.ReceiptErr = errBoom
	inbox := runInbox(t, connected(t, fake), app.InboxOptions{}, incoming(aliceUser(), aliceChat(), 1, "1"))

	res, err := inbox.MarkRead(t.Context(), app.MarkReadRequest{})
	if !errors.Is(err, errBoom) || res != (app.MarkReadResult{}) {
		t.Errorf("MarkRead = %+v, %v; want the receipt's error", res, err)
	}

	if entries := fake.Inbox(); !entries[0].Unread {
		t.Error("message marked read although its receipt failed")
	}
}

func TestInboxAttachment(t *testing.T) {
	t.Parallel()

	fake := directory()
	fake.Attachments = map[string][]byte{"cdn-photo": []byte("png")}
	msg := incoming(aliceUser(), aliceChat(), 1000, "photo")
	msg.Attachments = []signal.Attachment{{
		ContentType: pngType, Filename: "photo.png", Size: 3, Remote: signal.RemoteAttachment{CDNKey: "cdn-photo"},
	}}
	inbox := runInbox(t, open(t, fake), app.InboxOptions{}, msg, incoming(aliceUser(), aliceChat(), 1001, "text"))
	dir := filepath.Join(t.TempDir(), "downloads")

	res, err := inbox.Attachment(t.Context(), app.AttachmentRequest{ID: "1", Number: 1, Dir: dir})
	if err != nil {
		t.Fatalf("Attachment: %v", err)
	}

	data, err := os.ReadFile(res.Path)
	if err != nil || string(data) != "png" || string(res.Data) != "png" ||
		filepath.Base(res.Path) != "1000-1-photo.png" || res.Attachment.ContentType != pngType {
		t.Errorf("result %+v, file %q, %v", res, data, err)
	}
}

func TestInboxAttachmentErrors(t *testing.T) {
	t.Parallel()

	msg := incoming(aliceUser(), aliceChat(), 1000, "photo")
	msg.Attachments = []signal.Attachment{{Filename: photoName}}
	inbox := runInbox(t, open(t, directory()), app.InboxOptions{}, msg, incoming(aliceUser(), aliceChat(), 1001, "text"))
	dir := t.TempDir()

	tests := []struct {
		req  app.AttachmentRequest
		want error
	}{
		{app.AttachmentRequest{ID: "1", Number: 2, Dir: dir}, app.ErrNoAttachment},
		{app.AttachmentRequest{ID: "2", Number: 1, Dir: dir}, app.ErrNoAttachment},
		{app.AttachmentRequest{ID: "3", Number: 1, Dir: dir}, app.ErrUnknownEntry},
		{app.AttachmentRequest{ID: "x", Number: 1, Dir: dir}, app.ErrUnknownEntry},
		{app.AttachmentRequest{ID: "0", Number: 1, Dir: dir}, app.ErrUnknownEntry},
		{app.AttachmentRequest{ID: "1", Number: 1, Dir: dir}, signal.ErrAttachmentNotFound},
	}

	for _, test := range tests {
		_, err := inbox.Attachment(t.Context(), test.req)
		if !errors.Is(err, test.want) {
			t.Errorf("%+v: %v, want %v", test.req, err, test.want)
		}
	}
}

func TestResolveChat(t *testing.T) {
	t.Parallel()

	a := connected(t, groupsFake())

	tests := []struct {
		arg  string
		want signal.Chat
	}{
		{aliceNumber, signal.Chat{Recipient: signal.Recipient{ACI: aliceACI, PNI: carolACI, Number: aliceNumber}}},
		{"group:" + groupID, groupChat()},
		{groupID, groupChat()},
		{"family", groupChat()},
		{app.SelfRecipient, signal.Chat{Recipient: signal.Recipient{ACI: testAccount().ACI, Number: testAccount().Number}}},
	}

	for _, tc := range tests {
		got, err := a.ResolveChat(t.Context(), tc.arg)
		if err != nil || got.Key() != tc.want.Key() {
			t.Errorf("%s: got %+v, %v; want %+v", tc.arg, got, err, tc.want)
		}
	}

	_, err := a.ResolveChat(t.Context(), nobody)
	if !errors.Is(err, signal.ErrUnknownGroup) {
		t.Errorf("unknown: %v, want ErrUnknownGroup", err)
	}
}

func TestLostConnection(t *testing.T) {
	t.Parallel()

	tests := []struct {
		evt  signal.Event
		want error
	}{
		{&signal.Message{}, nil},
		{&signal.Connection{State: signal.StateDisconnected}, nil},
		{&signal.Connection{State: signal.StateLoggedOut}, signal.ErrDeviceUnlinked},
		{&signal.Connection{State: signal.StateLoggedOut, Err: errBoom}, signal.ErrDeviceUnlinked},
		{&signal.Connection{State: signal.StateFailed}, signal.ErrConnectionFailed},
		{&signal.Connection{State: signal.StateFailed, Err: errBoom}, errBoom},
	}

	for _, tc := range tests {
		err := app.LostConnection(tc.evt)
		if (tc.want == nil) != (err == nil) || !errors.Is(err, tc.want) {
			t.Errorf("%+v: got %v, want %v", tc.evt, err, tc.want)
		}
	}
}
