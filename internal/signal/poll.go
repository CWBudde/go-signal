package signal

// Poll describes a poll's question and ordered, zero-based answer options.
type Poll struct {
	Question      string
	Options       []string
	AllowMultiple bool
}

// OutgoingPollVote selects or clears options on a poll identified by its creator and timestamp.
type OutgoingPollVote struct {
	TargetAuthor    Recipient
	TargetTimestamp uint64
	OptionIndexes   []uint32
	// VoteCount orders changes by this voter; it is not a tally.
	VoteCount uint32
}

// OutgoingPollClose closes a poll created by the sending account.
type OutgoingPollClose struct {
	TargetTimestamp uint64
}

// PollVote is a received change to one voter's selections; an empty selection withdraws it.
type PollVote struct {
	Envelope
	OutgoingPollVote
}

// PollClose is a received closure. Sender identifies the original poll's creator.
type PollClose struct {
	Envelope
	OutgoingPollClose
}

func (*PollVote) isEvent()  {}
func (*PollClose) isEvent() {}
