package signal

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

// ErrConnectionFailed means the client gave up reconnecting (a StateFailed event).
var ErrConnectionFailed = errors.New("connection to the Signal server failed")

// ReconnectPolicy bounds how often the client restarts its receive loops after they gave up.
// Transient errors (network down, 5xx) don't count: signalmeow's websockets retry those on their
// own, with a backoff of 10 s up to 1 min. The loops only give up on errors signalmeow considers
// fatal, such as an unexpected 4xx (e.g. 429 rate limiting).
type ReconnectPolicy struct {
	// MaxAttempts is the number of consecutive restarts without a successful connection; after
	// that the client reports StateFailed.
	MaxAttempts int
	// InitialBackoff is the delay before the first restart; it doubles up to MaxBackoff.
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
}

// The ReconnectPolicy of the signalmeow-backed client.
const (
	defaultMaxAttempts    = 5
	defaultInitialBackoff = 2 * time.Second
	defaultMaxBackoff     = time.Minute
)

// backoff returns the delay before restart attempt n (1-based).
func (p ReconnectPolicy) backoff(n int) time.Duration {
	delay := p.InitialBackoff
	for i := 1; i < n && delay < p.MaxBackoff; i++ {
		delay *= 2
	}

	return min(delay, p.MaxBackoff)
}

// loopStatus is a connection status reported by the receive loops.
type loopStatus struct {
	// State is the new state; zero means no change worth reporting.
	State ConnectionState
	Err   error
	// Stopped means the loops gave up and have to be restarted.
	Stopped bool
}

// supervisor watches the receive loops: it reports state transitions as Connection events (and in
// the debug log), turns a logout into UnlinkedError, and restarts the loops when they give up.
type supervisor struct {
	log    *slog.Logger
	policy ReconnectPolicy
	// start (re)starts the receive loops; stop stops them. Both are only called by run.
	start func() (<-chan loopStatus, error)
	stop  func() error
	// emit delivers an event to the consumer.
	emit func(Event) bool
	// loggedOut records a logout and returns the error to report.
	loggedOut func(cause error) error

	state ConnectionState
}

// run consumes statuses until ctx is done or the connection is lost for good (StateLoggedOut or
// StateFailed, after which the loops are stopped).
func (s *supervisor) run(ctx context.Context, statuses <-chan loopStatus) {
	attempts := 0

	for {
		status, ok := next(ctx, statuses)
		if !ok {
			return
		}

		switch {
		case status.State == StateLoggedOut:
			s.stopLoops()
			s.transition(ctx, &Connection{State: StateLoggedOut, Err: s.loggedOut(status.Err)})

			return
		case status.Stopped:
			s.stopLoops()
			s.transition(ctx, &Connection{State: StateDisconnected, Err: status.Err})

			statuses, attempts = s.restart(ctx, attempts, status.Err)
			if statuses == nil {
				return
			}
		case status.State == StateConnected:
			attempts = 0

			s.transition(ctx, &Connection{State: StateConnected})
		case status.State != 0:
			s.transition(ctx, &Connection{State: status.State, Err: status.Err})
		}
	}
}

// next returns the next status, or false once ctx is done. A closed channel means that the
// loops ended on their own, so it counts as stopped.
func next(ctx context.Context, statuses <-chan loopStatus) (loopStatus, bool) {
	select {
	case <-ctx.Done():
		return loopStatus{}, false
	case status, ok := <-statuses:
		if !ok {
			return loopStatus{State: StateDisconnected, Stopped: true}, ctx.Err() == nil
		}

		return status, true
	}
}

// restart starts the loops again after a backoff, retrying up to the policy's limit. It returns
// the new status channel and attempt count, or nil after giving up (StateFailed) or when ctx is
// done.
func (s *supervisor) restart(ctx context.Context, attempts int, cause error) (<-chan loopStatus, int) {
	for {
		attempts++
		if attempts > s.policy.MaxAttempts {
			err := fmt.Errorf("%w: gave up after %d attempts", ErrConnectionFailed, s.policy.MaxAttempts)
			if cause != nil {
				err = fmt.Errorf("%w: %w", err, cause)
			}

			s.transition(ctx, &Connection{State: StateFailed, Err: err})

			return nil, attempts
		}

		delay := s.policy.backoff(attempts)
		s.log.Debug("reconnecting", "attempt", attempts, "of", s.policy.MaxAttempts, "backoff", delay, "error", cause)

		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()

			return nil, attempts
		case <-timer.C:
		}

		statuses, err := s.start()
		if err == nil {
			return statuses, attempts
		}

		cause = err
	}
}

func (s *supervisor) stopLoops() {
	err := s.stop()
	if err != nil {
		s.log.Debug("stop receive loops", "error", err)
	}
}

// transition logs and emits conn if its state differs from the current one.
func (s *supervisor) transition(ctx context.Context, conn *Connection) {
	if conn.State == s.state {
		return
	}

	s.log.Debug("connection state", "state", conn.State, "error", conn.Err)
	s.state = conn.State

	if ctx.Err() == nil {
		s.emit(conn)
	}
}
