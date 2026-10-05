//go:build cgo || libsignal_go

package signal_test

import (
	"testing"

	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow/protobuf/signalpb"
	"github.com/google/uuid"
)

func counterWire(count uint32) *signalpb.DataMessage {
	author := uuid.MustParse(bobUser)

	return &signalpb.DataMessage{Timestamp: new(uint64(100)), PollVote: &signalpb.DataMessage_PollVote{
		TargetAuthorAciBinary: author[:], TargetSentTimestamp: new(uint64(42)),
		VoteCount: new(count), OptionIndexes: []uint32{0},
	}}
}

// Mutation: removing receive-side persistence would reset the next counter to 1.
func TestPollVoteCounterOwnSync(t *testing.T) {
	t.Parallel()
	dir := seedAccount(t)
	client := openOffline(t, dir)

	for _, step := range []struct {
		sender string
		count  uint32
	}{{seededACI, 8}, {seededACI, 3}, {aliceUser, 99}} {
		done := make(chan bool, 1)
		go func() { done <- signal.Handle(client, timerEvent(step.sender, aliceUser, counterWire(step.count))) }()

		evt := <-client.Events()
		if _, ok := evt.(*signal.PollVote); !ok {
			t.Fatalf("event = %T", evt)
		}

		if !<-done {
			t.Fatal("valid vote not delivered")
		}
	}

	err := client.Close()
	if err != nil {
		t.Fatal(err)
	}

	client = openOffline(t, dir)

	got, err := client.ReservePollVote(t.Context(), signal.PollVoteCounterRequest{
		Chat: signal.Chat{Recipient: signal.Recipient{ACI: aliceUser}}, Author: signal.Recipient{ACI: bobUser}, Timestamp: 42,
	})
	if err != nil || got != 9 {
		t.Fatalf("after sync/reopen = %d, %v; want 9", got, err)
	}
}

// Mutation: acknowledging before persistence would lose the other device's counter on a write failure.
func TestPollVoteCounterPersistenceFailure(t *testing.T) {
	t.Parallel()
	dir := seedAccount(t)
	client := openOffline(t, dir)
	database := timerSQL(t, dir)
	execTimerSQL(t, database, `CREATE TRIGGER fail_poll_counter BEFORE INSERT ON gosignal_poll_counters
 BEGIN SELECT RAISE(ABORT, 'test counter write failure'); END`)

	done := make(chan bool, 1)
	go func() { done <- signal.Handle(client, timerEvent(seededACI, aliceUser, counterWire(8))) }()

	select {
	case ack := <-done:
		if ack {
			t.Fatal("failed persistence acknowledged")
		}
	case evt := <-client.Events():
		t.Fatalf("failed persistence emitted %T", evt)
	case <-t.Context().Done():
		t.Fatal(t.Context().Err())
	}

	if signal.Acked(client) {
		t.Fatal("pending ack after counter failure")
	}
}

func TestPollVoteCounterSendOnlyAndMalformed(t *testing.T) {
	t.Parallel()
	dir := seedAccount(t)

	client := openOffline(t, dir, signal.SendOnly())
	if signal.Handle(client, timerEvent(seededACI, aliceUser, counterWire(8))) {
		t.Fatal("send-only vote acknowledged")
	}

	got, err := client.ReservePollVote(t.Context(), signal.PollVoteCounterRequest{
		Chat: signal.Chat{Recipient: signal.Recipient{ACI: aliceUser}}, Author: signal.Recipient{ACI: bobUser}, Timestamp: 42,
	})
	if err != nil || got != 1 {
		t.Fatalf("unread event altered counter = %d, %v", got, err)
	}

	err = client.Close()
	if err != nil {
		t.Fatal(err)
	}

	client = openOffline(t, dir)
	malformed := counterWire(9)
	malformed.PollVote.TargetAuthorAciBinary = nil

	done := make(chan bool, 1)
	go func() { done <- signal.Handle(client, timerEvent(seededACI, aliceUser, malformed)) }()

	evt := <-client.Events()
	if _, ok := evt.(*signal.Unsupported); !ok {
		t.Fatalf("malformed event = %T", evt)
	}

	if !<-done {
		t.Fatal("unsupported event not delivered")
	}

	got, err = client.ReservePollVote(t.Context(), signal.PollVoteCounterRequest{
		Chat: signal.Chat{Recipient: signal.Recipient{ACI: aliceUser}}, Author: signal.Recipient{ACI: bobUser}, Timestamp: 42,
	})
	if err != nil || got != 2 {
		t.Fatalf("malformed event altered counter = %d, %v", got, err)
	}
}
