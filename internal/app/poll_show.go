package app

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/cwbudde/go-signal/internal/signal"
)

const (
	maxReceivedPollQuestion = 200
	maxReceivedPollOption   = 100
)

// PollShow reads one bounded local snapshot without connecting or resolving identities.
func (a *App) PollShow(ctx context.Context, req PollShowRequest) (PollState, error) {
	err := req.Check()
	if err != nil {
		return PollState{}, fmt.Errorf("poll show: %w", err)
	}

	err = ctx.Err()
	if err != nil {
		return PollState{}, fmt.Errorf("poll show: %w", err)
	}

	author, timestamp, _ := ParseTarget(req.Target)
	state := PollState{
		Chat: signal.Chat{GroupID: req.GroupID}, Author: author.Recipient,
		Timestamp: timestamp, Completeness: "unknown",
	}

	if req.Recipient != "" {
		state.Chat = signal.Chat{Recipient: signal.Recipient{ACI: req.Recipient}}
	}

	limit := req.ScanLimit
	if limit == 0 {
		limit = DefaultPollScanLimit
	}

	entries, err := a.client.InboxList(ctx, signal.InboxQuery{Chat: state.Chat.Key(), Newest: true, Limit: limit + 1})
	if err != nil {
		return PollState{}, fmt.Errorf("poll show: inbox: %w", err)
	}

	state.boundSnapshot(&entries, limit)

	for _, entry := range entries {
		state.observeCreation(entry.Event)
	}

	votes := map[string]PollStateVote{}

	for _, entry := range entries {
		err := ctx.Err()
		if err != nil {
			return PollState{}, fmt.Errorf("poll show: %w", err)
		}

		state.observeControl(entry.Event, votes)
	}

	state.finalizeVotes(votes)

	return state, nil
}

func validReceivedPoll(p *signal.Poll) bool {
	if len(p.Options) < 2 || len(p.Options) > 10 || !validPollText(p.Question, maxReceivedPollQuestion) {
		return false
	}

	for _, option := range p.Options {
		if !validPollText(option, maxReceivedPollOption) {
			return false
		}
	}

	return true
}

func validPollText(s string, limit int) bool {
	return utf8.ValidString(s) && strings.TrimSpace(s) != "" && len(utf16.Encode([]rune(s))) <= limit
}

func validObservedVote(vote *signal.PollVote, p *signal.Poll) bool {
	voter, err := ParseRecipient(vote.Sender.ACI)
	if err != nil || voter.Recipient.ACI == "" || voter.Recipient.ACI != vote.Sender.ACI {
		return false
	}

	copyVote := vote.OutgoingPollVote

	copyVote.VoteCount = 1
	if copyVote.Check() != nil {
		return false
	}

	if p == nil {
		return true
	}

	if !p.AllowMultiple && len(vote.OptionIndexes) > 1 {
		return false
	}

	for _, index := range vote.OptionIndexes {
		if int(index) >= len(p.Options) {
			return false
		}
	}

	return true
}

func samePollSelection(a, b []uint32) bool {
	a = slices.Clone(a)
	b = slices.Clone(b)

	slices.Sort(a)
	slices.Sort(b)

	return slices.Equal(a, b)
}

func (state *PollState) observeCreation(event signal.Event) {
	msg, ok := event.(*signal.Message)
	if !ok || msg.Poll == nil || msg.Chat.Key() != state.Chat.Key() {
		return
	}

	if msg.Sender.ACI != state.Author.ACI || msg.Timestamp != state.Timestamp {
		return
	}

	if !validReceivedPoll(msg.Poll) {
		state.IgnoredInvalid++
		return
	}

	if state.Creation != nil {
		if !samePollCreation(state.Creation, msg.Poll) {
			state.Conflicts++
		}

		return
	}

	copyPoll := *msg.Poll
	copyPoll.Options = slices.Clone(msg.Poll.Options)
	state.Creation = &copyPoll
}

func (state *PollState) observeControl(event signal.Event, votes map[string]PollStateVote) {
	env, ok := envelopeOf(event)
	if !ok || env.Chat.Key() != state.Chat.Key() {
		return
	}

	if env.Timestamp == 0 && pollControlMatches(event, state) {
		state.IgnoredInvalid++
		return
	}

	switch evt := event.(type) {
	case *signal.Delete:
		if evt.TargetTimestamp == state.Timestamp && evt.Sender.ACI == state.Author.ACI {
			state.Deleted = true
		}
	case *signal.PollClose:
		state.observeClose(evt)

	case *signal.PollVote:
		state.observeVote(evt, votes)
	}
}

func (state *PollState) observeVote(evt *signal.PollVote, votes map[string]PollStateVote) {
	if state.Deleted || state.ClosureObserved {
		return
	}

	if evt.TargetTimestamp != state.Timestamp || evt.TargetAuthor.ACI != state.Author.ACI {
		return
	}

	if !validObservedVote(evt, state.Creation) {
		state.IgnoredInvalid++
		return
	}

	if !state.acceptVote(evt, votes) {
		return
	}

	votes[evt.Sender.ACI] = PollStateVote{
		Voter: evt.Sender, OptionIndexes: slices.Clone(evt.OptionIndexes),
		VoteCount: evt.VoteCount, Timestamp: evt.Timestamp,
	}
}

func (state *PollState) boundSnapshot(entries *[]signal.InboxEntry, limit int) {
	slices.SortFunc(*entries, func(a, b signal.InboxEntry) int { return cmp.Compare(a.ID, b.ID) })

	if len(*entries) > limit {
		state.Truncated = true
		*entries = (*entries)[len(*entries)-limit:]
	}

	state.Scanned = len(*entries)
	if len(*entries) > 0 {
		state.FirstEntryID = (*entries)[0].ID
		state.LastEntryID = (*entries)[len(*entries)-1].ID
	}
}

func (state *PollState) finalizeVotes(votes map[string]PollStateVote) {
	for _, vote := range votes {
		state.Votes = append(state.Votes, vote)
	}

	slices.SortFunc(state.Votes, func(a, b PollStateVote) int { return strings.Compare(a.Voter.ACI, b.Voter.ACI) })

	if state.Creation == nil || state.Deleted {
		return
	}

	state.Tally = make([]int, len(state.Creation.Options))
	for _, vote := range state.Votes {
		for _, index := range vote.OptionIndexes {
			state.Tally[index]++
		}
	}
}

func (state *PollState) observeClose(evt *signal.PollClose) {
	if evt.TargetTimestamp != state.Timestamp {
		return
	}

	if evt.Sender.ACI != state.Author.ACI {
		state.IgnoredInvalid++
		return
	}

	if !state.Deleted && !state.ClosureObserved {
		state.ClosureObserved = true
		state.ClosedAt = evt.Timestamp
	}
}

func (state *PollState) acceptVote(evt *signal.PollVote, votes map[string]PollStateVote) bool {
	prev, exists := votes[evt.Sender.ACI]
	if !exists {
		return true
	}

	if evt.VoteCount != prev.VoteCount {
		return evt.VoteCount > prev.VoteCount
	}

	if !samePollSelection(evt.OptionIndexes, prev.OptionIndexes) {
		state.Conflicts++
	}

	return evt.Sync && evt.Timestamp > prev.Timestamp
}

func pollControlMatches(event signal.Event, state *PollState) bool {
	switch evt := event.(type) {
	case *signal.Delete:
		return evt.TargetTimestamp == state.Timestamp && evt.Sender.ACI == state.Author.ACI
	case *signal.PollClose:
		return evt.TargetTimestamp == state.Timestamp
	case *signal.PollVote:
		return evt.TargetTimestamp == state.Timestamp && evt.TargetAuthor.ACI == state.Author.ACI
	default:
		return false
	}
}

func samePollCreation(first, second *signal.Poll) bool {
	return first.Question == second.Question && first.AllowMultiple == second.AllowMultiple &&
		slices.Equal(first.Options, second.Options)
}
