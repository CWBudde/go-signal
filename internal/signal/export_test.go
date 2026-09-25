//go:build cgo

package signal

import "go.mau.fi/mautrix-signal/pkg/signalmeow/events"

// ConvertEvent exposes convertEvent to the signal_test package.
func ConvertEvent(raw events.SignalEvent, ownACI string) Event {
	return convertEvent(raw, ownACI)
}
