//go:build cgo

package signal_test

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/internal/signal"
)

// TestInbox runs the inbox methods of the real client on a seeded account, without connecting.
func TestInbox(t *testing.T) {
	t.Parallel()

	client, err := signal.Open(t.Context(), signal.Options{DataDir: seedAccount(t)})
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	alice := signal.Recipient{ACI: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", Number: "+4915199999999"}
	received := time.Date(2026, 9, 26, 8, 0, 0, 0, time.UTC)
	added := addInboxMessage(t, client, alice, received)

	entries, err := client.InboxList(t.Context(), signal.InboxQuery{Chat: added.Chat.Key()})
	if err != nil || len(entries) != 1 || !reflect.DeepEqual(entries[0], added) {
		t.Fatalf("InboxList = %+v, %v; want %+v", entries, err, added)
	}

	checkInboxChanges(t, client, added)

	err = client.Close()
	if err != nil {
		t.Fatal(err)
	}

	_, err = client.InboxList(t.Context(), signal.InboxQuery{})
	if !errors.Is(err, signal.ErrClosed) {
		t.Errorf("InboxList after Close: %v, want ErrClosed", err)
	}
}

// checkInboxChanges checks the chats of an inbox that only has added, then marks added read and
// deletes it.
func checkInboxChanges(t *testing.T, client signal.Client, added signal.InboxEntry) {
	t.Helper()

	chats, err := client.InboxChats(t.Context())
	if err != nil || len(chats) != 1 {
		t.Fatalf("InboxChats = %+v, %v; want one chat", chats, err)
	}

	if chats[0].Chat != added.Chat || chats[0].Unread != 1 || chats[0].Last.ID != added.ID {
		t.Errorf("chat %+v, want the one of %+v", chats[0], added)
	}

	msg, _ := added.Event.(*signal.Message)

	marked, err := client.InboxMarkRead(t.Context(), []signal.ReadMark{{Sender: msg.Sender, Timestamp: msg.Timestamp}})
	if err != nil || marked != 1 {
		t.Errorf("InboxMarkRead = %d, %v; want 1", marked, err)
	}

	deleted, err := client.InboxPrune(t.Context(), added.ReceivedAt.Add(time.Second), 0)
	if err != nil || deleted != 1 {
		t.Errorf("InboxPrune = %d, %v; want 1", deleted, err)
	}
}

// addInboxMessage stores an unread message from sender and checks that events without content
// can't be stored.
func addInboxMessage(
	t *testing.T, client signal.Client, sender signal.Recipient, received time.Time,
) signal.InboxEntry {
	t.Helper()

	chat := signal.Chat{Recipient: sender}
	msg := &signal.Message{Envelope: signal.Envelope{Sender: sender, Chat: chat, Timestamp: 1000}, Body: "hi"}

	added, err := client.InboxAdd(t.Context(), signal.InboxEntry{
		ReceivedAt: received, Time: received, Chat: chat, Event: msg, Unread: true,
	})
	if err != nil || added.ID == 0 {
		t.Fatalf("InboxAdd = %+v, %v", added, err)
	}

	_, err = client.InboxAdd(t.Context(), signal.InboxEntry{ReceivedAt: received, Event: &signal.QueueEmpty{}})
	if !errors.Is(err, signal.ErrNotStorable) {
		t.Errorf("InboxAdd(queueEmpty): %v, want ErrNotStorable", err)
	}

	return added
}
