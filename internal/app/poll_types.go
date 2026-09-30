package app

import "github.com/cwbudde/go-signal/internal/signal"

// PollCreateRequest creates a poll in exactly one group.
type PollCreateRequest struct {
	GroupID      string
	Question     string
	Options      []string
	SingleChoice bool
}

// PollVoteRequest changes this account's selections with an explicit increasing counter.
type PollVoteRequest struct {
	GroupID       string
	Target        string
	OptionIndexes []uint32
	VoteCount     uint32
	Clear         bool
}

// PollCloseRequest closes our own poll at Target in one group.
type PollCloseRequest struct {
	GroupID string
	Target  uint64
}

// PollSendResult preserves the poll operation and partial group delivery outcomes.
type PollSendResult struct {
	SendResult

	Operation       string
	Poll            *signal.Poll
	TargetAuthor    signal.Recipient
	TargetTimestamp uint64
	OptionIndexes   []uint32
	VoteCount       uint32
}

// PollShowRequest selects a poll from bounded local group inbox history.
type PollShowRequest struct {
	GroupID   string
	Target    string
	ScanLimit int
}

// PollState is a view of retained observations, whose completeness is always unknown.
type PollState struct {
	Chat      signal.Chat
	Author    signal.Recipient
	Timestamp uint64
	Creation  *signal.Poll
	Votes     []PollStateVote
	// Tally is absent when creation is unavailable or the poll was deleted.
	Tally           []int
	ClosureObserved bool
	ClosedAt        uint64
	Deleted         bool
	Completeness    string
	Scanned         int
	FirstEntryID    int64
	LastEntryID     int64
	Truncated       bool
	IgnoredInvalid  int
	Conflicts       int
}

// PollStateVote is one voter's latest retained selection, including withdrawals.
type PollStateVote struct {
	Voter         signal.Recipient
	OptionIndexes []uint32
	VoteCount     uint32
	Timestamp     uint64
}
