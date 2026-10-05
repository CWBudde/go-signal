package app_test

import (
	"errors"
	"testing"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/signal"
)

//nolint:cyclop // One timeline checks selections, withdrawals, overrides and failed-send reservations.
func TestPollVoteAutomaticCounter(t *testing.T) {
	t.Parallel()

	fake := directory()
	a := sender(t, fake)

	req := app.PollVoteRequest{Recipient: aliceNumber, Target: "self:43", OptionIndexes: []uint32{0}}
	for _, step := range []struct {
		explicit, want uint32
		clear          bool
	}{
		{0, 1, false}, {0, 2, true}, {12, 12, false}, {2, 2, false}, {0, 13, false},
	} {
		req.VoteCount, req.Clear = step.explicit, step.clear

		req.OptionIndexes = nil
		if !step.clear {
			req.OptionIndexes = []uint32{0}
		}

		got, err := a.PollVote(t.Context(), req)
		if err != nil || got.VoteCount != step.want {
			t.Fatalf("vote count = %d, %v; want %d", got.VoteCount, err, step.want)
		}

		sent := fake.Sent()
		if sent[len(sent)-1].PollVote.VoteCount != step.want {
			t.Fatal("wire counter differs from result")
		}
	}

	fake.SendFailures = map[string]error{aliceACI: signal.ErrUntrustedIdentity}
	req.VoteCount = 0

	got, err := a.PollVote(t.Context(), req)
	if !errors.Is(err, app.ErrSendFailed) || got.VoteCount != 14 {
		t.Fatalf("failure counter = %d, %v", got.VoteCount, err)
	}

	fake.SendFailures = nil

	got, err = a.PollVote(t.Context(), req)
	if err != nil || got.VoteCount != 15 {
		t.Fatalf("after failure counter = %d, %v", got.VoteCount, err)
	}

	req.Recipient = bobUsername

	got, err = a.PollVote(t.Context(), req)
	if err != nil || got.VoteCount != 1 {
		t.Fatalf("isolated chat counter = %d, %v", got.VoteCount, err)
	}
}

func TestPollVoteCounterPolicyAndPersistenceFailure(t *testing.T) {
	t.Parallel()

	fake := directory()

	client, err := fake.Factory(t.Context(), signal.Options{})
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = client.Close() })

	allow, err := app.ParseAllowlist([]string{bobACI})
	if err != nil {
		t.Fatal(err)
	}

	req := app.PollVoteRequest{Recipient: aliceNumber, Target: "self:43", OptionIndexes: []uint32{0}}
	fake.PollCounterErr = signal.ErrPollVoteExhausted

	_, err = app.New(client, app.WithAllowlist(allow)).PollVote(t.Context(), req)
	if !errors.Is(err, app.ErrRecipientNotAllowed) {
		t.Fatalf("policy must precede reservation: %v", err)
	}

	fake.PollCounterErr = nil
	a := app.New(client)

	got, err := a.PollVote(t.Context(), req)
	if err != nil || got.VoteCount != 1 {
		t.Fatalf("denied request consumed counter: %d, %v", got.VoteCount, err)
	}

	fake.PollCounterErr = signal.ErrPollVoteExhausted

	_, err = a.PollVote(t.Context(), req)
	if !errors.Is(err, signal.ErrPollVoteExhausted) || len(fake.Sent()) != 1 {
		t.Fatalf("persistence failure sent: %v", err)
	}

	fake.PollCounterErr = nil

	got, err = a.PollVote(t.Context(), req)
	if err != nil || got.VoteCount != 2 {
		t.Fatalf("failed reservation consumed counter: %d, %v", got.VoteCount, err)
	}
}

func TestPollVoteCounterSelectedAccount(t *testing.T) {
	t.Parallel()

	fake := directory()
	first := testAccount()
	second := first
	second.ACI = "abcdefab-cdef-4abc-8def-abcdefabcdef"
	second.Number = "+15550123456"

	fake.Linked = []signal.Account{first, second}
	for _, step := range []struct {
		account string
		want    uint32
	}{{first.ACI, 1}, {second.ACI, 1}, {first.ACI, 2}} {
		client, err := fake.Factory(t.Context(), signal.Options{Account: step.account})
		if err != nil {
			t.Fatal(err)
		}

		got, err := app.New(client).PollVote(t.Context(), app.PollVoteRequest{
			Recipient: aliceNumber, Target: bobACI + ":42", Clear: true,
		})

		closeErr := client.Close()
		if err != nil || closeErr != nil || got.VoteCount != step.want {
			t.Fatalf("selected counter = %d, %v, %v", got.VoteCount, err, closeErr)
		}
	}
}
