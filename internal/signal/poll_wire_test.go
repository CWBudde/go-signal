//go:build cgo || libsignal_go

//nolint:cyclop,lll,goconst // complete payload fixtures and discriminating assertions
package signal_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow/protobuf/signalpb"
	"github.com/google/uuid"
)

func TestPollWire(t *testing.T) {
	t.Parallel()

	senderID := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	poll := &signal.Poll{Question: " q ", Options: []string{"a", "b"}, AllowMultiple: true}

	create, err := signal.DataMessage(signal.SendRequest{Timestamp: 42, PollCreate: poll}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	if create.GetRequiredProtocolVersion() != 8 || create.GetPollCreate() == nil || create.GetPollCreate().GetQuestion() != poll.Question || !create.GetPollCreate().GetAllowMultiple() || !reflect.DeepEqual(create.GetPollCreate().GetOptions(), poll.Options) {
		t.Fatalf("creation = %v", create)
	}

	poll.Options[0] = "changed"

	if create.GetPollCreate().GetOptions()[0] != "a" {
		t.Fatal("creation aliases request")
	}

	indexes := []uint32{1, 0}

	vote, err := signal.DataMessage(signal.SendRequest{PollVote: &signal.OutgoingPollVote{TargetAuthor: signal.Recipient{ACI: senderID.String()}, TargetTimestamp: 42, VoteCount: 3, OptionIndexes: indexes}}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	if vote.GetPollVote() == nil || !reflect.DeepEqual(vote.GetPollVote().GetTargetAuthorAciBinary(), senderID[:]) || vote.GetPollVote().GetVoteCount() != 3 || vote.GetPollVote().GetTargetSentTimestamp() != 42 || vote.RequiredProtocolVersion != nil {
		t.Fatalf("vote = %v", vote)
	}

	indexes[0] = 8

	if vote.GetPollVote().GetOptionIndexes()[0] != 1 {
		t.Fatal("vote aliases request")
	}

	closeMsg, err := signal.DataMessage(signal.SendRequest{PollClose: &signal.OutgoingPollClose{TargetTimestamp: 42}}, nil, nil)
	if err != nil || closeMsg.GetPollTerminate().GetTargetSentTimestamp() != 42 || closeMsg.RequiredProtocolVersion != nil {
		t.Fatalf("close = %v, %v", closeMsg, err)
	}
}

func TestPollConversion(t *testing.T) {
	t.Parallel()

	senderID := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	for _, chat := range []string{senderID.String(), "poll-group"} {
		create := &signalpb.DataMessage{Timestamp: new(uint64(42)), PollCreate: &signalpb.DataMessage_PollCreate{Question: new(strings.Repeat("q", 200)), Options: []string{"a", "b"}}}
		evt := signal.ConvertEvent(chatEvent(senderID, chat, create), senderID.String())

		msg, ok := evt.(*signal.Message)
		if !ok || msg.Poll == nil || !msg.Sync {
			t.Fatalf("creation = %#v", evt)
		}

		create.PollCreate.Options[0] = "changed"
		if msg.Poll.Options[0] != "a" {
			t.Fatal("incoming options alias wire")
		}

		vote := &signalpb.DataMessage{Timestamp: new(uint64(43)), PollVote: &signalpb.DataMessage_PollVote{TargetAuthorAciBinary: senderID[:], TargetSentTimestamp: new(uint64(42)), VoteCount: new(uint32(0)), OptionIndexes: []uint32{1}}}
		evt = signal.ConvertEvent(chatEvent(senderID, chat, vote), "")

		v, ok := evt.(*signal.PollVote)
		if !ok || v.TargetAuthor.ACI != senderID.String() || v.VoteCount != 0 {
			t.Fatalf("vote = %#v", evt)
		}

		evt = signal.ConvertEvent(chatEvent(senderID, chat, &signalpb.DataMessage{Timestamp: new(uint64(44)), PollTerminate: &signalpb.DataMessage_PollTerminate{TargetSentTimestamp: new(uint64(42))}}), "")
		if _, ok = evt.(*signal.PollClose); !ok {
			t.Fatalf("close = %#v", evt)
		}
	}
}

func TestMalformedPollConversion(t *testing.T) {
	t.Parallel()

	senderID := uuid.MustParse("11111111-1111-4111-8111-111111111111")

	valid := func() *signalpb.DataMessage {
		return &signalpb.DataMessage{Timestamp: new(uint64(42)), PollCreate: &signalpb.DataMessage_PollCreate{Question: new("q"), Options: []string{"a", "b"}}}
	}
	for _, mutate := range []func(*signalpb.DataMessage){
		func(m *signalpb.DataMessage) { m.Body = new("mixed") },
		func(m *signalpb.DataMessage) { m.Contact = []*signalpb.DataMessage_Contact{{}} },
		func(m *signalpb.DataMessage) { m.Reaction = &signalpb.DataMessage_Reaction{} },
		func(m *signalpb.DataMessage) { m.Delete = &signalpb.DataMessage_Delete{} },
		func(m *signalpb.DataMessage) { m.PollVote = &signalpb.DataMessage_PollVote{} },
		func(m *signalpb.DataMessage) { m.PollCreate.Question = new(strings.Repeat("😀", 101)) },
		func(m *signalpb.DataMessage) {
			m.PollCreate = nil
			m.PollVote = &signalpb.DataMessage_PollVote{TargetAuthorAciBinary: []byte{1}, TargetSentTimestamp: new(uint64(42))}
		},
		func(m *signalpb.DataMessage) {
			m.PollCreate = nil
			m.PollTerminate = &signalpb.DataMessage_PollTerminate{}
		},
	} {
		m := valid()
		mutate(m)
		evt := signal.ConvertEvent(chatEvent(senderID, senderID.String(), m), "")

		u, ok := evt.(*signal.Unsupported)
		if !ok || u.Type != "invalidPoll" {
			t.Fatalf("malformed = %#v", evt)
		}
	}
}

func TestPollInboxSurvivesReopen(t *testing.T) {
	t.Parallel()
	dataDir := seedAccount(t)
	client := openInboxClient(t, dataDir)
	env := signal.Envelope{Sender: signal.Recipient{ACI: "11111111-1111-4111-8111-111111111111"}, Chat: signal.Chat{GroupID: "poll-group"}, Timestamp: 42}

	entries := make([]signal.InboxEntry, 0, 3)

	for _, evt := range []signal.Event{
		&signal.Message{Envelope: env, Poll: &signal.Poll{Question: "q", Options: []string{"a", "b"}}},
		&signal.PollVote{Envelope: env, OutgoingPollVote: signal.OutgoingPollVote{TargetAuthor: env.Sender, TargetTimestamp: 42, OptionIndexes: []uint32{1}, VoteCount: 2}},
		&signal.PollClose{Envelope: env, OutgoingPollClose: signal.OutgoingPollClose{TargetTimestamp: 42}},
	} {
		entry, err := client.InboxAdd(t.Context(), signal.InboxEntry{Chat: env.Chat, Event: evt})
		if err != nil {
			t.Fatal(err)
		}

		entries = append(entries, entry)
	}

	err := client.Close()
	if err != nil {
		t.Fatal(err)
	}

	client = openInboxClient(t, dataDir)
	assertInboxEntries(t, client, signal.InboxQuery{}, entries...)
}

func TestPollScalarsAndEdit(t *testing.T) {
	t.Parallel()

	sender := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	vote := func() *signalpb.DataMessage {
		return &signalpb.DataMessage{Timestamp: new(uint64(44)), PollVote: &signalpb.DataMessage_PollVote{TargetAuthorAciBinary: sender[:], TargetSentTimestamp: new(uint64(42))}}
	}
	evt := signal.ConvertEvent(chatEvent(sender, sender.String(), vote()), "")

	converted, ok := evt.(*signal.PollVote)
	if !ok || converted.VoteCount != 0 {
		t.Fatalf("missing count = %#v", evt)
	}

	for _, mutate := range []func(*signalpb.DataMessage){
		func(m *signalpb.DataMessage) { m.Timestamp = nil },
		func(m *signalpb.DataMessage) { m.Timestamp = new(uint64(0)) },
		func(m *signalpb.DataMessage) { m.PollVote.TargetSentTimestamp = nil },
		func(m *signalpb.DataMessage) { m.PollVote.TargetSentTimestamp = new(uint64(0)) },
		func(m *signalpb.DataMessage) { m.PollVote.TargetAuthorAciBinary = nil },
		func(m *signalpb.DataMessage) { m.PollVote.TargetAuthorAciBinary = make([]byte, 16) },
		func(m *signalpb.DataMessage) { m.PollVote.OptionIndexes = []uint32{0, 0} },
		func(m *signalpb.DataMessage) { m.PollVote.OptionIndexes = []uint32{10} },
		func(m *signalpb.DataMessage) { m.Attachments = []*signalpb.AttachmentPointer{{}} },
		func(m *signalpb.DataMessage) { m.Sticker = &signalpb.DataMessage_Sticker{} },
		func(m *signalpb.DataMessage) { m.Quote = &signalpb.DataMessage_Quote{} },
		func(m *signalpb.DataMessage) { m.BodyRanges = []*signalpb.BodyRange{{}} },
	} {
		msg := vote()
		mutate(msg)
		evt = signal.ConvertEvent(chatEvent(sender, sender.String(), msg), "")

		unsupported, ok := evt.(*signal.Unsupported)
		if !ok || unsupported.Type != "invalidPoll" {
			t.Fatalf("malformed = %#v", evt)
		}
	}

	evt = signal.ConvertEvent(chatEvent(sender, sender.String(), &signalpb.EditMessage{DataMessage: vote()}), "")

	unsupported, ok := evt.(*signal.Unsupported)
	if !ok || unsupported.Type != "invalidPoll" || unsupported.Timestamp != 44 {
		t.Fatalf("poll edit = %#v", evt)
	}
}

func TestPollMixedMetadata(t *testing.T) {
	t.Parallel()

	sender := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	valid := func() *signalpb.DataMessage {
		return &signalpb.DataMessage{
			Timestamp:  new(uint64(42)),
			PollCreate: &signalpb.DataMessage_PollCreate{Question: new("q"), Options: []string{"a", "b"}},
			GroupV2:    &signalpb.GroupContextV2{}, ProfileKey: []byte{1, 2}, ExpireTimer: new(uint32(60)),
		}
	}

	evt := signal.ConvertEvent(chatEvent(sender, "poll-metadata-group", valid()), "")
	if _, ok := evt.(*signal.Message); !ok {
		t.Fatalf("legitimate metadata = %#v", evt)
	}

	for name, mutate := range map[string]func(*signalpb.DataMessage){
		"preview":        func(msg *signalpb.DataMessage) { msg.Preview = []*signalpb.Preview{{}} },
		"view once":      func(msg *signalpb.DataMessage) { msg.IsViewOnce = new(true) },
		"end session":    func(msg *signalpb.DataMessage) { msg.Flags = new(uint32(1)) },
		"timer update":   func(msg *signalpb.DataMessage) { msg.Flags = new(uint32(signalpb.DataMessage_EXPIRATION_TIMER_UPDATE)) },
		"profile update": func(msg *signalpb.DataMessage) { msg.Flags = new(uint32(signalpb.DataMessage_PROFILE_KEY_UPDATE)) },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			msg := valid()
			mutate(msg)
			evt := signal.ConvertEvent(chatEvent(sender, "poll-metadata-group", msg), "")

			unsupported, ok := evt.(*signal.Unsupported)
			if !ok || unsupported.Type != "invalidPoll" {
				t.Fatalf("mixed metadata = %#v", evt)
			}
		})
	}
}
