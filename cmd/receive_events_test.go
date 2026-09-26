package cmd_test

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/go-signal/internal/signal/signaltest"
)

// Users and chats in the receive tests; our own account is testAccount.
const (
	aliceACI = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	bobACI   = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	groupID  = "Z3JvdXAtaWQtZ3JvdXAtaWQtZ3JvdXAtaWQtZ3JvdXA="
)

// at returns the Signal timestamp (ms) seconds after 2026-09-20 12:30:00 UTC.
func at(seconds int) uint64 {
	sent := time.Date(2026, 9, 20, 12, 30, seconds, 0, time.UTC)

	return uint64(sent.UnixMilli()) //nolint:gosec // positive
}

// allEvents has every event type receive can print, in the variants that render differently.
func allEvents() []signal.Event {
	own := signal.Recipient{ACI: testAccount().ACI}
	alice := signal.Recipient{ACI: aliceACI}
	bob := signal.Recipient{ACI: bobACI}
	direct := func(sender signal.Recipient, ts uint64) signal.Envelope {
		return signal.Envelope{Sender: sender, Chat: signal.Chat{Recipient: sender}, Timestamp: ts, ServerTimestamp: ts + 500}
	}
	group := func(sender signal.Recipient, ts uint64) signal.Envelope {
		return signal.Envelope{Sender: sender, Chat: signal.Chat{GroupID: groupID}, Timestamp: ts, ServerTimestamp: ts + 500}
	}
	sync := func(chat signal.Chat, ts uint64) signal.Envelope {
		return signal.Envelope{Sender: own, Chat: chat, Timestamp: ts, ServerTimestamp: ts + 500, Sync: true}
	}

	return []signal.Event{
		&signal.Connection{State: signal.StateConnected},
		// Data messages: 1:1 with quote and attachment, group, sticker, view-once, unsupported part.
		&signal.Message{
			Envelope: direct(alice, at(1)),
			Body:     "hello\nworld",
			Attachments: []signal.Attachment{
				{ContentType: "image/jpeg", Filename: "photo.jpg", Size: 12345, Caption: "the view"},
			},
			Quote: &signal.Quote{Author: own, Timestamp: at(0), Text: "what are you up to this afternoon, anything fun?"},
		},
		&signal.Message{Envelope: group(alice, at(2)), Body: "hi all"},
		&signal.Message{
			Envelope: direct(bob, at(3)),
			Sticker:  &signal.Sticker{PackID: "0123456789abcdef0123456789abcdef", StickerID: 7, Emoji: "😀"},
		},
		&signal.Message{
			Envelope:    direct(bob, at(4)),
			Attachments: []signal.Attachment{{ContentType: "video/mp4", Size: 3_400_000}},
			ViewOnce:    true,
		},
		&signal.Message{Envelope: direct(bob, at(5)), Body: "nice story", Unsupported: []string{"storyReply"}},
		// Sync transcripts: sent from our phone to bob, to a group and to ourselves.
		&signal.Message{Envelope: sync(signal.Chat{Recipient: bob}, at(6)), Body: "sent from the phone"},
		&signal.Message{Envelope: sync(signal.Chat{GroupID: groupID}, at(7)), Body: "to the group"},
		&signal.Message{Envelope: sync(signal.Chat{Recipient: own}, at(8)), Body: "note to self"},
		&signal.Edit{Envelope: direct(alice, at(9)), TargetTimestamp: at(1), Body: "hello there"},
		&signal.Delete{Envelope: group(alice, at(10)), TargetTimestamp: at(2)},
		&signal.Reaction{Envelope: direct(bob, at(11)), Emoji: "👍", TargetAuthor: alice, TargetTimestamp: at(1)},
		&signal.Reaction{
			Envelope: direct(bob, at(12)), Emoji: "👍", Remove: true, TargetAuthor: alice, TargetTimestamp: at(1),
		},
		&signal.Typing{Envelope: group(alice, at(13)), Started: true},
		&signal.Typing{Envelope: group(alice, at(14))},
		&signal.Receipt{Sender: bob, Type: signal.ReceiptDelivery, Timestamps: []uint64{at(6)}},
		&signal.Receipt{Sender: bob, Type: signal.ReceiptRead, Timestamps: []uint64{at(6), at(7)}},
		&signal.Receipt{Sender: bob, Type: signal.ReceiptViewed, Timestamps: []uint64{at(6)}},
		&signal.ReadSync{Timestamp: at(15), Messages: []signal.ReadMark{
			{Sender: alice, Timestamp: at(1)}, {Sender: bob, Timestamp: at(3)},
		}},
		&signal.Unsupported{Envelope: direct(alice, at(16)), Type: "call"},
		&signal.Unsupported{Envelope: group(bob, at(17)), Type: "groupUpdate"},
		&signal.Unsupported{Envelope: sync(signal.Chat{}, at(18)), Type: "deleteForMe"},
		&signal.DecryptionFailure{Sender: alice, Timestamp: at(19), Err: io.ErrUnexpectedEOF},
		&signal.QueueEmpty{},
		&signal.Connection{State: signal.StateDisconnected, Err: io.ErrUnexpectedEOF},
	}
}

// receiveAll runs `receive --follow` with args until all events are printed.
func receiveAll(t *testing.T, events []signal.Event, args ...string) string {
	t.Helper()

	return receiveAllFrom(t, &signaltest.Fake{Linked: []signal.Account{*testAccount()}}, events, args...)
}

// receiveAllFrom is receiveAll on fake.
func receiveAllFrom(t *testing.T, fake *signaltest.Fake, events []signal.Event, args ...string) string {
	t.Helper()

	fake.Incoming = events

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	go func() {
		for fake.Delivered() < len(events) {
			time.Sleep(time.Millisecond)
		}

		cancel()
	}()

	out, err := runContext(t, ctx, fake, append([]string{"receive", "--follow"}, args...)...)
	if err != nil {
		t.Fatalf("receive: %v", err)
	}

	return out
}

func TestReceiveEventsPlain(t *testing.T) {
	t.Parallel()

	golden(t, "receive_events", receiveAll(t, allEvents()))
}

func TestReceiveEventsJSON(t *testing.T) {
	t.Parallel()

	golden(t, "receive_events_json", receiveAll(t, allEvents(), "-o", "json"))
}

// namedContacts are alice with a name and bob known only by number.
func namedContacts() []signal.Contact {
	return []signal.Contact{
		{Recipient: signal.Recipient{ACI: aliceACI, Number: aliceNumber}, ContactName: "Alice Smith", ProfileName: "Ali"},
		{Recipient: signal.Recipient{ACI: bobACI, Number: "+15550102"}},
	}
}

// TestReceiveEventsNames shows the contacts' names (or numbers) instead of their ACIs, and the
// titles of known groups.
func TestReceiveEventsNames(t *testing.T) {
	t.Parallel()

	goldens := map[string]string{"receive_events_names": formatPlain, "receive_events_names_json": formatJSON}

	for name, format := range goldens {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			fake := &signaltest.Fake{
				Linked: []signal.Account{*testAccount()}, Contacts: namedContacts(),
				GroupTitleCache: map[string]signal.CachedGroup{groupID: {Title: "Family"}},
			}
			golden(t, name, receiveAllFrom(t, fake, allEvents(), "-o", format))
		})
	}
}
