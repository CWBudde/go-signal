//go:build cgo || libsignal_go

package signal_test

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/go-signal/internal/store"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow/events"
)

// Removing terminal cancellation or replacing it when supervision starts must fail these tests.
func TestKeyCheckLogoutStopsFacadeSupervisor(t *testing.T) {
	t.Parallel()

	for _, sendOnly := range []bool{false, true} {
		for _, early := range []bool{false, true} {
			name := "receive"
			if sendOnly {
				name = "send-only"
			}

			if early {
				name += "/before-supervision"
			} else {
				name += "/during-worker-join"
			}

			t.Run(name, func(t *testing.T) {
				t.Parallel()
				testKeyCheckLogoutSupervisor(t, sendOnly, early)
			})
		}
	}
}

//nolint:cyclop,funlen // Keep the callback/join ordering and terminal assertions together.
func testKeyCheckLogoutSupervisor(t *testing.T, sendOnly, early bool) {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	dataDir := seedAccount(t)

	var opts []signal.ConnectOption
	if sendOnly {
		opts = append(opts, signal.SendOnly())
	}

	client := openOffline(t, dataDir, opts...)
	callbackDone := make(chan struct{})
	stopEntered := make(chan struct{}, 1)

	var starts atomic.Int32

	launch, supervised, terminal := signal.PrepareReceiveSupervisor(ctx, client,
		signal.ReconnectPolicy{MaxAttempts: 1},
		func() (<-chan signal.LoopStatus, error) {
			starts.Add(1)
			return nil, errStart
		},
		func() error {
			stopEntered <- struct{}{}
			// .24 StopReceiveLoops joins the synchronous logout callback.
			select {
			case <-callbackDone:
			case <-ctx.Done(): // Bound teardown if an assertion fails before callback entry.
			}

			return nil
		})

	var launchOnce sync.Once

	statuses := make(chan signal.LoopStatus)
	close(statuses) // Transports have disconnected before the key checker emits logout.

	if !early {
		launchOnce.Do(func() { launch(statuses) })

		select {
		case <-stopEntered:
		case <-ctx.Done():
			t.Fatal("supervisor did not join stopped workers:", ctx.Err())
		}
	}

	var acked bool

	go func() {
		defer close(callbackDone)

		acked = signal.Handle(client, &events.LoggedOut{Error: errTest})
	}()

	// Do not let Close cancel supervision or unblock delivery before testing terminal behavior.
	// On any assertion failure it still releases and joins the callback before other cleanups.
	t.Cleanup(func() {
		launchOnce.Do(func() { launch(statuses) })

		_ = client.Close()

		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()

		waitWebsocketLifecycle(t, cleanupCtx, callbackDone, "logout callback cleanup")
	})

	waitWebsocketLifecycle(t, ctx, terminal, "logout cancellation before event delivery")

	if !sendOnly {
		select {
		case <-callbackDone:
			t.Fatal("unread logout bypassed unbuffered Events delivery")
		default:
		}

		select {
		case evt := <-client.Events():
			conn, ok := evt.(*signal.Connection)
			if !ok || conn.State != signal.StateLoggedOut || !errors.Is(conn.Err, signal.ErrDeviceUnlinked) {
				t.Fatalf("event = %+v, want terminal device-unlinked logout", evt)
			}
		case <-ctx.Done():
			t.Fatal("logout was not delivered:", ctx.Err())
		}
	}

	waitWebsocketLifecycle(t, ctx, callbackDone, "logout callback")

	if !acked {
		t.Error("logout callback refused delivery")
	}

	if early {
		launchOnce.Do(func() { launch(statuses) })
	}

awaitSupervisor:
	for {
		select {
		case <-supervised:
			break awaitSupervisor
		case evt := <-client.Events():
			t.Errorf("connection event after terminal logout: %+v", evt)
		case <-ctx.Done():
			t.Fatal("supervisor did not terminate after logout:", ctx.Err())
		}
	}

	if got := starts.Load(); got != 0 {
		t.Errorf("restarted receive loops %d times after key-check logout", got)
	}

	if sendOnly && !errors.Is(signal.LostOr(client, errTest), signal.ErrDeviceUnlinked) {
		t.Error("send-only mode did not retain device-unlinked error")
	}

	dir, err := store.OpenDir(dataDir, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}

	accounts, err := dir.Accounts()
	if err != nil {
		t.Fatal(err)
	}

	if len(accounts) != 1 || accounts[0].UnlinkedAt.IsZero() {
		t.Errorf("account not persisted as unlinked: %+v", accounts)
	}

	err = client.Close()
	if err != nil {
		t.Fatal("Close after terminal logout:", err)
	}

	if _, ok := <-client.Events(); ok {
		t.Error("Events remains open after Close")
	}
}
