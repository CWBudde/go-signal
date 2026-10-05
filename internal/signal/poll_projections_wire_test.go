//go:build cgo || libsignal_go

package signal_test

import (
	"slices"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow/protobuf/signalpb"
)

func durableRef() signal.PollReference {
	return signal.PollReference{
		Chat:   signal.Chat{Recipient: signal.Recipient{ACI: aliceUser}},
		Author: signal.Recipient{ACI: bobUser}, Timestamp: 42,
	}
}

func pollCreationWire() *signalpb.DataMessage {
	return &signalpb.DataMessage{Timestamp: new(uint64(42)), PollCreate: &signalpb.DataMessage_PollCreate{
		Question: new("When?"), Options: []string{"a", "b"}, AllowMultiple: new(true),
	}}
}

func deliverPollWire(t *testing.T, client signal.Client, sender string, wire *signalpb.DataMessage) {
	t.Helper()

	done := make(chan bool, 1)
	go func() { done <- signal.Handle(client, timerEvent(sender, aliceUser, wire)) }()

	<-client.Events()

	if !<-done {
		t.Fatal("event not delivered")
	}
}

func TestPollProjectionReceivePruneReopen(t *testing.T) {
	t.Parallel()
	dir := seedAccount(t)
	client := openOffline(t, dir)
	// A vote before creation must be revalidated against the late creation.
	deliverPollWire(t, client, aliceUser, counterWire(8))
	deliverPollWire(t, client, bobUser, pollCreationWire())
	deliverPollWire(t, client, aliceUser, counterWire(8))

	_, err := client.InboxPrune(t.Context(), time.Now().Add(time.Hour), 0)
	if err != nil {
		t.Fatal(err)
	}

	err = client.Close()
	if err != nil {
		t.Fatal(err)
	}

	client = openOffline(t, dir)

	state, err := client.PollProjection(t.Context(), durableRef())
	if err != nil || state.Creation == nil || !slices.Equal(state.Tally, []int{1, 0}) ||
		state.Observations != 2 {
		t.Fatalf("durable state = %+v, %v", state, err)
	}

	entries, err := client.InboxList(t.Context(), signal.InboxQuery{})
	if err != nil || len(entries) != 0 {
		t.Fatalf("ordinary receive inbox = %v, %v", entries, err)
	}
}

func TestPollProjectionPersistenceBeforeAck(t *testing.T) {
	t.Parallel()
	dir := seedAccount(t)
	client := openOffline(t, dir)
	database := timerSQL(t, dir)
	execTimerSQL(t, database, `CREATE TRIGGER fail_poll_projection BEFORE INSERT ON gosignal_poll_projections
 BEGIN SELECT RAISE(ABORT, 'test projection write failure'); END`)

	done := make(chan bool, 1)
	go func() { done <- signal.Handle(client, timerEvent(bobUser, aliceUser, pollCreationWire())) }()

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
		t.Fatal("failed projection queued acknowledgement")
	}

	execTimerSQL(t, database, "DROP TRIGGER fail_poll_projection")
	deliverPollWire(t, client, bobUser, pollCreationWire())

	state, err := client.PollProjection(t.Context(), durableRef())
	if err != nil || state.Creation == nil || state.Observations != 1 {
		t.Fatalf("retry = %+v, %v", state, err)
	}
}

func TestPollProjectionSendOnlyExcluded(t *testing.T) {
	t.Parallel()

	client := openOffline(t, seedAccount(t), signal.SendOnly())
	if signal.Handle(client, timerEvent(bobUser, aliceUser, pollCreationWire())) {
		t.Fatal("send-only ack")
	}

	state, err := client.PollProjection(t.Context(), durableRef())
	if err != nil || state.Creation != nil || state.Observations != 0 {
		t.Fatalf("send-only = %+v, %v", state, err)
	}
}

func TestPollProjectionBootstrapsRetainedInbox(t *testing.T) {
	t.Parallel()
	dir := seedAccount(t)
	client := openOffline(t, dir)
	database := timerSQL(t, dir)
	ref := durableRef()
	event := &signal.Message{
		Envelope: signal.Envelope{Sender: ref.Author, Chat: ref.Chat, Timestamp: ref.Timestamp},
		Poll:     &signal.Poll{Question: "Before upgrade", Options: []string{"a", "b"}},
	}

	encoded, err := signal.MarshalEvent(event, ref.Chat)
	if err != nil {
		t.Fatal(err)
	}
	// Insert the old codec directly: InboxAdd now persists projections itself.
	_, err = database.ExecContext(t.Context(), `INSERT INTO gosignal_inbox
 (received_at,time,chat,sender,timestamp,unread,event) VALUES (1,1,?,'',0,false,?)`, ref.Chat.Key(), string(encoded))
	if err != nil {
		t.Fatal(err)
	}
	// The first newly received vote must bootstrap creation before inbox pruning.
	deliverPollWire(t, client, aliceUser, counterWire(1))

	_, err = client.InboxPrune(t.Context(), time.Now().Add(time.Hour), 0)
	if err != nil {
		t.Fatal(err)
	}

	state, err := client.PollProjection(t.Context(), ref)
	if err != nil || state.Creation == nil || state.Observations != 2 || !slices.Equal(state.Tally, []int{1, 0}) {
		t.Fatalf("bootstrap = %+v, %v", state, err)
	}
}

func TestPollProjectionLateCreationControls(t *testing.T) {
	t.Parallel()
	client := openOffline(t, seedAccount(t))
	ref := durableRef()
	add := func(event signal.Event) {
		t.Helper()

		_, err := client.InboxAdd(t.Context(), signal.InboxEntry{Chat: ref.Chat, Event: event})
		if err != nil {
			t.Fatal(err)
		}
	}
	vote := func(timestamp uint64, count uint32, options ...uint32) *signal.PollVote {
		return &signal.PollVote{
			Envelope: signal.Envelope{Chat: ref.Chat, Sender: signal.Recipient{ACI: aliceUser}, Timestamp: timestamp},
			OutgoingPollVote: signal.OutgoingPollVote{
				TargetAuthor: ref.Author, TargetTimestamp: ref.Timestamp,
				VoteCount: count, OptionIndexes: options,
			},
		}
	}
	add(vote(100, 1, 0))
	add(vote(101, 2, 9))
	add(&signal.PollClose{
		Envelope:          signal.Envelope{Chat: ref.Chat, Sender: ref.Author, Timestamp: 102},
		OutgoingPollClose: signal.OutgoingPollClose{TargetTimestamp: ref.Timestamp},
	})
	add(vote(103, 3, 1))
	add(&signal.Message{
		Envelope: signal.Envelope{Chat: ref.Chat, Sender: ref.Author, Timestamp: ref.Timestamp},
		Poll:     &signal.Poll{Question: "Q", Options: []string{"a", "b"}},
	})

	state, err := client.PollProjection(t.Context(), ref)
	if err != nil || !state.ClosureObserved || state.ClosedAt != 102 || state.IgnoredInvalid != 1 ||
		!slices.Equal(state.Tally, []int{1, 0}) {
		t.Fatalf("late creation/control = %+v, %v", state, err)
	}

	add(&signal.Delete{
		Envelope:        signal.Envelope{Chat: ref.Chat, Sender: ref.Author, Timestamp: 104},
		TargetTimestamp: ref.Timestamp,
	})

	state, err = client.PollProjection(t.Context(), ref)
	if err != nil || !state.Deleted || state.Tally != nil {
		t.Fatalf("delete = %+v, %v", state, err)
	}
}

func TestPollProjectionKeysAndMetadata(t *testing.T) {
	t.Parallel()
	client := openOffline(t, seedAccount(t))
	ref := durableRef()
	ref.Timestamp = ^uint64(0)

	creation := &signal.Message{
		Envelope: signal.Envelope{Chat: ref.Chat, Sender: ref.Author, Timestamp: ref.Timestamp},
		Poll:     &signal.Poll{Question: "Q", Options: []string{"a", "b"}},
	}
	for range 2 {
		_, err := client.InboxAdd(t.Context(), signal.InboxEntry{Chat: ref.Chat, Event: creation})
		if err != nil {
			t.Fatal(err)
		}

		creation.Sender.Number = "+15550123456"
		creation.Chat.Recipient.Number = "+15550987654"
		creation.ServerTimestamp = 999
	}

	state, err := client.PollProjection(t.Context(), ref)
	if err != nil || state.Creation == nil || state.Observations != 1 {
		t.Fatalf("dedup = %+v, %v", state, err)
	}

	for _, other := range []signal.PollReference{
		{Chat: ref.Chat, Author: ref.Author, Timestamp: ref.Timestamp - 1},
		{Chat: ref.Chat, Author: signal.Recipient{ACI: aliceUser}, Timestamp: ref.Timestamp},
		{Chat: signal.Chat{Recipient: signal.Recipient{ACI: bobUser}}, Author: ref.Author, Timestamp: ref.Timestamp},
	} {
		state, err = client.PollProjection(t.Context(), other)
		if err != nil || state.Creation != nil || state.Observations != 0 {
			t.Fatalf("isolation = %+v, %v", state, err)
		}
	}
}

func TestPollProjectionInvalidUTF8Evidence(t *testing.T) {
	t.Parallel()
	client := openOffline(t, seedAccount(t))

	ref := durableRef()
	for _, question := range []string{"\xff", "\xfe"} {
		_, err := client.InboxAdd(t.Context(), signal.InboxEntry{
			Chat: ref.Chat,
			Event: &signal.Message{
				Envelope: signal.Envelope{Chat: ref.Chat, Sender: ref.Author, Timestamp: ref.Timestamp},
				Poll:     &signal.Poll{Question: question, Options: []string{"a", "b"}},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	state, err := client.PollProjection(t.Context(), ref)
	if err != nil || state.Creation != nil || state.Observations != 2 || state.IgnoredInvalid != 2 {
		t.Fatalf("invalid UTF-8 through codec = %+v, %v", state, err)
	}
}
