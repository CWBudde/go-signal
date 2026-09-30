package signal

// OutgoingPin pins a message for a finite number of seconds or forever.
type OutgoingPin struct {
	TargetAuthor    Recipient
	TargetTimestamp uint64
	DurationSeconds uint32
	Forever         bool
}

// OutgoingUnpin removes a pin on a message identified by its author and sent timestamp.
type OutgoingUnpin struct {
	TargetAuthor    Recipient
	TargetTimestamp uint64
}

// Pin is a received pin control; its duration starts at local receipt time.
type Pin struct {
	Envelope
	OutgoingPin
}

// Unpin is a received unpin control.
type Unpin struct {
	Envelope
	OutgoingUnpin
}

func (*Pin) isEvent()   {}
func (*Unpin) isEvent() {}
