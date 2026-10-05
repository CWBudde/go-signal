//nolint:lll // Complete wire fixtures and independent result assertions.
package app_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/signal"
)

//nolint:cyclop,gocognit // One end-to-end timeline exercises four operations for each recipient form.
func TestDirectPollOperations(t *testing.T) {
	t.Parallel()

	for _, recipient := range []struct{ arg, aci string }{
		{aliceNumber, aliceACI},
		{bobUsername, bobACI},
		{aliceACI, aliceACI},
		{app.SelfRecipient, testAccount().ACI},
	} {
		t.Run(recipient.arg, func(t *testing.T) {
			t.Parallel()

			fake := directory()
			a := sender(t, fake)

			created, err := a.PollCreate(t.Context(), app.PollCreateRequest{
				Recipient: recipient.arg, Question: "Q", Options: []string{"a", "b"},
			})
			if err != nil {
				t.Fatal(err)
			}

			if created.TargetAuthor.ACI != testAccount().ACI || created.TargetTimestamp != sentAt {
				t.Fatalf("creation %+v", created)
			}

			_, err = a.PollVote(t.Context(), app.PollVoteRequest{
				Recipient: recipient.arg, Target: "self:42", VoteCount: 1, OptionIndexes: []uint32{0},
			})
			if err != nil {
				t.Fatal(err)
			}

			_, err = a.PollVote(t.Context(), app.PollVoteRequest{
				Recipient: recipient.arg, Target: "self:42", VoteCount: 2, Clear: true,
			})
			if err != nil {
				t.Fatal(err)
			}

			_, err = a.PollClose(t.Context(), app.PollCloseRequest{Recipient: recipient.arg, Target: 42})
			if err != nil {
				t.Fatal(err)
			}

			sent := fake.Sent()
			if len(sent) != 4 {
				t.Fatalf("sends %+v", sent)
			}

			for _, req := range sent {
				if req.GroupID != "" || len(req.Recipients) != 1 || req.Recipients[0].ACI != recipient.aci {
					t.Fatalf("destination %+v", req)
				}
			}

			if sent[1].PollVote.TargetAuthor.ACI != testAccount().ACI || sent[2].PollVote.VoteCount != 2 || len(sent[2].PollVote.OptionIndexes) != 0 {
				t.Fatalf("votes %+v", sent)
			}
		})
	}
}

func TestDirectPollPreflight(t *testing.T) {
	t.Parallel()

	for _, recipient := range []string{"", "invalid-direct-poll-recipient", "00000000-0000-0000-0000-000000000000", app.GroupPrefix + groupID} {
		for _, group := range []string{"", groupID} {
			if recipient == "" && group != "" {
				continue
			}

			fake := directory()
			a := sender(t, fake)

			_, err := a.PollCreate(t.Context(), app.PollCreateRequest{
				GroupID: group, Recipient: recipient, Question: "Q", Options: []string{"a", "b"},
			})
			if err == nil || len(fake.Connects()) != 0 || len(fake.Sent()) != 0 {
				t.Fatalf("invalid destination connected/sent: %q %q %v", group, recipient, err)
			}
		}
	}
}

func TestDirectPollShowIsolation(t *testing.T) {
	t.Parallel()

	fake := directory()
	a := sender(t, fake)
	chat := signal.Chat{Recipient: signal.Recipient{ACI: bobACI, Number: aliceNumber}}
	creation := incoming(aliceUser(), chat, 100, "")
	creation.Poll = &signal.Poll{Question: "Q", Options: []string{"a", "b"}}
	vote := pollVote(bobACI, 101, 1, 1)
	vote.Chat = chat
	otherChat := pollVote(bobACI, 102, 2, 0)
	runInbox(t, a, app.InboxOptions{}, creation, vote, otherChat)

	connects := len(fake.Connects())

	state, err := a.PollShow(t.Context(), app.PollShowRequest{Recipient: bobACI, Target: aliceACI + ":100"})
	if err != nil {
		t.Fatal(err)
	}

	if state.Chat.Key() != chat.Key() || state.Scanned != 2 || !reflect.DeepEqual(state.Tally, []int{0, 1}) || state.Completeness != pollCompletenessUnknown || len(fake.Connects()) != connects {
		t.Fatalf("state %+v", state)
	}

	for _, recipient := range []string{app.SelfRecipient, aliceNumber, bobUsername, "AAAAAAAA-AAAA-4AAA-8AAA-AAAAAAAAAAAA"} {
		if (app.PollShowRequest{Recipient: recipient, Target: aliceACI + ":100"}).Check() == nil {
			t.Fatalf("noncanonical offline chat accepted: %q", recipient)
		}
	}
}

func TestDirectPollAllowlist(t *testing.T) {
	t.Parallel()

	fake := directory()
	a := sender(t, fake)

	list, err := app.ParseAllowlist([]string{aliceACI})
	if err != nil {
		t.Fatal(err)
	}

	app.WithAllowlist(list)(a)

	_, err = a.PollVote(t.Context(), app.PollVoteRequest{Recipient: bobACI, Target: "+15550000:42", VoteCount: 1, OptionIndexes: []uint32{0}})
	if !errors.Is(err, app.ErrRecipientNotAllowed) || len(fake.Sent()) != 0 || len(fake.Uploaded()) != 0 {
		t.Fatalf("policy %v", err)
	}
}
