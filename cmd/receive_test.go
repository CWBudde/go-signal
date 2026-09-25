package cmd_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/cmd"
	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/go-signal/internal/signal/signaltest"
)

func testAccount() *signal.Account {
	return &signal.Account{
		Number:   "+15550100",
		ACI:      "11111111-1111-1111-1111-111111111111",
		PNI:      "22222222-2222-2222-2222-222222222222",
		DeviceID: 2,
	}
}

// run executes the root command against fake and returns its stdout. An empty config file keeps
// the user's config out without t.Setenv, so tests can run in parallel.
func run(t *testing.T, fake *signaltest.Fake, args ...string) (string, error) {
	t.Helper()

	return runContext(t, t.Context(), fake, args...)
}

// runContext is run with a context, e.g. to interrupt the command.
func runContext(t *testing.T, ctx context.Context, fake *signaltest.Fake, args ...string) (string, error) {
	t.Helper()

	cfgFile := filepath.Join(t.TempDir(), "config.yaml")

	err := os.WriteFile(cfgFile, nil, 0o600)
	if err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer

	//nolint:contextcheck // the command gets ctx through ExecuteContext
	root := cmd.NewRootCmd(cmd.WithClientFactory(fake.Factory), cmd.WithLocation(time.UTC))
	root.SetOut(&out)
	root.SetArgs(append([]string{"--config", cfgFile, "--data-dir", t.TempDir()}, args...))

	err = root.ExecuteContext(ctx)

	if !fake.AllClosed() {
		t.Error("client was not closed")
	}

	return out.String(), err
}

func TestLink(t *testing.T) {
	t.Parallel()

	fake := &signaltest.Fake{LinkAs: *testAccount()}

	out, err := run(t, fake, "link", "--name", "test-device")
	if err != nil {
		t.Fatalf("link: %v", err)
	}

	if !strings.HasPrefix(out, signaltest.LinkURI+"\n") {
		t.Errorf("output does not start with the link URI: %q", out)
	}

	want := "Linked +15550100 (ACI 11111111-1111-1111-1111-111111111111, device 2)\n"
	if !strings.HasSuffix(out, want) {
		t.Errorf("output does not end with %q: %q", want, out)
	}

	if len(fake.Linked) != 1 || fake.Linked[0] != *testAccount() {
		t.Errorf("account not stored: %+v", fake.Linked)
	}
}

func TestLinkError(t *testing.T) {
	t.Parallel()

	fake := &signaltest.Fake{LinkErr: signal.ErrNotImplemented}

	_, err := run(t, fake, "link")
	if !errors.Is(err, signal.ErrNotImplemented) {
		t.Fatalf("got %v, want the Link error", err)
	}
}

func TestReceiveDrainsUntilIdle(t *testing.T) {
	t.Parallel()

	fake := &signaltest.Fake{
		Linked: []signal.Account{*testAccount()},
		Incoming: []signal.Event{
			&signal.Connection{State: signal.StateConnected},
			&signal.Message{Body: "first"},
			&signal.Message{Body: "second"},
			&signal.QueueEmpty{},
		},
	}

	start := time.Now()

	out, err := run(t, fake, "receive", "--timeout", "50ms")
	if err != nil {
		t.Fatalf("receive: %v", err)
	}

	if !strings.Contains(out, "first") || !strings.Contains(out, "second") {
		t.Errorf("receive did not drain the queue: %q", out)
	}

	if got := fake.Delivered(); got != len(fake.Incoming) {
		t.Errorf("delivered %d events, want %d", got, len(fake.Incoming))
	}

	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("receive took %v to stop after the idle timeout", elapsed)
	}
}

func TestReceiveTimeout(t *testing.T) {
	t.Parallel()

	fake := &signaltest.Fake{Linked: []signal.Account{*testAccount()}}

	_, err := run(t, fake, "receive", "--timeout", "10ms")
	if err != nil {
		t.Fatalf("a timeout should not be an error: %v", err)
	}
}

func TestReceiveLoggedOut(t *testing.T) {
	t.Parallel()

	fake := &signaltest.Fake{
		Linked:   []signal.Account{*testAccount()},
		Incoming: []signal.Event{&signal.Connection{State: signal.StateLoggedOut}},
	}

	_, err := run(t, fake, "receive", "--timeout", "5s")
	if !errors.Is(err, signal.ErrDeviceUnlinked) {
		t.Fatalf("got %v, want ErrDeviceUnlinked", err)
	}
}

func TestReceiveAccountInUse(t *testing.T) {
	t.Parallel()

	fake := &signaltest.Fake{Linked: []signal.Account{*testAccount()}, InUse: true}

	_, err := run(t, fake, "receive", "--timeout", "5s")
	if !errors.Is(err, signal.ErrAccountInUse) {
		t.Fatalf("got %v, want ErrAccountInUse", err)
	}
}

func TestReceiveNotLinked(t *testing.T) {
	t.Parallel()

	_, err := run(t, &signaltest.Fake{}, "receive", "--timeout", "5s")
	if !errors.Is(err, signal.ErrNotLinked) {
		t.Fatalf("got %v, want ErrNotLinked", err)
	}
}

func TestReceivePassesGlobalFlags(t *testing.T) {
	t.Parallel()

	fake := &signaltest.Fake{Linked: []signal.Account{*testAccount()}}

	_, err := run(t, fake, "receive", "-a", "+15550199", "--timeout", "5s")
	if !errors.Is(err, signal.ErrAccountNotFound) {
		t.Fatalf("got %v, want ErrAccountNotFound", err)
	}

	opened := fake.Opened()
	if len(opened) != 1 || opened[0].Account != "+15550199" || opened[0].DataDir == "" {
		t.Errorf("unexpected client options: %+v", opened)
	}
}

// Message bodies in the tests below.
const (
	first  = "first"
	second = "second"
)

func TestReceiveMaxLeavesUnreadEvents(t *testing.T) {
	t.Parallel()

	for _, mode := range []string{"--timeout=5s", "--follow"} {
		fake := &signaltest.Fake{
			Linked: []signal.Account{*testAccount()},
			Incoming: []signal.Event{
				&signal.Connection{State: signal.StateConnected},
				&signal.Message{Body: first},
				&signal.QueueEmpty{},
				&signal.Typing{Started: true},
				&signal.Message{Body: second},
			},
		}

		out, err := run(t, fake, "receive", "--max", "2", mode)
		if err != nil {
			t.Fatalf("receive %s: %v", mode, err)
		}

		if strings.Contains(out, second) {
			t.Errorf("receive %s did not stop after 2 events: %q", mode, out)
		}

		// Connection changes and queueEmpty don't count; only what was read counts as
		// delivered (and would be acked).
		if got := fake.Delivered(); got != 4 {
			t.Errorf("receive %s delivered %d events, want 4", mode, got)
		}
	}
}

func TestReceiveMaxNegative(t *testing.T) {
	t.Parallel()

	fake := &signaltest.Fake{Linked: []signal.Account{*testAccount()}}

	_, err := run(t, fake, "receive", "--max", "-1")
	if err == nil {
		t.Fatal("negative --max accepted")
	}
}

func TestReceiveFollowUntilInterrupted(t *testing.T) {
	t.Parallel()

	fake := &signaltest.Fake{
		Linked: []signal.Account{*testAccount()},
		Incoming: []signal.Event{
			&signal.Connection{State: signal.StateConnected},
			&signal.Message{Body: first},
			&signal.Connection{State: signal.StateDisconnected},
			&signal.Connection{State: signal.StateConnected},
			&signal.Message{Body: second},
		},
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	go func() {
		for fake.Delivered() < len(fake.Incoming) {
			time.Sleep(time.Millisecond)
		}

		cancel()
	}()

	start := time.Now()

	out, err := runContext(t, ctx, fake, "receive", "--follow")
	if err != nil {
		t.Fatalf("an interrupt should end receive normally: %v", err)
	}

	if !strings.Contains(out, first) || !strings.Contains(out, second) {
		t.Errorf("receive --follow stopped early: %q", out)
	}

	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("receive took %v to stop", elapsed)
	}
}

func TestReceiveFollowConnectionFailed(t *testing.T) {
	t.Parallel()

	fake := &signaltest.Fake{
		Linked: []signal.Account{*testAccount()},
		Incoming: []signal.Event{
			&signal.Message{Body: first},
			&signal.Connection{State: signal.StateFailed},
		},
	}

	_, err := run(t, fake, "receive", "--follow")
	if !errors.Is(err, signal.ErrConnectionFailed) {
		t.Fatalf("got %v, want ErrConnectionFailed", err)
	}

	if code := cmd.ExitCode(err); code != cmd.ExitFailure {
		t.Errorf("exit code %d, want %d", code, cmd.ExitFailure)
	}
}

func TestReceiveFollowExcludesTimeout(t *testing.T) {
	t.Parallel()

	fake := &signaltest.Fake{Linked: []signal.Account{*testAccount()}}

	_, err := run(t, fake, "receive", "--follow", "--timeout", "5s")
	if err == nil || !strings.Contains(err.Error(), "none of the others can be") {
		t.Fatalf("got %v, want a flag conflict", err)
	}
}
