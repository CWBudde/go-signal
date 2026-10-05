package signal

import (
	"errors"

	"github.com/google/uuid"
)

// ErrPollVoteExhausted means automatic allocation cannot produce a larger uint32 counter.
var ErrPollVoteExhausted = errors.New("poll vote counter exhausted")

// PollVoteCounterRequest identifies this account's counter for a poll in one canonical chat.
type PollVoteCounterRequest struct {
	Chat      Chat
	Author    Recipient
	Timestamp uint64
	// Explicit is zero for automatic allocation; positive values are sent unchanged.
	Explicit uint32
}

// Check validates the same canonical destination and author identity as a poll vote.
func (req PollVoteCounterRequest) Check() error {
	id, err := uuid.Parse(req.Author.ACI)
	if err != nil || id == uuid.Nil || id.String() != req.Author.ACI {
		return ErrInvalidPoll
	}

	vote := &OutgoingPollVote{TargetAuthor: req.Author, TargetTimestamp: req.Timestamp, VoteCount: 1}

	send := SendRequest{PollVote: vote, GroupID: req.Chat.GroupID}
	if !req.Chat.IsGroup() {
		send.Recipients = []Recipient{req.Chat.Recipient}
	}

	return send.Check()
}

// OwnPollVoteCounter returns only validated own-device votes, never another voter's counter.
func OwnPollVoteCounter(evt Event, ownACI string) (PollVoteCounterRequest, bool) {
	vote, ok := evt.(*PollVote)
	if !ok || vote == nil || !vote.Sync || vote.Sender.ACI != ownACI || vote.Check() != nil {
		return PollVoteCounterRequest{}, false
	}

	req := PollVoteCounterRequest{
		Chat: vote.Chat, Author: vote.TargetAuthor, Timestamp: vote.TargetTimestamp, Explicit: vote.VoteCount,
	}

	return req, req.Check() == nil
}
