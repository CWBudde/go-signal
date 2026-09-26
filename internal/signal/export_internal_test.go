package signal

import (
	"context"
	"log/slog"
	"time"
)

// LoopStatus exposes loopStatus to the signal_test package.
type LoopStatus = loopStatus

// Supervise runs a supervisor over statuses until it returns (see supervisor.run).
func Supervise(
	ctx context.Context,
	log *slog.Logger,
	policy ReconnectPolicy,
	statuses <-chan LoopStatus,
	start func() (<-chan LoopStatus, error),
	stop func() error,
	emit func(Event) bool,
	loggedOut func(error) error,
) {
	sup := &supervisor{log: log, policy: policy, start: start, stop: stop, emit: emit, loggedOut: loggedOut}
	sup.run(ctx, statuses)
}

// Backoff exposes ReconnectPolicy.backoff to the signal_test package.
func (p ReconnectPolicy) Backoff(n int) time.Duration {
	return p.backoff(n)
}

// MarshalEvent exposes marshalEvent to the signal_test package.
func MarshalEvent(evt Event, chat Chat) ([]byte, error) {
	return marshalEvent(evt, chat)
}

// UnmarshalEvent exposes unmarshalEvent to the signal_test package.
func UnmarshalEvent(data []byte) (Event, Chat) {
	return unmarshalEvent(data)
}
