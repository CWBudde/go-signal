//go:build cgo || purego

package store_test

import (
	"fmt"
	"io"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/internal/store"
)

const (
	aliceChat = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	groupChat = "group:Zm9v"
)

// addInbox stores records with the given chats, one minute apart from base, the first two of
// them unread messages from Alice (timestamps 1 and 2), and returns their IDs.
func addInbox(t *testing.T, data *store.Store, base time.Time, chats ...string) []int64 {
	t.Helper()

	ids := make([]int64, 0, len(chats))

	for i, chat := range chats {
		rec := store.InboxRecord{
			ReceivedAt: base.Add(time.Duration(i) * time.Minute), Time: base.Add(time.Duration(i) * time.Minute),
			Chat: chat, Event: []byte(`{}`),
		}

		if i < 2 {
			rec.Sender, rec.Timestamp, rec.Unread = aliceChat, uint64(i+1), true
		}

		id, err := data.AddInboxRecord(t.Context(), rec)
		if err != nil {
			t.Fatalf("AddInboxRecord: %v", err)
		}

		ids = append(ids, id)
	}

	return ids
}

func recordIDs(recs []store.InboxRecord) []int64 {
	ids := make([]int64, 0, len(recs))
	for _, rec := range recs {
		ids = append(ids, rec.ID)
	}

	return ids
}

func TestInboxRecords(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	data := openAccount(t, openDir(t, io.Discard))
	base := time.Date(2026, 9, 26, 8, 0, 0, 0, time.UTC)
	ids := addInbox(t, data, base, aliceChat, groupChat, aliceChat, "")

	tests := []struct {
		name   string
		filter store.InboxFilter
		want   []int64
	}{
		{"all", store.InboxFilter{}, ids},
		{"after", store.InboxFilter{After: ids[1]}, ids[2:]},
		{"until", store.InboxFilter{Until: ids[1]}, ids[:2]},
		{"chat", store.InboxFilter{Chat: aliceChat}, []int64{ids[0], ids[2]}},
		{"since", store.InboxFilter{Since: base.Add(2 * time.Minute)}, ids[2:]},
		{"unread", store.InboxFilter{Unread: true}, ids[:2]},
		{"first", store.InboxFilter{Limit: 2}, ids[:2]},
		{"newest", store.InboxFilter{Limit: 2, Newest: true}, ids[2:]},
	}

	for _, test := range tests {
		recs, err := data.InboxRecords(ctx, test.filter)
		if err != nil {
			t.Fatalf("%s: %v", test.name, err)
		}

		if got := recordIDs(recs); !slices.Equal(got, test.want) {
			t.Errorf("%s: got %v, want %v", test.name, got, test.want)
		}
	}
}

func TestInboxRecordFields(t *testing.T) {
	t.Parallel()

	data := openAccount(t, openDir(t, io.Discard))
	base := time.Date(2026, 9, 26, 8, 0, 0, 0, time.UTC)
	ids := addInbox(t, data, base, aliceChat)

	recs, err := data.InboxRecords(t.Context(), store.InboxFilter{})
	if err != nil || len(recs) != 1 {
		t.Fatalf("records: %+v, %v", recs, err)
	}

	want := store.InboxRecord{
		ID: ids[0], ReceivedAt: base, Time: base, Chat: aliceChat, Sender: aliceChat, Timestamp: 1, Unread: true,
		Event: []byte(`{}`),
	}
	if got := recs[0]; !reflect.DeepEqual(got, want) {
		t.Errorf("record %+v, want %+v", got, want)
	}
}

func TestInboxChats(t *testing.T) {
	t.Parallel()

	data := openAccount(t, openDir(t, io.Discard))
	ids := addInbox(t, data, time.Now(), aliceChat, aliceChat, groupChat, "")

	chats, err := data.InboxChats(t.Context())
	if err != nil || len(chats) != 2 {
		t.Fatalf("InboxChats = %+v, %v; want two chats", chats, err)
	}

	// The group has the newest record; the record without a chat is left out.
	want := []string{
		fmt.Sprintf("%s: 1 entries, 0 unread, last %d", groupChat, ids[2]),
		fmt.Sprintf("%s: 2 entries, 2 unread, last %d", aliceChat, ids[1]),
	}

	for i, chat := range chats {
		got := fmt.Sprintf("%s: %d entries, %d unread, last %d", chat.Chat, chat.Entries, chat.Unread, chat.Last.ID)
		if got != want[i] {
			t.Errorf("chat %d: %s, want %s", i, got, want[i])
		}
	}
}

func TestMarkInboxRead(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	data := openAccount(t, openDir(t, io.Discard))
	ids := addInbox(t, data, time.Now(), aliceChat, aliceChat)
	marks := []store.InboxMark{{Sender: aliceChat, Timestamp: 2}, {Sender: "x", Timestamp: 1}}

	marked, err := data.MarkInboxRead(ctx, marks)
	if err != nil || marked != 1 {
		t.Fatalf("MarkInboxRead = %d, %v; want 1", marked, err)
	}

	// Marking again changes nothing.
	marked, err = data.MarkInboxRead(ctx, marks)
	if err != nil || marked != 0 {
		t.Errorf("MarkInboxRead again = %d, %v; want 0", marked, err)
	}

	unread, err := data.InboxRecords(ctx, store.InboxFilter{Unread: true})
	if err != nil || !slices.Equal(recordIDs(unread), ids[:1]) {
		t.Errorf("unread = %v, %v; want %v", recordIDs(unread), err, ids[:1])
	}
}

func TestPruneInbox(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	data := openAccount(t, openDir(t, io.Discard))
	base := time.Date(2026, 9, 26, 8, 0, 0, 0, time.UTC)
	ids := addInbox(t, data, base, aliceChat, aliceChat, aliceChat, aliceChat, aliceChat)

	deleted, err := data.PruneInbox(ctx, base.Add(time.Minute), 0)
	if err != nil || deleted != 1 {
		t.Fatalf("prune by age = %d, %v; want 1", deleted, err)
	}

	deleted, err = data.PruneInbox(ctx, time.Time{}, 2)
	if err != nil || deleted != 2 {
		t.Fatalf("prune by count = %d, %v; want 2", deleted, err)
	}

	recs, err := data.InboxRecords(ctx, store.InboxFilter{})
	if err != nil || !slices.Equal(recordIDs(recs), ids[3:]) {
		t.Fatalf("left %v, %v; want %v", recordIDs(recs), err, ids[3:])
	}

	// IDs are not reused after pruning, so that cursors stay valid.
	next := addInbox(t, data, base, aliceChat)
	if next[0] <= ids[4] {
		t.Errorf("new ID %d, want > %d", next[0], ids[4])
	}
}
