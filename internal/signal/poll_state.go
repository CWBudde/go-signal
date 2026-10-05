package signal

// PollState is a view of retained observations, whose completeness is always unknown.
type PollState struct {
	// Source is durable for materialized observations, empty for a bounded inbox snapshot.
	Source string
	// Observations counts distinct poll evidence in a durable projection.
	Observations int

	Chat      Chat
	Author    Recipient
	Timestamp uint64
	Creation  *Poll
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
	Voter         Recipient
	OptionIndexes []uint32
	VoteCount     uint32
	Timestamp     uint64
}
