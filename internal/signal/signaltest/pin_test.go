//nolint:cyclop,funlen,lll // complete payload fixtures and ownership assertions
package signaltest_test

import (
	"testing"

	"github.com/cwbudde/go-signal/internal/signal"
)

func TestPinFakeCopies(t *testing.T) {
	t.Parallel()

	fake := profileFake()
	cli := profileClient(t, fake, "+12025550101", true)
	author := signal.Recipient{ACI: profileAliceACI}
	pin := &signal.OutgoingPin{TargetAuthor: author, TargetTimestamp: 42, DurationSeconds: 60}

	unpin := &signal.OutgoingUnpin{TargetAuthor: author, TargetTimestamp: 42}
	for _, req := range []signal.SendRequest{
		{Recipients: []signal.Recipient{author}, Pin: pin},
		{Recipients: []signal.Recipient{author}, Unpin: unpin},
	} {
		_, err := cli.Send(t.Context(), req)
		if err != nil {
			t.Fatal(err)
		}
	}

	pin.TargetTimestamp, pin.DurationSeconds, unpin.TargetTimestamp = 100, 99, 101

	sent := fake.Sent()
	if sent[0].Pin.TargetTimestamp != 42 || sent[0].Pin.DurationSeconds != 60 || sent[1].Unpin.TargetTimestamp != 42 {
		t.Fatal("send aliases input pin/unpin")
	}

	sent[0].Pin.Forever, sent[1].Unpin.TargetTimestamp = true, 200
	if fake.Sent()[0].Pin.Forever || fake.Sent()[1].Unpin.TargetTimestamp != 42 {
		t.Fatal("Sent aliases retained controls")
	}
}

func TestPinFakeInboxCopies(t *testing.T) {
	t.Parallel()

	fake := profileFake()
	cli := profileClient(t, fake, "+12025550101", false)
	env := signal.Envelope{Sender: signal.Recipient{ACI: profileAliceACI}, Chat: signal.Chat{Recipient: signal.Recipient{ACI: profileAliceACI}}, Timestamp: 43}
	pin := &signal.Pin{Envelope: env, OutgoingPin: signal.OutgoingPin{TargetAuthor: env.Sender, TargetTimestamp: 42, DurationSeconds: 60}}

	unpin := &signal.Unpin{Envelope: env, OutgoingUnpin: signal.OutgoingUnpin{TargetAuthor: env.Sender, TargetTimestamp: 42}}
	for _, event := range []signal.Event{pin, unpin} {
		entry, err := cli.InboxAdd(t.Context(), signal.InboxEntry{Chat: env.Chat, Event: event})
		if err != nil {
			t.Fatal(err)
		}

		switch returned := entry.Event.(type) {
		case *signal.Pin:
			returned.TargetTimestamp = 100
		case *signal.Unpin:
			returned.TargetTimestamp = 100
		}
	}

	pin.TargetTimestamp, unpin.TargetTimestamp = 101, 102

	entries, err := cli.InboxList(t.Context(), signal.InboxQuery{})
	if err != nil {
		t.Fatal(err)
	}

	storedPin, pinOK := entries[0].Event.(*signal.Pin)

	storedUnpin, unpinOK := entries[1].Event.(*signal.Unpin)
	if !pinOK || !unpinOK || storedPin.TargetTimestamp != 42 || storedUnpin.TargetTimestamp != 42 {
		t.Fatalf("inbox aliases input or returned entry: %#v", entries)
	}

	storedPin.DurationSeconds, storedUnpin.TargetTimestamp = 99, 103
	snapshot := fake.Inbox()
	snapshotPin, pinOK := snapshot[0].Event.(*signal.Pin)

	snapshotUnpin, unpinOK := snapshot[1].Event.(*signal.Unpin)
	if !pinOK || !unpinOK || snapshotPin.DurationSeconds != 60 || snapshotUnpin.TargetTimestamp != 42 {
		t.Fatal("InboxList aliases retained controls")
	}

	snapshotPin.DurationSeconds, snapshotUnpin.TargetTimestamp = 100, 104

	chats, err := cli.InboxChats(t.Context())
	if err != nil || len(chats) != 1 {
		t.Fatalf("chats = %#v, %v", chats, err)
	}

	last, ok := chats[0].Last.Event.(*signal.Unpin)
	if !ok || last.TargetTimestamp != 42 {
		t.Fatal("snapshot aliases retained controls")
	}

	last.TargetTimestamp = 105

	entries, err = cli.InboxList(t.Context(), signal.InboxQuery{})
	if err != nil {
		t.Fatal(err)
	}

	storedUnpin, ok = entries[1].Event.(*signal.Unpin)
	if !ok || storedUnpin.TargetTimestamp != 42 {
		t.Fatal("InboxChats aliases retained unpin")
	}
}
