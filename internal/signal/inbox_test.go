package signal_test

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/internal/signal"
)

const (
	testGroupID = "Zm9v"
	pngType     = "image/png"
)

var errNoSession = errors.New("no session")

func TestChatKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in   signal.Chat
		want string
	}{
		{signal.Chat{GroupID: testGroupID}, "group:" + testGroupID},
		{signal.Chat{Recipient: signal.Recipient{ACI: "the-aci", Number: "+1"}}, "the-aci"},
		{signal.Chat{Recipient: signal.Recipient{Number: "+1"}}, "+1"},
		{signal.Chat{}, ""},
	}

	for _, tc := range tests {
		if got := tc.in.Key(); got != tc.want {
			t.Errorf("%+v: got %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestInboxEventRoundTrip stores every storable event type and checks that it comes back as it
// was, with its chat.
func TestInboxEventRoundTrip(t *testing.T) {
	t.Parallel()

	alice := signal.Recipient{ACI: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", Number: "+4915199999999"}
	env := signal.Envelope{Sender: alice, Chat: signal.Chat{Recipient: alice}, Timestamp: 1000, ServerTimestamp: 1001}
	chat := signal.Chat{GroupID: testGroupID}

	events := []signal.Event{
		&signal.Message{
			Envelope: env, Body: "hi",
			Attachments: []signal.Attachment{{
				ContentType: pngType, Filename: "a.png", Size: 3,
				Remote: signal.RemoteAttachment{CDNNumber: 3, CDNKey: "key", Key: []byte{1, 2}, Digest: []byte{3}},
			}},
			Sticker:  &signal.Sticker{PackID: "ab", StickerID: 2, Emoji: "x"},
			Quote:    &signal.Quote{Author: alice, Timestamp: 900, Text: "q"},
			ViewOnce: true, Unsupported: []string{"contact"},
		},
		&signal.Edit{Envelope: env, TargetTimestamp: 900, Body: "edited"},
		&signal.Delete{Envelope: env, TargetTimestamp: 900},
		&signal.Reaction{Envelope: env, Emoji: "👍", Remove: true, TargetAuthor: alice, TargetTimestamp: 900},
		&signal.Typing{Envelope: env, Started: true},
		&signal.Receipt{Sender: alice, Type: signal.ReceiptRead, Timestamps: []uint64{1, 2}},
		&signal.ReadSync{Timestamp: 5, Messages: []signal.ReadMark{{Sender: alice, Timestamp: 4}}},
		&signal.Unsupported{Envelope: env, Type: "call"},
		&signal.DecryptionFailure{Sender: alice, Timestamp: 7},
		&signal.IdentityChanged{
			Recipient: alice, OldFingerprint: "05aa", NewFingerprint: "05bb",
			Time: time.Date(2026, 9, 26, 8, 0, 0, 0, time.UTC),
		},
	}

	for _, evt := range events {
		data, err := signal.MarshalEvent(evt, chat)
		if err != nil {
			t.Fatalf("%T: %v", evt, err)
		}

		got, gotChat := signal.UnmarshalEvent(data)
		if !reflect.DeepEqual(got, evt) || gotChat != chat {
			t.Errorf("%T: got %+v in %+v, want %+v", evt, got, gotChat, evt)
		}
	}
}

func TestInboxDecryptionFailureError(t *testing.T) {
	t.Parallel()

	data, err := signal.MarshalEvent(&signal.DecryptionFailure{Timestamp: 7, Err: errNoSession}, signal.Chat{})
	if err != nil {
		t.Fatal(err)
	}

	got, _ := signal.UnmarshalEvent(data)

	failure, ok := got.(*signal.DecryptionFailure)
	if !ok || failure.Err == nil || failure.Err.Error() != errNoSession.Error() {
		t.Errorf("got %+v, want the error text back", got)
	}
}

func TestInboxNotStorable(t *testing.T) {
	t.Parallel()

	for _, evt := range []signal.Event{&signal.Connection{State: signal.StateConnected}, &signal.QueueEmpty{}} {
		_, err := signal.MarshalEvent(evt, signal.Chat{})
		if !errors.Is(err, signal.ErrNotStorable) {
			t.Errorf("%T: got %v, want ErrNotStorable", evt, err)
		}
	}
}

func TestInboxUnreadable(t *testing.T) {
	t.Parallel()

	chat := signal.Chat{GroupID: testGroupID}

	for _, data := range []string{
		`not json`,
		`{"type":"future","event":{},"chat":{"GroupID":"Zm9v"}}`,
		`{"type":"message","event":[],"chat":{"GroupID":"Zm9v"}}`,
	} {
		got, gotChat := signal.UnmarshalEvent([]byte(data))

		unsupported, ok := got.(*signal.Unsupported)
		if !ok || unsupported.Type != signal.UnreadableEntry {
			t.Errorf("%s: got %+v, want an unreadable entry", data, got)
		}

		if data != `not json` && gotChat != chat {
			t.Errorf("%s: chat %+v, want %+v", data, gotChat, chat)
		}
	}
}
