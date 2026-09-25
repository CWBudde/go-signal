package signal_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/internal/signal"
)

var (
	errFatal = errors.New("unexpected status opening websocket: 429")
	errStart = errors.New("start failed")
)

// fastPolicy gives up after two restarts without waiting.
func fastPolicy() signal.ReconnectPolicy {
	return signal.ReconnectPolicy{MaxAttempts: 2, InitialBackoff: time.Millisecond, MaxBackoff: time.Millisecond}
}

// loops fakes the receive loops. Each start hands out the next of runs.
type loops struct {
	mu     sync.Mutex
	runs   []chan signal.LoopStatus
	starts int
	stops  int
	// startErrs fail the first len(startErrs) starts.
	startErrs []error
}

func (l *loops) start() (<-chan signal.LoopStatus, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.starts++
	if l.starts <= len(l.startErrs) {
		return nil, l.startErrs[l.starts-1]
	}

	run := make(chan signal.LoopStatus, 8)
	l.runs = append(l.runs, run)

	return run, nil
}

func (l *loops) stop() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.stops++

	return nil
}

func (l *loops) counts() (int, int) {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.starts, l.stops
}

// supervise runs the supervisor on the first run's statuses until it returns and collects
// the emitted Connection events.
func supervise(
	t *testing.T, ctx context.Context, fake *loops, policy signal.ReconnectPolicy, first chan signal.LoopStatus,
) ([]*signal.Connection, string) {
	t.Helper()

	var (
		logs    bytes.Buffer
		emitted []*signal.Connection
	)

	log := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	emit := func(evt signal.Event) bool {
		conn, ok := evt.(*signal.Connection)
		if !ok {
			t.Errorf("unexpected event %T", evt)
		}

		emitted = append(emitted, conn)

		return true
	}
	loggedOut := func(cause error) error {
		return errors.Join(signal.ErrDeviceUnlinked, cause)
	}

	done := make(chan struct{})

	go func() {
		defer close(done)

		signal.Supervise(ctx, log, policy, first, fake.start, fake.stop, emit, loggedOut)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("supervisor did not return")
	}

	return emitted, logs.String()
}

// queue returns a channel holding statuses.
func queue(statuses ...signal.LoopStatus) chan signal.LoopStatus {
	out := make(chan signal.LoopStatus, len(statuses))
	for _, status := range statuses {
		out <- status
	}

	return out
}

func states(conns []*signal.Connection) []signal.ConnectionState {
	out := make([]signal.ConnectionState, 0, len(conns))
	for _, conn := range conns {
		out = append(out, conn.State)
	}

	return out
}

func equalStates(got []*signal.Connection, want ...signal.ConnectionState) bool {
	gotStates := states(got)
	if len(gotStates) != len(want) {
		return false
	}

	for i := range want {
		if gotStates[i] != want[i] {
			return false
		}
	}

	return true
}

func TestSupervisorTransitions(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	fake := &loops{}
	first := queue(
		signal.LoopStatus{State: signal.StateConnected},
		signal.LoopStatus{State: signal.StateConnected},
		signal.LoopStatus{}, // no change worth reporting
		signal.LoopStatus{State: signal.StateDisconnected, Err: errFatal},
		signal.LoopStatus{State: signal.StateConnected},
	)

	go func() {
		for len(first) > 0 {
			time.Sleep(time.Millisecond)
		}

		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	got, logs := supervise(t, ctx, fake, fastPolicy(), first)

	if !equalStates(got, signal.StateConnected, signal.StateDisconnected, signal.StateConnected) {
		t.Errorf("got states %v", states(got))
	}

	if strings.Count(logs, "connection state") != 3 {
		t.Errorf("want 3 logged transitions:\n%s", logs)
	}

	// signalmeow retries transient errors itself.
	if starts, stops := fake.counts(); starts != 0 || stops != 0 {
		t.Errorf("restarted on a transient error: %d starts, %d stops", starts, stops)
	}
}

func TestSupervisorRestartsStoppedLoops(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	fake := &loops{startErrs: []error{errStart}}
	first := queue(
		signal.LoopStatus{State: signal.StateConnected},
		signal.LoopStatus{State: signal.StateError, Err: errFatal, Stopped: true},
	)

	go func() {
		// The first restart fails; the second one connects.
		for {
			fake.mu.Lock()
			runs := len(fake.runs)
			fake.mu.Unlock()

			if runs == 1 {
				break
			}

			time.Sleep(time.Millisecond)
		}

		fake.runs[0] <- signal.LoopStatus{State: signal.StateConnected}

		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	got, _ := supervise(t, ctx, fake, signal.ReconnectPolicy{
		MaxAttempts: 3, InitialBackoff: time.Millisecond, MaxBackoff: time.Millisecond,
	}, first)

	if !equalStates(got, signal.StateConnected, signal.StateDisconnected, signal.StateConnected) {
		t.Errorf("got states %v", states(got))
	}

	if !errors.Is(got[1].Err, errFatal) {
		t.Errorf("disconnect error: %v", got[1].Err)
	}

	if starts, stops := fake.counts(); starts != 2 || stops != 1 {
		t.Errorf("got %d starts and %d stops, want 2 and 1", starts, stops)
	}
}

func TestSupervisorGivesUp(t *testing.T) {
	t.Parallel()

	fake := &loops{startErrs: []error{errStart, errStart, errStart}}

	first := queue(
		signal.LoopStatus{State: signal.StateError, Err: errFatal, Stopped: true},
	)

	got, _ := supervise(t, t.Context(), fake, fastPolicy(), first)

	if !equalStates(got, signal.StateDisconnected, signal.StateFailed) {
		t.Fatalf("got states %v", states(got))
	}

	err := got[1].Err
	if !errors.Is(err, signal.ErrConnectionFailed) || !errors.Is(err, errStart) {
		t.Errorf("failure error: %v", err)
	}

	if starts, _ := fake.counts(); starts != fastPolicy().MaxAttempts {
		t.Errorf("got %d starts, want %d", starts, fastPolicy().MaxAttempts)
	}
}

func TestSupervisorClosedStatuses(t *testing.T) {
	t.Parallel()

	fake := &loops{startErrs: []error{errStart, errStart}}
	first := make(chan signal.LoopStatus)
	close(first)

	got, _ := supervise(t, t.Context(), fake, fastPolicy(), first)

	if !equalStates(got, signal.StateDisconnected, signal.StateFailed) {
		t.Errorf("got states %v", states(got))
	}
}

func TestSupervisorLoggedOut(t *testing.T) {
	t.Parallel()

	fake := &loops{}

	first := queue(
		signal.LoopStatus{State: signal.StateConnected},
		signal.LoopStatus{State: signal.StateLoggedOut, Err: errFatal, Stopped: true},
	)

	got, _ := supervise(t, t.Context(), fake, fastPolicy(), first)

	if !equalStates(got, signal.StateConnected, signal.StateLoggedOut) {
		t.Fatalf("got states %v", states(got))
	}

	if !errors.Is(got[1].Err, signal.ErrDeviceUnlinked) {
		t.Errorf("logout error: %v", got[1].Err)
	}

	if starts, stops := fake.counts(); starts != 0 || stops != 1 {
		t.Errorf("got %d starts and %d stops, want 0 and 1", starts, stops)
	}
}

func TestSupervisorCancelDuringBackoff(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	fake := &loops{}

	first := queue(
		signal.LoopStatus{State: signal.StateError, Err: errFatal, Stopped: true},
	)

	time.AfterFunc(10*time.Millisecond, cancel)

	got, _ := supervise(t, ctx, fake, signal.ReconnectPolicy{
		MaxAttempts: 3, InitialBackoff: time.Hour, MaxBackoff: time.Hour,
	}, first)

	if !equalStates(got, signal.StateDisconnected) {
		t.Errorf("got states %v", states(got))
	}

	if starts, _ := fake.counts(); starts != 0 {
		t.Errorf("restarted after cancel: %d starts", starts)
	}
}

func TestReconnectBackoff(t *testing.T) {
	t.Parallel()

	policy := signal.ReconnectPolicy{MaxAttempts: 10, InitialBackoff: time.Second, MaxBackoff: 5 * time.Second}

	for n, want := range map[int]time.Duration{
		1: time.Second, 2: 2 * time.Second, 3: 4 * time.Second, 4: 5 * time.Second, 9: 5 * time.Second,
	} {
		if got := policy.Backoff(n); got != want {
			t.Errorf("Backoff(%d) = %v, want %v", n, got, want)
		}
	}
}
