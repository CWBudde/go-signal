//nolint:lll // complete payload fixtures and discriminating assertions
package signal_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/cwbudde/go-signal/internal/signal"
)

func TestPollPayloadChecks(t *testing.T) {
	t.Parallel()

	valid := signal.Poll{Question: " question ", Options: []string{"a", "b"}, AllowMultiple: true}

	err := valid.Check()
	if err != nil {
		t.Fatal(err)
	}

	for _, p := range []signal.Poll{
		{Question: " ", Options: []string{"a", "b"}},
		{Question: "q", Options: []string{"a"}},
		{Question: strings.Repeat("😀", 51), Options: []string{"a", "b"}},
		{Question: "q", Options: []string{"a", "\xff"}},
		{Question: "q", Options: []string{"a", strings.Repeat("😀", 51)}},
	} {
		if !errors.Is(p.Check(), signal.ErrInvalidPoll) {
			t.Errorf("accepted %+v", p)
		}
	}

	vote := signal.OutgoingPollVote{TargetAuthor: signal.Recipient{ACI: "11111111-1111-4111-8111-111111111111"}, TargetTimestamp: 1, VoteCount: 1}

	err = vote.Check()
	if err != nil {
		t.Fatal(err)
	}

	for _, change := range []func(*signal.OutgoingPollVote){
		func(v *signal.OutgoingPollVote) { v.TargetAuthor.ACI = "invalid-poll-identifier" },
		func(v *signal.OutgoingPollVote) { v.TargetTimestamp = 0 },
		func(v *signal.OutgoingPollVote) { v.VoteCount = 0 },
		func(v *signal.OutgoingPollVote) { v.OptionIndexes = []uint32{1, 1} },
		func(v *signal.OutgoingPollVote) { v.OptionIndexes = []uint32{10} },
	} {
		v := vote
		change(&v)

		if !errors.Is(v.Check(), signal.ErrInvalidPoll) {
			t.Errorf("accepted %+v", v)
		}
	}

	if !errors.Is((signal.OutgoingPollClose{}).Check(), signal.ErrInvalidPoll) {
		t.Fatal("accepted missing close target")
	}
}

func TestPollSendAndCodec(t *testing.T) {
	t.Parallel()

	p := &signal.Poll{Question: "q", Options: []string{"a", "b"}}
	for _, req := range []signal.SendRequest{
		{Recipients: []signal.Recipient{{ACI: "a"}}, PollCreate: p},
		{GroupID: "invalid-poll-identifier", PollCreate: p},
		{GroupID: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=", Body: "mixed", PollCreate: p},
		{GroupID: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=", PollCreate: p, PollClose: &signal.OutgoingPollClose{TargetTimestamp: 1}},
	} {
		if req.Check() == nil {
			t.Errorf("accepted %+v", req)
		}
	}

	for _, evt := range []signal.Event{&signal.Message{Poll: p}, &signal.PollVote{OutgoingPollVote: signal.OutgoingPollVote{TargetTimestamp: 42, VoteCount: 2, OptionIndexes: []uint32{1}}}, &signal.PollClose{OutgoingPollClose: signal.OutgoingPollClose{TargetTimestamp: 42}}} {
		data, err := signal.MarshalEvent(evt, signal.Chat{})
		if err != nil {
			t.Fatal(err)
		}

		got, _ := signal.UnmarshalEvent(data)

		data2, err := signal.MarshalEvent(got, signal.Chat{})
		if err != nil || string(data) != string(data2) {
			t.Fatalf("roundtrip %T: %s -> %s, %v", evt, data, data2, err)
		}
	}
}
