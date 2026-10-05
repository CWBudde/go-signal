package app_test

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/signal"
)

//nolint:cyclop // Persistence, deduplication and bounded view independence assertions.
func TestDurablePollShowSurvivesPruning(t *testing.T) {
	t.Parallel()

	fake := directory()
	a := sender(t, fake)
	creation := incoming(aliceUser(), groupChat(), 100, "")
	creation.Poll = &signal.Poll{Question: "Q", Options: []string{"a", "b"}}
	// Latest invalid vote must not displace an earlier valid vote once creation arrives.
	vote := pollVote(bobACI, 101, 1, 0)
	duplicate := pollVote(bobACI, 101, 1, 0)
	duplicate.Sender.Number = "+15550001"
	runInbox(t, a, app.InboxOptions{}, vote, duplicate, pollVote(bobACI, 102, 2, 9), creation)

	client, err := fake.Factory(t.Context(), signal.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	_, err = client.InboxPrune(t.Context(), time.Now().Add(time.Hour), 0)
	if err != nil {
		t.Fatal(err)
	}

	state, err := a.PollShow(t.Context(), app.PollShowRequest{GroupID: groupID, Target: aliceACI + ":100", Durable: true})
	if err != nil || state.Creation == nil || !slices.Equal(state.Tally, []int{1, 0}) ||
		state.Observations != 3 || state.IgnoredInvalid != 1 {
		t.Fatalf("durable = %+v, %v", state, err)
	}

	if state.Source != "durable" || state.Scanned != 0 || state.Truncated ||
		state.Completeness != pollCompletenessUnknown {
		t.Fatalf("durable metadata = %+v", state)
	}

	snapshot, err := a.PollShow(t.Context(), app.PollShowRequest{GroupID: groupID, Target: aliceACI + ":100"})
	if err != nil || snapshot.Creation != nil {
		t.Fatalf("pruned snapshot = %+v, %v", snapshot, err)
	}
}

func TestDurablePollSelectedAccount(t *testing.T) {
	t.Parallel()

	fake := directory()
	first := testAccount()
	second := first
	second.ACI = "abcdefab-cdef-4abc-8def-abcdefabcdef"
	second.Number = "+15550123456"
	fake.Linked = []signal.Account{first, second}
	ref := signal.PollReference{Chat: groupChat(), Author: aliceUser(), Timestamp: 100}

	for _, step := range []struct {
		account string
		known   bool
	}{{first.ACI, true}, {second.ACI, false}, {first.ACI, true}} {
		client, err := fake.Factory(t.Context(), signal.Options{Account: step.account})
		if err != nil {
			t.Fatal(err)
		}

		if step.account == first.ACI && len(fake.Inbox()) == 0 {
			creation := incoming(aliceUser(), groupChat(), 100, "")
			creation.Poll = &signal.Poll{Question: "Q", Options: []string{"a", "b"}}

			_, err = client.InboxAdd(t.Context(), signal.InboxEntry{Chat: groupChat(), Event: creation})
			if err != nil {
				t.Fatal(err)
			}
		}

		state, err := client.PollProjection(t.Context(), ref)

		closeErr := client.Close()
		if err != nil || closeErr != nil || (state.Creation != nil) != step.known {
			t.Fatalf("selected account %s = %+v, %v, %v", step.account, state, err, closeErr)
		}
	}
}

// Removing the event kind from deduplication can conflate a close and delete at the same timestamp.
func TestDurablePollCloseAndDeleteSameTimestamp(t *testing.T) {
	t.Parallel()

	fake := directory()
	a := sender(t, fake)
	creation := incoming(aliceUser(), groupChat(), 100, "")
	creation.Poll = &signal.Poll{Question: "Q", Options: []string{"a", "b"}}
	env := signal.Envelope{Sender: aliceUser(), Chat: groupChat(), Timestamp: 101}
	runInbox(t, a, app.InboxOptions{}, creation,
		&signal.PollClose{Envelope: env, OutgoingPollClose: signal.OutgoingPollClose{TargetTimestamp: 100}},
		&signal.Delete{Envelope: env, TargetTimestamp: 100})

	state, err := a.PollShow(t.Context(), app.PollShowRequest{GroupID: groupID, Target: aliceACI + ":100", Durable: true})
	if err != nil || !state.Deleted || state.Tally != nil || state.Observations != 3 {
		t.Fatalf("close/delete collision = %+v, %v", state, err)
	}
}

func TestDurablePollRemovedAccountIsEmpty(t *testing.T) {
	t.Parallel()

	fake := directory()

	client, err := fake.Factory(t.Context(), signal.Options{})
	if err != nil {
		t.Fatal(err)
	}

	ref := signal.PollReference{Chat: groupChat(), Author: aliceUser(), Timestamp: 100}
	creation := incoming(aliceUser(), groupChat(), 100, "")
	creation.Poll = &signal.Poll{Question: "Q", Options: []string{"a", "b"}}

	_, err = client.InboxAdd(t.Context(), signal.InboxEntry{Chat: groupChat(), Event: creation})
	if err != nil {
		t.Fatal(err)
	}

	account, err := client.Unlink(t.Context(), signal.UnlinkOptions{LocalOnly: true})
	if err != nil {
		t.Fatal(err)
	}

	err = client.Close()
	if err != nil {
		t.Fatal(err)
	}

	fake.Linked = []signal.Account{account}

	client, err = fake.Factory(t.Context(), signal.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	state, err := client.PollProjection(t.Context(), ref)
	if err != nil || state.Creation != nil || state.Observations != 0 {
		t.Fatalf("removed/relinked = %+v, %v", state, err)
	}
}

func TestDurablePollDistinctInvalidCreations(t *testing.T) {
	t.Parallel()

	fake := directory()
	a := sender(t, fake)

	questions := []string{strings.Repeat("a", 201), strings.Repeat("b", 201), "\xff", "\xfe"}
	events := make([]signal.Event, 0, len(questions))

	for _, question := range questions {
		creation := incoming(aliceUser(), groupChat(), 100, "")
		creation.Poll = &signal.Poll{Question: question, Options: []string{"a", "b"}}
		events = append(events, creation)
	}

	runInbox(t, a, app.InboxOptions{}, events...)

	state, err := a.PollShow(t.Context(), app.PollShowRequest{GroupID: groupID, Target: aliceACI + ":100", Durable: true})
	if err != nil || state.Creation != nil || state.Observations != 4 || state.IgnoredInvalid != 4 {
		t.Fatalf("distinct invalid = %+v, %v", state, err)
	}
}

func TestBoundedPollZeroVoterCompatibility(t *testing.T) {
	t.Parallel()
	a := sender(t, directory())
	creation := incoming(aliceUser(), groupChat(), 100, "")
	creation.Poll = &signal.Poll{Question: "Q", Options: []string{"a", "b"}}
	runInbox(t, a, app.InboxOptions{}, creation, pollVote("00000000-0000-0000-0000-000000000000", 101, 1, 0))

	state, err := a.PollShow(t.Context(), app.PollShowRequest{GroupID: groupID, Target: aliceACI + ":100"})
	if err != nil || !slices.Equal(state.Tally, []int{1, 0}) {
		t.Fatalf("bounded compatibility = %+v, %v", state, err)
	}
}
