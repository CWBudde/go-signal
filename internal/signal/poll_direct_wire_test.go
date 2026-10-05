//go:build cgo || libsignal_go

//nolint:lll // Complete wire fixtures and independent result assertions.
package signal_test

import (
	"context"
	"testing"

	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/mautrix-signal/pkg/libsignalgo"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow/protobuf/signalpb"
)

//nolint:cyclop // Table verifies each wire operation for a peer and our own account.
func TestDirectPollWire(t *testing.T) {
	t.Parallel()
	dataDir := seedAccount(t)
	client := openOffline(t, dataDir)
	mergeTimer(t, dataDir, aliceUser, 30, 2)

	for _, recipient := range []string{aliceUser, seededACI} {
		for _, payload := range []signal.SendRequest{
			{PollCreate: &signal.Poll{Question: "Q", Options: []string{"a", "b"}}},
			{PollVote: &signal.OutgoingPollVote{TargetAuthor: signal.Recipient{ACI: seededACI}, TargetTimestamp: 42, VoteCount: 1, OptionIndexes: []uint32{0}}},
			{PollClose: &signal.OutgoingPollClose{TargetTimestamp: 42}},
		} {
			payload.Recipients = []signal.Recipient{{ACI: recipient}}
			payload.Timestamp = 100

			fresh, err := signal.BuildMessage(t.Context(), client, payload)
			if err != nil {
				t.Fatal(err)
			}

			calls := 0

			result := signal.SendDirectRecipients(t.Context(), client, payload, fresh, func(_ context.Context, id libsignalgo.ServiceID, content *signalpb.Content) signalmeow.SendMessageResult {
				calls++

				msg := content.GetDataMessage()
				if id.String() != recipient || msg == nil || msg.GetGroupV2() != nil || msg.GetTimestamp() != 100 {
					t.Fatalf("direct wire %v %v", id, content)
				}

				if recipient == aliceUser {
					checkWireTimer(t, msg, 30, 2)
				}

				checkDirectPollWire(t, payload, msg)

				return signalmeow.SendMessageResult{WasSuccessful: true}
			})
			if calls != 1 || len(result.Results) != 1 || result.Results[0].Err != nil || result.Timestamp != 100 {
				t.Fatalf("delivery %+v", result)
			}
		}
	}
}

//nolint:cyclop // Each control is checked independently against literal protocol values.
func checkDirectPollWire(t *testing.T, req signal.SendRequest, msg *signalpb.DataMessage) {
	t.Helper()

	switch {
	case req.PollCreate != nil:
		if msg.GetRequiredProtocolVersion() != 8 || msg.GetPollCreate().GetQuestion() != "Q" || len(msg.GetPollCreate().GetOptions()) != 2 {
			t.Fatalf("creation %v", msg)
		}
	case req.PollVote != nil:
		vote := msg.GetPollVote()
		if vote.GetTargetSentTimestamp() != 42 || vote.GetVoteCount() != 1 || len(vote.GetOptionIndexes()) != 1 || vote.GetOptionIndexes()[0] != 0 {
			t.Fatalf("vote %v", msg)
		}
	case req.PollClose != nil:
		if msg.GetPollTerminate().GetTargetSentTimestamp() != 42 {
			t.Fatalf("close %v", msg)
		}
	}
}
