package signal

import (
	"encoding/base64"
	"errors"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/google/uuid"
)

// ErrInvalidPoll means a poll payload is malformed or contains incompatible content.
var ErrInvalidPoll = errors.New("invalid poll")

const (
	pollTextLimit             = 100
	pollIncomingQuestionLimit = 200
	pollMaxOptions            = 10
	pollMinOptions            = 2
	pollGroupIDSize           = 32
)

// Check validates an outbound creation.
func (p Poll) Check() error { return p.check(pollTextLimit) }

func (p Poll) check(questionLimit int) error {
	if !pollText(p.Question, questionLimit) || len(p.Options) < pollMinOptions || len(p.Options) > pollMaxOptions {
		return ErrInvalidPoll
	}

	for _, option := range p.Options {
		if !pollText(option, pollTextLimit) {
			return ErrInvalidPoll
		}
	}

	return nil
}

func pollText(text string, limit int) bool {
	return utf8.ValidString(text) && strings.TrimSpace(text) != "" && len(utf16.Encode([]rune(text))) <= limit
}

// Check validates an outbound vote, including its explicit positive counter.
func (v OutgoingPollVote) Check() error {
	if v.VoteCount == 0 {
		return ErrInvalidPoll
	}

	return v.check()
}

func (v OutgoingPollVote) check() error {
	id, err := uuid.Parse(v.TargetAuthor.ACI)
	if err != nil || id == uuid.Nil || v.TargetTimestamp == 0 {
		return ErrInvalidPoll
	}

	seen := make(map[uint32]bool, len(v.OptionIndexes))
	for _, index := range v.OptionIndexes {
		if index >= pollMaxOptions || seen[index] {
			return ErrInvalidPoll
		}

		seen[index] = true
	}

	return nil
}

// Check validates an outbound closure.
func (p OutgoingPollClose) Check() error {
	if p.TargetTimestamp == 0 {
		return ErrInvalidPoll
	}

	return nil
}

func (req SendRequest) hasPoll() bool {
	return req.PollCreate != nil || req.PollVote != nil || req.PollClose != nil
}

func (req SendRequest) checkPoll() error {
	if !validPollGroup(req.GroupID) || len(req.Recipients) > 0 || req.pollHasContent() {
		return ErrInvalidPoll
	}

	count := 0
	if req.PollCreate != nil {
		count++
	}

	if req.PollVote != nil {
		count++
	}

	if req.PollClose != nil {
		count++
	}

	if count != 1 {
		return ErrInvalidPoll
	}

	if req.PollCreate != nil {
		return req.PollCreate.Check()
	}

	if req.PollVote != nil {
		return req.PollVote.Check()
	}

	return req.PollClose.Check()
}

func validPollGroup(groupID string) bool {
	raw, err := base64.StdEncoding.DecodeString(groupID)
	return err == nil && len(raw) == pollGroupIDSize && base64.StdEncoding.EncodeToString(raw) == groupID
}

func (req SendRequest) pollHasContent() bool {
	return req.hasContent() || req.Sticker != nil || req.Reaction != nil || req.DeleteTarget != 0 || req.EditTarget != 0
}
