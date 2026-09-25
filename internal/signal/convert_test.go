//go:build cgo

package signal_test

import (
	"errors"
	"fmt"
	"io"
	"reflect"
	"testing"

	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/google/uuid"
	"go.mau.fi/mautrix-signal/pkg/signalmeow"
	"go.mau.fi/mautrix-signal/pkg/signalmeow/events"
	"go.mau.fi/mautrix-signal/pkg/signalmeow/protobuf/signalpb"
)

func chatEvent(sender uuid.UUID, chatID string, content signalpb.ChatEventContent) *events.ChatEvent {
	return &events.ChatEvent{
		Info:  events.MessageInfo{Sender: sender, ChatID: chatID, ServerTimestamp: 99},
		Event: content,
	}
}

func TestConvertEvent(t *testing.T) { //nolint:funlen // table-driven
	t.Parallel()

	alice := uuid.MustParse("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
	bob := uuid.MustParse("bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb")
	aliceR := signal.Recipient{ACI: alice.String()}
	bobR := signal.Recipient{ACI: bob.String()}
	groupID := "Z3JvdXAtaWQtZ3JvdXAtaWQtZ3JvdXAtaWQtZ3JvdXA="
	errDecrypt := io.ErrUnexpectedEOF

	tests := []struct {
		name string
		in   events.SignalEvent
		want signal.Event
	}{
		{
			name: "direct message",
			in: chatEvent(alice, alice.String(), &signalpb.DataMessage{
				Timestamp: new(uint64(42)),
				Body:      new("hi"),
				Attachments: []*signalpb.AttachmentPointer{{
					ContentType: new("image/png"),
					FileName:    new("a.png"),
					Size:        new(uint32(7)),
				}},
				Quote: &signalpb.DataMessage_Quote{
					Id:        new(uint64(41)),
					AuthorAci: new(bob.String()),
					Text:      new("earlier"),
				},
			}),
			want: &signal.Message{
				Envelope: signal.Envelope{
					Sender: aliceR, Chat: signal.Chat{Recipient: aliceR}, Timestamp: 42, ServerTimestamp: 99,
				},
				Body:        "hi",
				Attachments: []signal.Attachment{{ContentType: "image/png", Filename: "a.png", Size: 7}},
				Quote:       &signal.Quote{Author: bobR, Timestamp: 41, Text: "earlier"},
			},
		},
		{
			name: "sync transcript to a group",
			in:   chatEvent(bob, groupID, &signalpb.DataMessage{Timestamp: new(uint64(42)), Body: new("yo")}),
			want: &signal.Message{
				Envelope: signal.Envelope{
					Sender: bobR, Chat: signal.Chat{GroupID: groupID}, Timestamp: 42, ServerTimestamp: 99, Sync: true,
				},
				Body: "yo",
			},
		},
		{
			name: "reaction",
			in: chatEvent(alice, alice.String(), &signalpb.DataMessage{
				Timestamp: new(uint64(43)),
				Reaction: &signalpb.DataMessage_Reaction{
					Emoji:               new("👍"),
					TargetAuthorAci:     new(bob.String()),
					TargetSentTimestamp: new(uint64(41)),
				},
			}),
			want: &signal.Reaction{
				Envelope: signal.Envelope{
					Sender: aliceR, Chat: signal.Chat{Recipient: aliceR}, Timestamp: 43, ServerTimestamp: 99,
				},
				Emoji: "👍", TargetAuthor: bobR, TargetTimestamp: 41,
			},
		},
		{
			name: "remote delete",
			in: chatEvent(alice, alice.String(), &signalpb.DataMessage{
				Timestamp: new(uint64(44)),
				Delete:    &signalpb.DataMessage_Delete{TargetSentTimestamp: new(uint64(42))},
			}),
			want: &signal.Delete{
				Envelope: signal.Envelope{
					Sender: aliceR, Chat: signal.Chat{Recipient: aliceR}, Timestamp: 44, ServerTimestamp: 99,
				},
				TargetTimestamp: 42,
			},
		},
		{
			name: "edit",
			in: chatEvent(alice, alice.String(), &signalpb.EditMessage{
				TargetSentTimestamp: new(uint64(42)),
				DataMessage:         &signalpb.DataMessage{Timestamp: new(uint64(45)), Body: new("fixed")},
			}),
			want: &signal.Edit{
				Envelope: signal.Envelope{
					Sender: aliceR, Chat: signal.Chat{Recipient: aliceR}, Timestamp: 45, ServerTimestamp: 99,
				},
				TargetTimestamp: 42, Body: "fixed",
			},
		},
		{
			name: "typing stopped",
			in: chatEvent(alice, alice.String(), &signalpb.TypingMessage{
				Timestamp: new(uint64(46)),
				Action:    signalpb.TypingMessage_STOPPED.Enum(),
			}),
			want: &signal.Typing{
				Envelope: signal.Envelope{
					Sender: aliceR, Chat: signal.Chat{Recipient: aliceR}, Timestamp: 46, ServerTimestamp: 99,
				},
			},
		},
		{
			name: "read receipt",
			in: &events.Receipt{Sender: alice, Content: &signalpb.ReceiptMessage{
				Type:      signalpb.ReceiptMessage_READ.Enum(),
				Timestamp: []uint64{1, 2},
			}},
			want: &signal.Receipt{Sender: aliceR, Type: signal.ReceiptRead, Timestamps: []uint64{1, 2}},
		},
		{
			name: "read sync",
			in: &events.ReadSelf{Timestamp: 5, Messages: []*signalpb.SyncMessage_Read{
				{SenderAci: new(alice.String()), Timestamp: new(uint64(3))},
			}},
			want: &signal.ReadSync{Timestamp: 5, Messages: []signal.ReadMark{{Sender: aliceR, Timestamp: 3}}},
		},
		{
			name: "decryption error",
			in:   &events.DecryptionError{Sender: alice, Timestamp: 7, Err: errDecrypt},
			want: &signal.DecryptionFailure{Sender: aliceR, Timestamp: 7, Err: errDecrypt},
		},
		{
			name: "queue empty",
			in:   &events.QueueEmpty{},
			want: &signal.QueueEmpty{},
		},
		{
			name: "logged out",
			in:   &events.LoggedOut{},
			want: &signal.Connection{State: signal.StateLoggedOut},
		},
		{
			name: "unsupported event",
			in:   &events.Call{},
			want: nil,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got := signal.ConvertEvent(test.in, bob.String())
			if !reflect.DeepEqual(got, test.want) {
				t.Errorf("got  %#v\nwant %#v", got, test.want)
			}
		})
	}
}

func TestConvertStatus(t *testing.T) {
	t.Parallel()

	//nolint:err113 // signalmeow's websocket errors are dynamic
	var (
		forbidden    = errors.New("403 opening websocket, we are logged out")
		unauthorized = fmt.Errorf("unexpected status opening websocket: %v", "401 Unauthorized")
		teapot       = fmt.Errorf("unexpected status opening websocket: %v", "418 I'm a teapot")
	)

	tests := []struct {
		name string
		in   signalmeow.SignalConnectionStatus
		want signal.Event
	}{
		{
			"connected",
			signalmeow.SignalConnectionStatus{Event: signalmeow.SignalConnectionEventConnected},
			&signal.Connection{State: signal.StateConnected},
		},
		{"logged out (403)", signalmeow.SignalConnectionStatus{
			Event: signalmeow.SignalConnectionEventLoggedOut, Err: forbidden,
		}, &signal.Connection{State: signal.StateLoggedOut, Err: forbidden}},
		{"unauthorized (401)", signalmeow.SignalConnectionStatus{
			Event: signalmeow.SignalConnectionEventFatalError, Err: unauthorized,
		}, &signal.Connection{State: signal.StateLoggedOut, Err: unauthorized}},
		{"other fatal error", signalmeow.SignalConnectionStatus{
			Event: signalmeow.SignalConnectionEventFatalError, Err: teapot,
		}, &signal.Connection{State: signal.StateError, Err: teapot}},
		{"clean shutdown", signalmeow.SignalConnectionStatus{Event: signalmeow.SignalConnectionCleanShutdown}, nil},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got := signal.ConvertStatus(test.in)
			if !reflect.DeepEqual(got, test.want) {
				t.Errorf("got  %#v\nwant %#v", got, test.want)
			}
		})
	}
}
