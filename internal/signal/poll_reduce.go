package signal

import (
	"slices"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/google/uuid"
)

const (
	maxReceivedPollQuestion = 200
	maxReceivedPollOption   = 100
)

// ReducePoll reduces observations in delivery order, revalidating votes against any known
// creation. It never infers complete server state from local observations.
func ReducePoll(ref PollReference, events []Event) PollState {
	state := PollState{Chat: ref.Chat, Author: ref.Author, Timestamp: ref.Timestamp, Completeness: unknownRole}
	for _, event := range events {
		state.observeCreation(event)
	}

	votes := make(map[string]PollStateVote)
	for _, event := range events {
		state.observeControl(event, votes)
	}

	state.finalizeVotes(votes)

	return state
}

func pollEnvelope(event Event) (Envelope, bool) {
	switch evt := event.(type) {
	case *Message:
		return evt.Envelope, true
	case *PollVote:
		return evt.Envelope, true
	case *PollClose:
		return evt.Envelope, true
	case *Delete:
		return evt.Envelope, true
	default:
		return Envelope{}, false
	}
}

func validReceivedPoll(p *Poll) bool {
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

func validObservedVote(vote *PollVote, p *Poll) bool {
	voter, err := uuid.Parse(vote.Sender.ACI)
	if err != nil || voter.String() != vote.Sender.ACI {
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

func (state *PollState) observeCreation(event Event) {
	msg, ok := event.(*Message)
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

func (state *PollState) observeControl(event Event, votes map[string]PollStateVote) {
	env, ok := pollEnvelope(event)
	if !ok || env.Chat.Key() != state.Chat.Key() {
		return
	}

	if env.Timestamp == 0 && pollControlMatches(event, state) {
		state.IgnoredInvalid++
		return
	}

	switch evt := event.(type) {
	case *Delete:
		if evt.TargetTimestamp == state.Timestamp && evt.Sender.ACI == state.Author.ACI {
			state.Deleted = true
		}
	case *PollClose:
		state.observeClose(evt)

	case *PollVote:
		state.observeVote(evt, votes)
	}
}

func (state *PollState) observeVote(evt *PollVote, votes map[string]PollStateVote) {
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

func (state *PollState) observeClose(evt *PollClose) {
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

func (state *PollState) acceptVote(evt *PollVote, votes map[string]PollStateVote) bool {
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

func pollControlMatches(event Event, state *PollState) bool {
	switch evt := event.(type) {
	case *Delete:
		return evt.TargetTimestamp == state.Timestamp && evt.Sender.ACI == state.Author.ACI
	case *PollClose:
		return evt.TargetTimestamp == state.Timestamp
	case *PollVote:
		return evt.TargetTimestamp == state.Timestamp && evt.TargetAuthor.ACI == state.Author.ACI
	default:
		return false
	}
}

func samePollCreation(first, second *Poll) bool {
	return first.Question == second.Question && first.AllowMultiple == second.AllowMultiple &&
		slices.Equal(first.Options, second.Options)
}
