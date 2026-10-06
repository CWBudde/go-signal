//go:build integration && (cgo || libsignal_go)

package signal_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/internal/signal"
)

// TestIntegrationRemoteUnlink links a temporary device, waits until it is removed on the phone
// and checks that the client reports the logout, marks the account as unlinked and refuses to
// talk to the server afterwards.
func TestIntegrationRemoteUnlink(t *testing.T) { //nolint:paralleltest // needs the phone
	if os.Getenv("GOSIGNAL_IT_REMOTE_UNLINK") != "1" {
		t.Skip("GOSIGNAL_IT_REMOTE_UNLINK not set to 1")
	}

	keep := os.Getenv("GOSIGNAL_IT_KEEP_UNLINKED") == "1"
	dataDir := unlinkDataDir(t, keep)
	timeout := envDuration(t, "GOSIGNAL_IT_TIMEOUT", defaultReceiptTimeout)

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()

	// A unique name, so that the right device is removed on the phone.
	name := fmt.Sprintf("go-signal unlink test %s %s", backend(), time.Now().Format(time.TimeOnly))
	client, acc := linkTemporary(ctx, t, dataDir, name)
	loggedOut := false

	defer func() {
		if loggedOut {
			return
		}

		// The device is still linked: remove it from the server, too, and keep nothing.
		cleanupLinkedDevice(t, client, dataDir)

		if keep {
			_ = os.RemoveAll(dataDir)
		}
	}()

	events := awaitRemoteLogout(ctx, t, client, name, timeout)
	loggedOut = true

	err := client.Close()
	if err != nil {
		t.Errorf("Close: %v", err)
	}

	events.wait()

	checkUnlinkedAccount(t, dataDir, acc)

	if keep {
		t.Logf("GOSIGNAL_IT_UNLINKED_DIR=%s", dataDir)

		return
	}

	removeUnlinkedAccount(t, dataDir, acc)
}

// unlinkDataDir returns the data dir for the device to unlink: a temporary one, or one that
// outlives the test with keep.
func unlinkDataDir(t *testing.T, keep bool) string {
	t.Helper()

	if !keep {
		return t.TempDir()
	}

	dataDir, err := os.MkdirTemp("", "go-signal-unlinked-") //nolint:usetesting // must outlive the test
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}

	t.Logf("data dir %s (kept)", dataDir)

	return dataDir
}

// awaitRemoteLogout connects client, asks to remove the device on the phone and waits for the
// logout, which must report ErrDeviceUnlinked.
func awaitRemoteLogout(
	ctx context.Context, t *testing.T, client signal.Client, name string, timeout time.Duration,
) *eventLog {
	t.Helper()

	err := client.Connect(ctx)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}

	events := collect(client.Events())

	fmt.Fprintf(os.Stderr, "Remove device %q on the phone (Settings > Linked devices)\n", name)

	waitFor(t, events, timeout, "logout", func(evt signal.Event) bool {
		conn, ok := evt.(*signal.Connection)

		return ok && conn.State == signal.StateLoggedOut
	})

	for _, evt := range events.all() {
		conn, ok := evt.(*signal.Connection)
		if ok && conn.State == signal.StateLoggedOut && !errors.Is(conn.Err, signal.ErrDeviceUnlinked) {
			t.Errorf("logout error %v, want ErrDeviceUnlinked", conn.Err)
		}
	}

	return events
}

// checkUnlinkedAccount reopens the account and checks that it is marked as unlinked and that
// the server calls fail right away.
func checkUnlinkedAccount(t *testing.T, dataDir string, acc signal.Account) {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	client, err := signal.Open(ctx, signal.Options{DataDir: dataDir, Logger: testLogger(t)})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	defer func() {
		err := client.Close()
		if err != nil {
			t.Errorf("Close: %v", err)
		}
	}()

	got, err := client.Account(ctx)
	if err != nil || got.ACI != acc.ACI || !got.Unlinked() {
		t.Errorf("Account = %s, unlinked at %s, %v; want %s marked as unlinked", got.ACI, got.UnlinkedAt, err, acc.ACI)
	}

	_, err = client.Devices(ctx)
	if !errors.Is(err, signal.ErrDeviceUnlinked) {
		t.Errorf("Devices: %v, want ErrDeviceUnlinked", err)
	}

	err = client.Connect(ctx, signal.SendOnly())
	if !errors.Is(err, signal.ErrDeviceUnlinked) {
		t.Errorf("Connect: %v, want ErrDeviceUnlinked", err)
	}
}

// removeUnlinkedAccount deletes the local data, as `account unlink --local-only` does.
func removeUnlinkedAccount(t *testing.T, dataDir string, acc signal.Account) {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	client, err := signal.Open(ctx, signal.Options{DataDir: dataDir, Logger: testLogger(t)})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	removed, err := client.Unlink(ctx, signal.UnlinkOptions{LocalOnly: true})

	err = errors.Join(err, client.Close())
	if err != nil || removed.ACI != acc.ACI {
		t.Fatalf("Unlink local only: removed %s, %v; want %s", removed.ACI, err, acc.ACI)
	}

	client, err = signal.Open(ctx, signal.Options{DataDir: dataDir, Logger: testLogger(t)})
	if err != nil {
		t.Fatalf("Open after Unlink: %v", err)
	}

	_, err = client.Account(ctx)
	if !errors.Is(err, signal.ErrNotLinked) {
		t.Errorf("Account after Unlink: %v, want ErrNotLinked", err)
	}

	err = client.Close()
	if err != nil {
		t.Errorf("Close: %v", err)
	}
}
