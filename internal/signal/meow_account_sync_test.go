//go:build cgo || libsignal_go

package signal_test

import (
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/go-signal/internal/store"
	"github.com/rs/zerolog"
)

func TestAccountLastSync(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name     string
		stored   string
		unlinked bool
	}{
		{name: "never synced"},
		{name: "stored", stored: "2026-10-04T12:30:00Z"},
		{name: "offset", stored: "2026-10-04T14:30:00.123+02:00"},
		{name: "unlinked", stored: "2026-10-04T12:30:00Z", unlinked: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			dataDir := seedAccount(t)
			if test.stored != "" {
				setLastSync(t, dataDir, seededACI, test.stored)
			}

			if test.unlinked {
				markSyncAccountUnlinked(t, dataDir)
			}

			client := openSeeded(t, dataDir)

			acc, err := client.Account(t.Context())
			if err != nil {
				t.Fatal(err)
			}

			var want time.Time
			if test.stored != "" {
				want, err = time.Parse(time.RFC3339, test.stored)
				if err != nil {
					t.Fatal(err)
				}
			}

			if !acc.LastSync.Equal(want) || acc.LastSync.Location() != time.UTC {
				t.Errorf("LastSync = %v, want %v in UTC", acc.LastSync, want)
			}
		})
	}
}

func TestAccountLastSyncSelected(t *testing.T) {
	t.Parallel()

	const otherACI = "33333333-3333-3333-3333-333333333333"

	dataDir := seedAccounts(t,
		signal.Account{Number: seededNumber, ACI: seededACI, DeviceID: 2},
		signal.Account{Number: "+15550200", ACI: otherACI, DeviceID: 3},
	)
	setLastSync(t, dataDir, seededACI, "2026-10-03T12:00:00Z")
	setLastSync(t, dataDir, otherACI, "2026-10-04T12:00:00Z")

	client, err := signal.Open(t.Context(), signal.Options{DataDir: dataDir, Account: otherACI})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	for _, stamp := range []string{"2026-10-04T12:00:00Z", "2026-10-04T13:00:00Z"} {
		setLastSync(t, dataDir, otherACI, stamp)

		acc, err := client.Account(t.Context())
		if err != nil {
			t.Fatal(err)
		}

		if acc.ACI != otherACI || acc.LastSync.Format(time.RFC3339) != stamp {
			t.Errorf("selected account = %+v, want %s at %s", acc, otherACI, stamp)
		}
	}
}

func setLastSync(t *testing.T, dataDir, aci, stamp string) {
	t.Helper()

	dir, err := store.OpenDir(dataDir, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}

	data, err := dir.OpenAccount(t.Context(), aci, zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	defer data.Close()

	err = data.SetMeta(t.Context(), "last_sync", stamp)
	if err != nil {
		t.Fatal(err)
	}
}

func TestAccountLastSyncCompletion(t *testing.T) {
	t.Parallel()

	dataDir := seedAccount(t)
	client := openOffline(t, dataDir, signal.SendOnly())

	const previous = "2026-09-20T12:00:00Z"

	setLastSync(t, dataDir, seededACI, previous)

	before := time.Now().UTC().Truncate(time.Second)

	_, err := signal.FinishSync(t.Context(), client, signal.SyncResult{
		MasterKey: true, Storage: true, ContactList: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	complete, err := client.Account(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	if complete.LastSync.Before(before) || complete.LastSync.After(time.Now()) {
		t.Fatalf("last complete sync = %v, want current time", complete.LastSync)
	}

	// Use a distinct stored time so an erroneous update in the same second still fails.
	setLastSync(t, dataDir, seededACI, previous)

	_, err = signal.FinishSync(t.Context(), client, signal.SyncResult{MasterKey: true, Storage: true})
	if !errors.Is(err, signal.ErrSyncIncomplete) {
		t.Fatalf("incomplete sync error = %v", err)
	}

	incomplete, err := client.Account(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	if incomplete.LastSync.Format(time.RFC3339) != previous {
		t.Errorf("incomplete sync changed last sync to %v from %s", incomplete.LastSync, previous)
	}
}

func markSyncAccountUnlinked(t *testing.T, dataDir string) {
	t.Helper()

	dir, err := store.OpenDir(dataDir, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}

	err = dir.MarkUnlinked(seededACI, time.Now())
	if err != nil {
		t.Fatal(err)
	}

	clearPassword(t, dataDir, seededACI)
}

func TestAccountLastSyncMalformed(t *testing.T) {
	t.Parallel()

	dataDir := seedAccount(t)
	setLastSync(t, dataDir, seededACI, "bad timestamp")
	client := openSeeded(t, dataDir)

	_, err := client.Account(t.Context())
	if _, ok := errors.AsType[*time.ParseError](err); !ok {
		t.Fatalf("Account error = %v, want timestamp parse error", err)
	}
}
