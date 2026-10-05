package app_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/signal"
)

const pollCompletenessUnknown = "unknown"

//nolint:cyclop // Independent assertions cover the full operation result.
func TestPollCreateAndPreflight(t *testing.T) {
	t.Parallel()

	fake := directory()
	fake.Groups = map[string][]signal.Recipient{groupID: {{ACI: aliceACI}}}

	a := sender(t, fake)

	_, err := a.PollCreate(t.Context(),
		app.PollCreateRequest{
			GroupID:  groupID,
			Question: "  ",
			Options: []string{
				"a",
				"b",
			},
		})
	if err == nil {
		t.Fatal("blank question accepted")
	}

	if len(fake.Connects()) != 0 {
		t.Fatal("invalid request connected")
	}

	got,
		err := a.PollCreate(t.Context(),
		app.PollCreateRequest{
			GroupID:  groupID,
			Question: " Q ",
			Options: []string{
				"a",
				"b",
			},
		})
	if err != nil {
		t.Fatal(err)
	}

	if got.Operation != "create" || got.TargetTimestamp != sentAt || got.TargetAuthor.ACI != testAccount().ACI {
		t.Fatalf("result %+v", got)
	}

	sent := fake.Sent()
	if len(sent) != 1 ||
		sent[0].PollCreate == nil ||
		sent[0].PollCreate.Question != " Q " ||
		!sent[0].PollCreate.AllowMultiple {
		t.Fatalf("sent %+v", sent)
	}
}

func TestPollRequestChecks(t *testing.T) {
	t.Parallel()

	for _, req := range []app.PollVoteRequest{
		{GroupID: groupID, Target: aliceACI + ":10", VoteCount: 1},
		{GroupID: groupID, Target: aliceACI + ":10", VoteCount: 1, Clear: true, OptionIndexes: []uint32{0}},
		{GroupID: groupID, Target: aliceACI + ":10", VoteCount: 1, OptionIndexes: []uint32{1, 1}},
	} {
		if req.Check() == nil {
			t.Fatalf("accepted %+v", req)
		}
	}

	automatic := app.PollVoteRequest{GroupID: groupID, Target: aliceACI + ":10", OptionIndexes: []uint32{0}}

	err := automatic.Check()
	if err != nil {
		t.Fatalf("automatic counter preflight: %v", err)
	}

	if (app.PollShowRequest{GroupID: groupID, Target: "self:10"}).Check() == nil {
		t.Fatal("show accepted self")
	}
}

func pollVote(voter string, ts uint64, count uint32, choices ...uint32) *signal.PollVote {
	return &signal.PollVote{
		Envelope: signal.Envelope{
			Sender:    signal.Recipient{ACI: voter},
			Chat:      groupChat(),
			Timestamp: ts,
		},
		OutgoingPollVote: signal.OutgoingPollVote{
			TargetAuthor:    aliceUser(),
			TargetTimestamp: 100,
			VoteCount:       count,
			OptionIndexes:   choices,
		},
	}
}

//nolint:cyclop,funlen // A single literal timeline verifies interacting reducer rules.
func TestPollShowReduction(t *testing.T) {
	t.Parallel()
	a := sender(t, directory())
	creation := incoming(aliceUser(), groupChat(), 100, "")
	creation.Poll = &signal.Poll{Question: "Q", Options: []string{"a", "b"}, AllowMultiple: true}
	duplicate := incoming(aliceUser(), groupChat(), 100, "")
	duplicate.Poll = &signal.Poll{Question: "other", Options: []string{"a", "b"}}
	syncVote := pollVote(bobACI, 204, 2, 1)
	syncVote.Sync = true
	runInbox(t,
		a,
		app.InboxOptions{},
		pollVote(bobACI,
			200,
			1,
			0),
		creation,
		creation,
		duplicate,
		pollVote(bobACI,
			201,
			2,
			0),
		pollVote(bobACI,
			202,
			2,
			1),
		syncVote,
		pollVote(carolACI,
			203,
			1,
			1),
		pollVote(carolACI,
			205,
			2),
		pollVote(carolACI,
			206,
			3,
			2),
		&signal.PollClose{
			Envelope: signal.Envelope{
				Sender:    aliceUser(),
				Chat:      groupChat(),
				Timestamp: 207,
			},
			OutgoingPollClose: signal.OutgoingPollClose{TargetTimestamp: 100},
		},
		pollVote(bobACI,
			208,
			3,
			0))

	got, err := a.PollShow(t.Context(), app.PollShowRequest{GroupID: groupID, Target: aliceACI + ":100"})
	if err != nil {
		t.Fatal(err)
	}

	if got.Scanned != 12 ||
		got.FirstEntryID != 1 ||
		got.LastEntryID != 12 ||
		got.Truncated ||
		got.Completeness != pollCompletenessUnknown ||
		got.Conflicts != 3 ||
		got.IgnoredInvalid != 1 ||
		!got.ClosureObserved ||
		got.ClosedAt != 207 {
		t.Fatalf("state %+v", got)
	}

	if len(got.Tally) != 2 || got.Tally[0] != 0 || got.Tally[1] != 1 {
		t.Fatalf("tally %v", got.Tally)
	}

	if len(got.Votes) != 2 {
		t.Fatalf("votes %+v", got.Votes)
	}

	if len(signalRecipientsForPoll(syncVote)) != 3 {
		t.Fatal("poll recipients missing")
	}
}
func signalRecipientsForPoll(e signal.Event) []signal.Recipient { return app.EventRecipients(e) }

//nolint:cyclop // Literal observations and independent result assertions.
func TestPollShowMissingTruncatedDeleted(t *testing.T) {
	t.Parallel()
	a := sender(t, directory())
	creation := incoming(aliceUser(), groupChat(), 100, "")
	creation.Poll = &signal.Poll{Question: "Q", Options: []string{"a", "b"}}
	runInbox(t,
		a,
		app.InboxOptions{},
		creation,
		pollVote(bobACI,
			101,
			0,
			1),
		&signal.Delete{
			Envelope: signal.Envelope{
				Sender:    aliceUser(),
				Chat:      groupChat(),
				Timestamp: 102,
			},
			TargetTimestamp: 100,
		},
		pollVote(bobACI,
			103,
			2,
			0))

	got, err := a.PollShow(t.Context(), app.PollShowRequest{GroupID: groupID, Target: aliceACI + ":100"})
	if err != nil {
		t.Fatal(err)
	}

	if !got.Deleted || got.Tally != nil || len(got.Votes) != 1 || got.Votes[0].OptionIndexes[0] != 1 {
		t.Fatalf("deleted %+v", got)
	}

	got, err = a.PollShow(t.Context(), app.PollShowRequest{GroupID: groupID, Target: aliceACI + ":100", ScanLimit: 1})
	if err != nil {
		t.Fatal(err)
	}

	if !got.Truncated ||
		got.Scanned != 1 ||
		got.FirstEntryID != 4 ||
		got.Creation != nil ||
		got.Tally != nil ||
		len(got.Votes) != 1 {
		t.Fatalf("truncated %+v", got)
	}
}

//nolint:cyclop // Literal observations and independent result assertions.
func TestPollVoteCloseAndPartialFailure(t *testing.T) {
	t.Parallel()

	fake := directory()
	fake.Groups = map[string][]signal.Recipient{groupID: {{ACI: bobACI}}}
	fake.SendFailures = map[string]error{bobACI: signal.ErrNotOnSignal}
	a := sender(t, fake)

	got,
		err := a.PollVote(t.Context(),
		app.PollVoteRequest{
			GroupID:       groupID,
			Target:        aliceNumber + ":100",
			VoteCount:     4,
			OptionIndexes: []uint32{1},
		})
	if !errors.Is(err, app.ErrSendFailed) || got.TargetAuthor.ACI != aliceACI || got.Failed() != 1 {
		t.Fatalf("vote %+v %v", got, err)
	}

	closed, err := a.PollClose(t.Context(), app.PollCloseRequest{GroupID: groupID, Target: 100})
	if !errors.Is(err, app.ErrSendFailed) || closed.TargetAuthor.ACI != testAccount().ACI || closed.Operation != "close" {
		t.Fatalf("close %+v %v", closed, err)
	}

	if closed.Timestamp <= got.Timestamp {
		t.Fatal("timestamp reused")
	}

	sent := fake.Sent()
	if len(sent) != 2 || sent[0].PollVote.VoteCount != 4 || sent[1].PollClose.TargetTimestamp != 100 {
		t.Fatalf("sent %+v", sent)
	}
}

func TestPollShowErrorsAndBounds(t *testing.T) {
	t.Parallel()

	for _, limit := range []int{0, 1000, 10000} {
		err := (app.PollShowRequest{GroupID: groupID, Target: aliceACI + ":100", ScanLimit: limit}).Check()
		if err != nil {
			t.Fatalf("limit %d: %v", limit, err)
		}
	}

	for _, limit := range []int{-1, 10001} {
		if (app.PollShowRequest{GroupID: groupID, Target: aliceACI + ":100", ScanLimit: limit}).Check() == nil {
			t.Fatalf("accepted %d", limit)
		}
	}

	fake := directory()
	a := sender(t, fake)

	fake.InboxErr = signal.ErrUnknownGroup

	_, err := a.PollShow(t.Context(),
		app.PollShowRequest{
			GroupID: groupID,
			Target:  aliceACI + ":100",
		})
	if !errors.Is(err, fake.InboxErr) {
		t.Fatalf("inbox err %v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err = a.PollShow(ctx,
		app.PollShowRequest{
			GroupID: groupID,
			Target:  aliceACI + ":100",
		})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation %v", err)
	}

	if len(fake.Connects()) != 0 {
		t.Fatal("offline show connected")
	}
}

func TestPollVoteAllowlistPrecedesAuthorResolution(t *testing.T) {
	t.Parallel()

	fake := directory()
	a := restricted(t, fake, aliceACI)

	_,
		err := a.PollVote(t.Context(),
		app.PollVoteRequest{
			GroupID:   groupID,
			Target:    "+19999999999:100",
			VoteCount: 1,
			Clear:     true,
		})
	if !errors.Is(err, app.ErrRecipientNotAllowed) {
		t.Fatalf("want denied group before unknown author: %v", err)
	}

	if len(fake.Sent()) != 0 {
		t.Fatal("denied poll sent")
	}
}

func TestPollShowSingleChoiceAndInvalidObservations(t *testing.T) {
	t.Parallel()
	a := sender(t, directory())
	creation := incoming(aliceUser(), groupChat(), 100, "")
	creation.Poll = &signal.Poll{Question: "Q", Options: []string{"a", "b"}}
	runInbox(t,
		a,
		app.InboxOptions{},
		creation,
		pollVote(bobACI,
			101,
			1,
			0,
			1),
		pollVote(bobACI,
			102,
			2,
			1,
			1),
		pollVote(bobACI,
			103,
			3,
			10),
		&signal.PollClose{
			Envelope: signal.Envelope{
				Sender:    bobUser(),
				Chat:      groupChat(),
				Timestamp: 104,
			},
			OutgoingPollClose: signal.OutgoingPollClose{TargetTimestamp: 100},
		},
		pollVote(bobACI,
			105,
			4,
			1),
		pollVote(bobACI,
			106,
			3,
			0))

	got, err := a.PollShow(t.Context(), app.PollShowRequest{GroupID: groupID, Target: aliceACI + ":100"})
	if err != nil {
		t.Fatal(err)
	}

	if got.IgnoredInvalid != 4 || got.ClosureObserved || !reflect.DeepEqual(got.Tally, []int{0, 1}) {
		t.Fatalf("state %+v", got)
	}

	if len(got.Votes) != 1 || got.Votes[0].VoteCount != 4 {
		t.Fatalf("votes %+v", got.Votes)
	}
}

func TestPollShowRejectsNilAuthorAndZeroControlTimestamp(t *testing.T) {
	t.Parallel()

	if (app.PollShowRequest{GroupID: groupID, Target: "00000000-0000-0000-0000-000000000000:100"}).Check() == nil {
		t.Fatal("nil ACI accepted")
	}

	a := sender(t, directory())
	runInbox(t, a, app.InboxOptions{},
		&signal.PollClose{
			Envelope:          signal.Envelope{Sender: aliceUser(), Chat: groupChat()},
			OutgoingPollClose: signal.OutgoingPollClose{TargetTimestamp: 100},
		},
		&signal.Delete{
			Envelope: signal.Envelope{Sender: aliceUser(), Chat: groupChat()}, TargetTimestamp: 100,
		},
		pollVote(bobACI, 0, 1, 0),
	)

	got, err := a.PollShow(t.Context(), app.PollShowRequest{GroupID: groupID, Target: aliceACI + ":100"})
	if err != nil {
		t.Fatal(err)
	}

	if got.ClosureObserved || got.Deleted || len(got.Votes) != 0 || got.IgnoredInvalid != 3 {
		t.Fatalf("malformed controls %+v", got)
	}
}

func TestPollInboxUnreadAndNames(t *testing.T) {
	t.Parallel()
	a := sender(t, directory())
	creation := incoming(aliceUser(), groupChat(), 100, "")
	creation.Poll = &signal.Poll{Question: "Q", Options: []string{"a", "b"}}
	vote := pollVote(bobACI, 101, 1, 0)
	closeEvent := &signal.PollClose{
		Envelope:          signal.Envelope{Sender: aliceUser(), Chat: groupChat(), Timestamp: 102},
		OutgoingPollClose: signal.OutgoingPollClose{TargetTimestamp: 100},
	}
	inbox := runInbox(t, a, app.InboxOptions{}, creation, vote, closeEvent)

	page, err := inbox.List(t.Context(), app.MessagesRequest{Chat: groupChat().Key()})
	if err != nil {
		t.Fatal(err)
	}

	if len(page.Entries) != 3 {
		t.Fatalf("entries %+v", page.Entries)
	}

	if !page.Entries[0].Unread || page.Entries[1].Unread || page.Entries[2].Unread {
		t.Fatalf("unread %+v", page.Entries)
	}

	if !page.Entries[1].Time.Equal(time.UnixMilli(101)) {
		t.Fatalf("vote time %v", page.Entries[1].Time)
	}

	if !reflect.DeepEqual(app.EventRecipients(closeEvent), []signal.Recipient{aliceUser(), {}}) {
		t.Fatal("closure recipient names missing")
	}
}

func TestPollVoteRejectsNilAuthorBeforeConnecting(t *testing.T) {
	t.Parallel()

	req := app.PollVoteRequest{
		GroupID: groupID, Target: "00000000-0000-0000-0000-000000000000:100", VoteCount: 1, Clear: true,
	}
	if !errors.Is(req.Check(), signal.ErrInvalidPoll) {
		t.Fatal("nil vote author passed preflight")
	}

	fake := directory()

	_, err := sender(t, fake).PollVote(t.Context(), req)
	if !errors.Is(err, signal.ErrInvalidPoll) {
		t.Fatalf("vote error %v", err)
	}

	if len(fake.Connects()) != 0 {
		t.Fatal("invalid target connected")
	}
}

func TestPollVoteDeferredAuthorPreflight(t *testing.T) {
	t.Parallel()

	for _, author := range []string{aliceNumber, bobUsername, app.SelfRecipient, aliceACI} {
		req := app.PollVoteRequest{GroupID: groupID, Target: author + ":100", VoteCount: 1, Clear: true}

		err := req.Check()
		if err != nil {
			t.Fatalf("valid deferred author %s: %v", author, err)
		}
	}
}
