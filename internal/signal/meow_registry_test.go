//go:build cgo || libsignal_go

package signal_test

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/go-signal/internal/store"
)

// replaceSeeded unlinks the seeded account like another process would, then links it again,
// so that its registry entry is back with new files.
func replaceSeeded(t *testing.T, dataDir string, relink bool) {
	t.Helper()

	dir, err := store.OpenDir(dataDir, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}

	lock, err := dir.Lock(seededACI)
	if err != nil {
		t.Fatal(err)
	}

	err = errors.Join(dir.RemoveAccount(seededACI), lock.Unlock())
	if err != nil {
		t.Fatal(err)
	}

	if !relink {
		return
	}

	acc := signal.Account{Number: seededNumber, ACI: seededACI, DeviceID: 2}
	putDevice(t, dir, acc)

	err = dir.PutAccount(store.AccountEntry{Number: acc.Number, ACI: acc.ACI, DeviceID: acc.DeviceID})
	if err != nil {
		t.Fatal(err)
	}
}

// TestConnectAfterReplacement: a client that read an account never connects it after another
// process unlinked or replaced it, and recreates nothing.
func TestConnectAfterReplacement(t *testing.T) {
	t.Parallel()

	for name, relink := range map[string]bool{"after unlink": false, "after relink": true} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dataDir := seedAccount(t)
			client := openSeeded(t, dataDir)

			_, err := client.Account(t.Context())
			if err != nil {
				t.Fatalf("Account: %v", err)
			}

			replaceSeeded(t, dataDir, relink)

			// Unlinked, there is nothing left to select; relinked, the files are new.
			want := signal.ErrNotLinked
			if relink {
				want = signal.ErrAccountChanged
			}

			err = client.Connect(t.Context())
			if !errors.Is(err, want) {
				t.Fatalf("Connect: %v, want %v", err, want)
			}

			_, err = os.Stat(filepath.Join(dataDir, seededACI))
			if relink == (err != nil) {
				t.Errorf("account dir %s: %v", name, err)
			}

			// A new client selects the account afresh.
			if relink {
				_, err = openSeeded(t, dataDir).Account(t.Context())
				if err != nil {
					t.Errorf("Account after relink: %v", err)
				}
			}
		})
	}
}
