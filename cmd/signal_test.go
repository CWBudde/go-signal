package cmd_test

import (
	"context"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/cmd"
)

func TestSignalContext(t *testing.T) {
	t.Parallel()

	sigs := make(chan os.Signal, 1)
	forced := make(chan os.Signal, 1)

	ctx, stop := cmd.SignalContext(t.Context(), sigs, func(sig os.Signal) { forced <- sig })
	defer stop()

	sigs <- os.Interrupt

	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("first signal did not cancel the context")
	}

	select {
	case sig := <-forced:
		t.Fatalf("first signal forced an exit (%v)", sig)
	default:
	}

	sigs <- syscall.SIGTERM

	select {
	case sig := <-forced:
		if sig != syscall.SIGTERM {
			t.Errorf("forced with %v, want SIGTERM", sig)
		}
	case <-time.After(time.Second):
		t.Fatal("second signal did not force an exit")
	}
}

func TestSignalContextStop(t *testing.T) {
	t.Parallel()

	sigs := make(chan os.Signal, 1)

	ctx, stop := cmd.SignalContext(t.Context(), sigs, func(os.Signal) { t.Error("forced after stop") })
	stop()
	stop()

	if ctx.Err() == nil {
		t.Error("stop did not cancel the context")
	}

	// A signal after stop never forces an exit.
	sigs <- os.Interrupt

	time.Sleep(10 * time.Millisecond)
}

func TestSignalContextParent(t *testing.T) {
	t.Parallel()

	parent, cancel := context.WithCancel(t.Context())
	ctx, stop := cmd.SignalContext(parent, make(chan os.Signal), func(os.Signal) {})

	defer stop()

	cancel()

	if ctx.Err() == nil {
		t.Error("cancelling the parent did not cancel the context")
	}
}
