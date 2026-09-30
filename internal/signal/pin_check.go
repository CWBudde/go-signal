package signal

import (
	"errors"

	"github.com/google/uuid"
)

// ErrInvalidPin means a pin/unpin payload is malformed or contains incompatible content.
var ErrInvalidPin = errors.New("invalid pin")

// Check validates the target and exactly one positive duration mode.
func (p OutgoingPin) Check() error {
	if (p.DurationSeconds > 0) == p.Forever {
		return ErrInvalidPin
	}

	return (OutgoingUnpin{TargetAuthor: p.TargetAuthor, TargetTimestamp: p.TargetTimestamp}).Check()
}

// Check validates a non-nil author ACI and a positive target timestamp.
func (p OutgoingUnpin) Check() error {
	aci, err := uuid.Parse(p.TargetAuthor.ACI)
	if err != nil || aci == uuid.Nil || p.TargetTimestamp == 0 {
		return ErrInvalidPin
	}

	return nil
}

func (req SendRequest) hasPin() bool {
	return req.Pin != nil || req.Unpin != nil
}

func (req SendRequest) checkPin() error {
	if (req.Pin != nil && req.Unpin != nil) || req.pollHasContent() || req.hasPoll() {
		return ErrInvalidPin
	}

	if req.Pin != nil {
		return req.Pin.Check()
	}

	return req.Unpin.Check()
}
