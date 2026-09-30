package app

import (
	"time"

	"github.com/cwbudde/go-signal/internal/signal"
)

// PinRequest pins a target message in the selected chats.
type PinRequest struct {
	Recipients      []string
	Target          string
	DurationSeconds uint32
	Forever         bool
}

// UnpinRequest removes a target message's pin in the selected chats.
type UnpinRequest struct {
	Recipients []string
	Target     string
}

// PinSendResult preserves pin operation metadata and partial delivery outcomes.
type PinSendResult struct {
	SendResult

	Operation       string
	TargetAuthor    signal.Recipient
	TargetTimestamp uint64
	DurationSeconds uint32
	Forever         bool
}

// PinListRequest selects a bounded snapshot of a canonical chat's local inbox.
type PinListRequest struct {
	Chat      string
	ScanLimit int
}

// PinState reports retained observations, whose completeness is always unknown.
type PinState struct {
	Chat                      signal.Chat
	Observations              []PinObservation
	Completeness              string
	Scanned                   int
	FirstEntryID, LastEntryID int64
	Truncated                 bool
	IgnoredInvalid, Conflicts int
}

// PinObservation is the latest retained pin or unpin for a target message.
// ExpiresAt and ExpiryReached describe local receipt-based expiry, not phone state.
type PinObservation struct {
	TargetAuthor, Sender                  signal.Recipient
	TargetTimestamp, Timestamp            uint64
	Operation                             string
	EntryID                               int64
	ReceivedAt, ExpiresAt                 time.Time
	DurationSeconds                       uint32
	Forever, ExpiryReached, TargetDeleted bool
}
