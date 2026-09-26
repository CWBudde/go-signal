package app_test

import (
	"testing"
	"time"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/signal"
)

func TestInboxEntryChatAndTime(t *testing.T) {
	t.Parallel()

	fake := directory()
	env := signal.Envelope{Sender: aliceUser(), Chat: groupChat(), Timestamp: 3000}
	// Timestamps beyond int64 can't be times; the entry gets the receive time instead.
	future := signal.Envelope{Sender: aliceUser(), Chat: aliceChat(), Timestamp: 1 << 63}
	changedAt := time.UnixMilli(5000)

	events := []signal.Event{
		&signal.Edit{Envelope: env, TargetTimestamp: 1000, Body: "fixed"},
		&signal.Delete{Envelope: env, TargetTimestamp: 1000},
		&signal.Reaction{Envelope: env, Emoji: "👍", TargetAuthor: aliceUser(), TargetTimestamp: 1000},
		&signal.Unsupported{Envelope: env, Type: "call"},
		&signal.DecryptionFailure{Sender: bobUser(), Timestamp: 4000},
		&signal.IdentityChanged{Recipient: bobUser(), NewFingerprint: "05bb", Time: changedAt},
		&signal.IdentityChanged{Recipient: bobUser(), NewFingerprint: "05cc"},
		&signal.Unsupported{Envelope: future, Type: "storyMessage"},
	}

	runInbox(t, open(t, fake), app.InboxOptions{}, events...)

	entries := fake.Inbox()
	if len(entries) != len(events) {
		t.Fatalf("stored %d entries, want %d", len(entries), len(events))
	}

	bobChat := signal.Chat{Recipient: bobUser()}

	for i, want := range []struct {
		chat signal.Chat
		time time.Time
	}{
		{groupChat(), time.UnixMilli(3000)},
		{groupChat(), time.UnixMilli(3000)},
		{groupChat(), time.UnixMilli(3000)},
		{groupChat(), time.UnixMilli(3000)},
		{bobChat, time.UnixMilli(4000)},
		{bobChat, changedAt},
		{bobChat, entries[6].ReceivedAt},
		{aliceChat(), entries[7].ReceivedAt},
	} {
		entry := entries[i]
		if entry.Chat.Key() != want.chat.Key() || !entry.Time.Equal(want.time) {
			t.Errorf("entry %d (%T): chat %q at %v, want %q at %v",
				i, entry.Event, entry.Chat.Key(), entry.Time, want.chat.Key(), want.time)
		}

		// Only messages from others are unread.
		if entry.Unread {
			t.Errorf("entry %d (%T) is unread", i, entry.Event)
		}
	}
}
