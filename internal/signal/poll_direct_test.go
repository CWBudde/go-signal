//nolint:lll // Complete wire fixtures and independent result assertions.
package signal_test

import (
	"testing"

	"github.com/cwbudde/go-signal/internal/signal"
)

func TestDirectPollChecks(t *testing.T) {
	t.Parallel()

	recipient := signal.Recipient{ACI: selfACI}
	for _, payload := range []signal.SendRequest{
		{PollCreate: &signal.Poll{Question: "Q", Options: []string{"a", "b"}}},
		{PollVote: &signal.OutgoingPollVote{TargetAuthor: recipient, TargetTimestamp: 42, VoteCount: 1, OptionIndexes: []uint32{0}}},
		{PollVote: &signal.OutgoingPollVote{TargetAuthor: recipient, TargetTimestamp: 42, VoteCount: 2}},
		{PollClose: &signal.OutgoingPollClose{TargetTimestamp: 42}},
	} {
		payload.Recipients = []signal.Recipient{recipient}

		err := payload.Check()
		if err != nil {
			t.Fatalf("direct payload rejected: %v", err)
		}

		for _, recipients := range [][]signal.Recipient{
			{recipient, recipient},
			{{Number: "+15550101"}},
			{{ACI: "invalid-direct-poll-aci"}},
			{{ACI: nilACITestValue}},
		} {
			payload.Recipients = recipients
			if payload.Check() == nil {
				t.Fatalf("accepted invalid direct recipients: %+v", recipients)
			}
		}
	}
}
