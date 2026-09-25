//go:build cgo

package signal_test

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"reflect"
	"testing"

	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/google/uuid"
	"go.mau.fi/mautrix-signal/pkg/libsignalgo"
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

type convertTest struct {
	name string
	in   events.SignalEvent
	want signal.Event
}

// contentTests cover sync transcripts, rich content and what becomes an *Unsupported event. bob
// is our own account.
//
//nolint:funlen // table-driven
func contentTests(alice, bob uuid.UUID, groupID string) []convertTest {
	aliceR := signal.Recipient{ACI: alice.String()}
	bobR := signal.Recipient{ACI: bob.String()}
	direct := signal.Envelope{Sender: aliceR, Chat: signal.Chat{Recipient: aliceR}, Timestamp: 50, ServerTimestamp: 99}
	group := signal.Envelope{Sender: aliceR, Chat: signal.Chat{GroupID: groupID}, Timestamp: 50, ServerTimestamp: 99}
	unsupported := func(env signal.Envelope, typ string) *signal.Unsupported {
		return &signal.Unsupported{Envelope: env, Type: typ}
	}
	dataMessage := func(msg *signalpb.DataMessage) *events.ChatEvent {
		msg.Timestamp = new(uint64(50))

		return chatEvent(alice, alice.String(), msg)
	}

	return []convertTest{
		{
			name: "sync transcript to a contact carries the destination",
			in:   chatEvent(bob, alice.String(), &signalpb.DataMessage{Timestamp: new(uint64(42)), Body: new("hi")}),
			want: &signal.Message{
				Envelope: signal.Envelope{
					Sender: bobR, Chat: signal.Chat{Recipient: aliceR}, Timestamp: 42, ServerTimestamp: 99, Sync: true,
				},
				Body: "hi",
			},
		},
		{
			name: "sync transcript of an edit",
			in: chatEvent(bob, alice.String(), &signalpb.EditMessage{
				TargetSentTimestamp: new(uint64(42)),
				DataMessage:         &signalpb.DataMessage{Timestamp: new(uint64(45)), Body: new("fixed")},
			}),
			want: &signal.Edit{
				Envelope: signal.Envelope{
					Sender: bobR, Chat: signal.Chat{Recipient: aliceR}, Timestamp: 45, ServerTimestamp: 99, Sync: true,
				},
				TargetTimestamp: 42, Body: "fixed",
			},
		},
		{
			name: "sticker",
			in: dataMessage(&signalpb.DataMessage{Sticker: &signalpb.DataMessage_Sticker{
				PackId: []byte{0xca, 0xfe}, StickerId: new(uint32(3)), Emoji: new("😀"),
			}}),
			want: &signal.Message{
				Envelope: direct,
				Sticker:  &signal.Sticker{PackID: "cafe", StickerID: 3, Emoji: "😀"},
			},
		},
		{
			name: "view-once story reply",
			in: dataMessage(&signalpb.DataMessage{
				Body:         new("nice"),
				IsViewOnce:   new(true),
				StoryContext: &signalpb.DataMessage_StoryContext{},
			}),
			want: &signal.Message{Envelope: direct, Body: "nice", ViewOnce: true, Unsupported: []string{"storyReply"}},
		},
		{
			name: "contact card",
			in:   dataMessage(&signalpb.DataMessage{Contact: []*signalpb.DataMessage_Contact{{}}}),
			want: unsupported(direct, "contact"),
		},
		{
			name: "poll",
			in:   dataMessage(&signalpb.DataMessage{PollCreate: &signalpb.DataMessage_PollCreate{}}),
			want: unsupported(direct, "pollCreate"),
		},
		{
			name: "end session",
			in:   dataMessage(&signalpb.DataMessage{Flags: new(uint32(1))}),
			want: unsupported(direct, "endSession"),
		},
		{
			name: "expiration timer update",
			in: dataMessage(&signalpb.DataMessage{
				Flags: new(uint32(signalpb.DataMessage_EXPIRATION_TIMER_UPDATE)), ExpireTimer: new(uint32(60)),
			}),
			want: unsupported(direct, "expirationTimerUpdate"),
		},
		{
			name: "profile key update",
			in: dataMessage(&signalpb.DataMessage{
				Flags: new(uint32(signalpb.DataMessage_PROFILE_KEY_UPDATE)), ProfileKey: []byte{1},
			}),
			want: unsupported(direct, "profileKeyUpdate"),
		},
		{
			name: "group update",
			in: chatEvent(alice, groupID, &signalpb.DataMessage{
				Timestamp: new(uint64(50)),
				GroupV2:   &signalpb.GroupContextV2{Revision: new(uint32(2)), GroupChange: []byte{1}},
			}),
			want: unsupported(group, "groupUpdate"),
		},
		{
			name: "empty data message",
			in:   dataMessage(&signalpb.DataMessage{}),
			want: unsupported(direct, "dataMessage"),
		},
		{
			name: "call",
			in: &events.Call{
				Info:      events.MessageInfo{Sender: alice, ChatID: alice.String(), ServerTimestamp: 99},
				Timestamp: 50,
				IsRinging: true,
			},
			want: unsupported(direct, "call"),
		},
		{
			name: "delete for me",
			in:   &events.DeleteForMe{Timestamp: 50, SyncMessage_DeleteForMe: &signalpb.SyncMessage_DeleteForMe{}},
			want: unsupported(signal.Envelope{Sender: bobR, Timestamp: 50, Sync: true}, "deleteForMe"),
		},
		{
			name: "message request response in a 1:1 chat",
			in: &events.MessageRequestResponse{
				Timestamp: 50, ThreadACI: alice, Type: signalpb.SyncMessage_MessageRequestResponse_ACCEPT,
			},
			want: unsupported(
				signal.Envelope{Sender: bobR, Chat: signal.Chat{Recipient: aliceR}, Timestamp: 50, Sync: true},
				"messageRequestResponse",
			),
		},
		{
			name: "message request response in a group",
			in: &events.MessageRequestResponse{
				Timestamp: 50,
				GroupID:   groupIdentifier(groupID),
				Type:      signalpb.SyncMessage_MessageRequestResponse_BLOCK,
			},
			want: unsupported(
				signal.Envelope{Sender: bobR, Chat: signal.Chat{GroupID: groupID}, Timestamp: 50, Sync: true},
				"messageRequestResponse",
			),
		},
	}
}

func groupIdentifier(id string) *libsignalgo.GroupIdentifier {
	raw, err := base64.StdEncoding.DecodeString(id)
	if err != nil || len(raw) != libsignalgo.GroupIdentifierLength {
		panic("invalid group ID " + id)
	}

	return (*libsignalgo.GroupIdentifier)(raw)
}

func TestConvertEvent(t *testing.T) { //nolint:funlen // table-driven
	t.Parallel()

	alice := uuid.MustParse("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
	bob := uuid.MustParse("bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb")
	aliceR := signal.Recipient{ACI: alice.String()}
	bobR := signal.Recipient{ACI: bob.String()}
	groupID := "Z3JvdXAtaWQtZ3JvdXAtaWQtZ3JvdXAtaWQtZ3JvdXA="
	errDecrypt := io.ErrUnexpectedEOF

	tests := append(contentTests(alice, bob, groupID), []convertTest{
		{
			name: "direct message",
			in: chatEvent(alice, alice.String(), &signalpb.DataMessage{
				Timestamp: new(uint64(42)),
				Body:      new("hi"),
				Attachments: []*signalpb.AttachmentPointer{{
					AttachmentIdentifier: &signalpb.AttachmentPointer_CdnKey{CdnKey: "cdn-key"},
					CdnNumber:            new(uint32(3)),
					Key:                  []byte("key"),
					Digest:               []byte("digest"),
					ContentType:          new("image/png"),
					FileName:             new("a.png"),
					Size:                 new(uint32(7)),
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
				Body: "hi",
				Attachments: []signal.Attachment{{
					ContentType: "image/png", Filename: "a.png", Size: 7,
					Remote: signal.RemoteAttachment{
						CDNNumber: 3, CDNKey: "cdn-key", Key: []byte("key"), Digest: []byte("digest"),
					},
				}},
				Quote: &signal.Quote{Author: bobR, Timestamp: 41, Text: "earlier"},
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
			name: "contact list is a store update",
			in:   &events.ContactList{},
			want: nil,
		},
		{
			name: "ACI found is a store update",
			in:   &events.ACIFound{},
			want: nil,
		},
	}...)

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

func TestConvertLoopStatus(t *testing.T) {
	t.Parallel()

	//nolint:err113 // signalmeow's websocket errors are dynamic
	var (
		transient = errors.New("transient error opening websocket: dial tcp: no route to host")
		teapot    = fmt.Errorf("unexpected status opening websocket: %v", "418 I'm a teapot")
	)

	tests := []struct {
		name string
		in   signalmeow.SignalConnectionStatus
		want signal.LoopStatus
	}{
		{
			"connected",
			signalmeow.SignalConnectionStatus{Event: signalmeow.SignalConnectionEventConnected},
			signal.LoopStatus{State: signal.StateConnected},
		},
		{
			"disconnected: signalmeow retries",
			signalmeow.SignalConnectionStatus{Event: signalmeow.SignalConnectionEventDisconnected, Err: transient},
			signal.LoopStatus{State: signal.StateDisconnected, Err: transient},
		},
		{
			"fatal error: loops stopped",
			signalmeow.SignalConnectionStatus{Event: signalmeow.SignalConnectionEventFatalError, Err: teapot},
			signal.LoopStatus{State: signal.StateError, Err: teapot, Stopped: true},
		},
		{
			"clean shutdown",
			signalmeow.SignalConnectionStatus{Event: signalmeow.SignalConnectionCleanShutdown},
			signal.LoopStatus{},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got := signal.ConvertLoopStatus(test.in)
			if !reflect.DeepEqual(got, test.want) {
				t.Errorf("got  %#v\nwant %#v", got, test.want)
			}
		})
	}
}
