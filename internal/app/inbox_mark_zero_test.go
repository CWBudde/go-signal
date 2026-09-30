package app_test

import (
	"slices"
	"testing"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/signal/signaltest"
)

func TestInboxMarkReadCursorBounds(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		cursor string
		result app.MarkReadResult
		unread []int64
	}{
		{"ready cursor zero", "0", app.MarkReadResult{}, []int64{1, 2}},
		{"zero with leading digit", "00", app.MarkReadResult{}, []int64{1, 2}},
		{"positive cursor", "1", app.MarkReadResult{Messages: 1, Senders: 1}, []int64{2}},
		{"omitted cursor", "", app.MarkReadResult{Messages: 2, Senders: 2}, []int64{}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			fake := directory()
			inbox := connected(t, fake).Inbox(app.InboxOptions{})

			ready, err := inbox.List(t.Context(), app.MessagesRequest{})
			if err != nil || ready.Cursor != "0" || len(ready.Entries) != 0 {
				t.Fatalf("empty ready page = %+v, %v; want cursor 0", ready, err)
			}

			err = inbox.Run(t.Context(), feed(
				incoming(aliceUser(), aliceChat(), 1000, "first unseen"),
				incoming(bobUser(), groupChat(), 2000, "second unseen"),
			))
			if err != nil {
				t.Fatalf("store unseen messages: %v", err)
			}

			res, err := inbox.MarkRead(t.Context(), app.MarkReadRequest{Cursor: test.cursor})
			if err != nil || res != test.result {
				t.Errorf("MarkRead(%q) = %+v, %v; want %+v", test.cursor, res, err, test.result)
			}

			assertUnreadAfterMark(t, fake, test.unread, test.result.Senders)
		})
	}
}

func assertUnreadAfterMark(t *testing.T, fake *signaltest.Fake, want []int64, receipts int) {
	t.Helper()

	var unread []int64

	for _, entry := range fake.Inbox() {
		if entry.Unread {
			unread = append(unread, entry.ID)
		}
	}

	if !slices.Equal(unread, want) || len(fake.Receipts()) != receipts {
		t.Errorf("unread IDs = %v, receipts = %+v; want unread %v and %d receipts",
			unread, fake.Receipts(), want, receipts)
	}
}

func TestInboxMarkReadZeroSkipsStore(t *testing.T) {
	t.Parallel()

	fake := directory()
	inbox := runInbox(t, sender(t, fake), app.InboxOptions{},
		incoming(aliceUser(), aliceChat(), 1000, "unseen"))
	fake.InboxErr = errBoom

	for _, cursor := range []string{"0", "00"} {
		res, err := inbox.MarkRead(t.Context(), app.MarkReadRequest{Cursor: cursor})
		if err != nil || res != (app.MarkReadResult{}) {
			t.Errorf("MarkRead(%q) with unavailable store = %+v, %v; want no-op success", cursor, res, err)
		}
	}

	if entries := fake.Inbox(); len(entries) != 1 || !entries[0].Unread || len(fake.Receipts()) != 0 {
		t.Errorf("entries = %+v, receipts = %+v; want one unread message and no receipts", entries, fake.Receipts())
	}
}
