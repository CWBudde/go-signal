package app

import "github.com/cwbudde/go-signal/internal/signal"

// PollCreateRequest creates a poll in exactly one group or direct chat.
type PollCreateRequest struct {
	GroupID string
	// Recipient is a direct recipient argument, mutually exclusive with GroupID.
	Recipient    string
	Question     string
	Options      []string
	SingleChoice bool
}

// PollVoteRequest changes this account's selections, reserving a durable counter before sending.
type PollVoteRequest struct {
	GroupID       string
	Recipient     string
	Target        string
	OptionIndexes []uint32
	// VoteCount is zero for automatic allocation; positive values are sent unchanged.
	VoteCount uint32
	Clear     bool
}

// PollCloseRequest closes our own poll at Target in one chat.
type PollCloseRequest struct {
	GroupID   string
	Recipient string
	Target    uint64
}

// PollSendResult preserves the poll operation and direct or partial group delivery outcomes.
type PollSendResult struct {
	SendResult

	Operation       string
	Poll            *signal.Poll
	TargetAuthor    signal.Recipient
	TargetTimestamp uint64
	OptionIndexes   []uint32
	VoteCount       uint32
}

// PollShowRequest selects a bounded inbox view or durable poll projection.
type PollShowRequest struct {
	// Durable selects the account-local projection instead of bounded inbox history.
	Durable bool
	GroupID string
	// Recipient must be a canonical chat ACI for offline inspection.
	Recipient string
	Target    string
	ScanLimit int
}

// PollState is the shared view of retained observations; completeness remains unknown.
type PollState = signal.PollState

// PollStateVote is one voter's latest retained selection.
type PollStateVote = signal.PollStateVote
