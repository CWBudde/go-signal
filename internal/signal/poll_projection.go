package signal

import (
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"
)

// PollReference identifies a poll by stable chat, creator ACI and creation timestamp.
type PollReference struct {
	Chat      Chat
	Author    Recipient
	Timestamp uint64
}

// Check requires canonical account-independent poll identity, without resolving recipients.
func (ref PollReference) Check() error {
	return (PollVoteCounterRequest{Chat: ref.Chat, Author: ref.Author, Timestamp: ref.Timestamp}).Check()
}

// PollEvidence extracts poll-only evidence with stable identity metadata. Unrelated messages
// and events without a canonical poll key do not contribute. Invalid content with a valid key
// is retained so the reducer can count it. Copies isolate evidence from caller mutation.
//
//nolint:cyclop,funlen // One extraction/copy case and nil guard per supported event type.
func PollEvidence(event Event) (PollReference, Event, bool) {
	var (
		env       Envelope
		author    Recipient
		timestamp uint64
		copyEvent Event
	)

	switch evt := event.(type) {
	case *Message:
		if evt == nil || evt.Poll == nil {
			return PollReference{}, nil, false
		}

		env, author, timestamp = evt.Envelope, evt.Sender, evt.Timestamp
		poll := *evt.Poll

		poll.Options = slices.Clone(poll.Options)
		if !utf8.ValidString(poll.Question) || slices.ContainsFunc(poll.Options, func(option string) bool {
			return !utf8.ValidString(option)
		}) {
			// Private evidence must stay invalid after JSON replaces malformed UTF-8, while
			// preserving the original byte identity for deduplication.
			poll.Question = strings.Repeat(" ", maxReceivedPollQuestion+1) + fmt.Sprintf("%q %q", poll.Question, poll.Options)
		}

		copyEvent = &Message{Poll: &poll}
	case *PollVote:
		if evt == nil {
			return PollReference{}, nil, false
		}

		env, author, timestamp = evt.Envelope, evt.TargetAuthor, evt.TargetTimestamp
		vote := evt.OutgoingPollVote
		vote.TargetAuthor = Recipient{ACI: vote.TargetAuthor.ACI}
		vote.OptionIndexes = slices.Clone(vote.OptionIndexes)
		slices.Sort(vote.OptionIndexes)
		copyEvent = &PollVote{OutgoingPollVote: vote}
	case *PollClose:
		if evt == nil {
			return PollReference{}, nil, false
		}

		env, author, timestamp = evt.Envelope, evt.Sender, evt.TargetTimestamp
		copyEvent = &PollClose{OutgoingPollClose: evt.OutgoingPollClose}
	case *Delete:
		if evt == nil {
			return PollReference{}, nil, false
		}

		env, author, timestamp = evt.Envelope, evt.Sender, evt.TargetTimestamp
		copyEvent = &Delete{TargetTimestamp: evt.TargetTimestamp}
	default:
		return PollReference{}, nil, false
	}

	chat := Chat{GroupID: env.Chat.GroupID}
	if !chat.IsGroup() {
		chat.Recipient = Recipient{ACI: env.Chat.Recipient.ACI}
	}

	ref := PollReference{Chat: chat, Author: Recipient{ACI: author.ACI}, Timestamp: timestamp}
	if ref.Check() != nil {
		return PollReference{}, nil, false
	}

	normalized := Envelope{Chat: chat, Sender: Recipient{ACI: env.Sender.ACI}, Timestamp: env.Timestamp, Sync: env.Sync}

	switch evt := copyEvent.(type) {
	case *Message:
		evt.Envelope = normalized
	case *PollVote:
		evt.Envelope = normalized
	case *PollClose:
		evt.Envelope = normalized
	case *Delete:
		evt.Envelope = normalized
	}

	return ref, copyEvent, true
}
