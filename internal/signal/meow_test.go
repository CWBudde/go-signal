//go:build cgo

package signal_test

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/go-signal/internal/store"
	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"go.mau.fi/mautrix-signal/pkg/libsignalgo"
	mstore "go.mau.fi/mautrix-signal/pkg/signalmeow/store"
)

const (
	seededACI    = "11111111-1111-1111-1111-111111111111"
	seededNumber = "+15550100"
	deviceName   = "laptop"
)

func TestOpenWithoutAccount(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	client, err := signal.Open(ctx, signal.Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	_, err = client.Account(ctx)
	if !errors.Is(err, signal.ErrNotLinked) {
		t.Errorf("Account: got %v, want ErrNotLinked", err)
	}

	err = client.Connect(ctx)
	if !errors.Is(err, signal.ErrNotLinked) {
		t.Errorf("Connect: got %v, want ErrNotLinked", err)
	}

	_, err = client.Send(ctx, signal.SendRequest{})
	if !errors.Is(err, signal.ErrNotConnected) {
		t.Errorf("Send: got %v, want ErrNotConnected", err)
	}

	err = client.Close()
	if err != nil {
		t.Fatalf("close: %v", err)
	}

	if _, ok := <-client.Events(); ok {
		t.Error("Events is not closed after Close")
	}

	err = client.Close()
	if err != nil {
		t.Errorf("second close: %v", err)
	}
}

func TestOpenUnknownAccount(t *testing.T) {
	t.Parallel()

	client, err := signal.Open(t.Context(), signal.Options{DataDir: t.TempDir(), Account: "+15550199"})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer client.Close()

	_, err = client.Account(t.Context())
	if !errors.Is(err, signal.ErrAccountNotFound) {
		t.Errorf("got %v, want ErrAccountNotFound", err)
	}
}

func TestAccountFromDataDir(t *testing.T) {
	t.Parallel()

	dataDir := seedAccount(t)

	for _, sel := range []string{"", seededNumber, seededACI} {
		client, err := signal.Open(t.Context(), signal.Options{DataDir: dataDir, Account: sel})
		if err != nil {
			t.Fatalf("open: %v", err)
		}

		acc, err := client.Account(t.Context())
		if err != nil || acc.ACI != seededACI || acc.Number != seededNumber || acc.DeviceID != 2 {
			t.Errorf("-a %q: got %+v, %v", sel, acc, err)
		}

		_ = client.Close()
	}
}

func TestConnectAccountInUse(t *testing.T) {
	t.Parallel()

	dataDir := seedAccount(t)

	dir, err := store.OpenDir(dataDir, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}

	lock, err := dir.Lock(seededACI)
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = lock.Unlock() }()

	client, err := signal.Open(t.Context(), signal.Options{DataDir: dataDir})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer client.Close()

	// Reading the account doesn't need the lock; connecting does.
	_, err = client.Account(t.Context())
	if err != nil {
		t.Errorf("Account: %v", err)
	}

	err = client.Connect(t.Context())
	if !errors.Is(err, signal.ErrAccountInUse) {
		t.Errorf("Connect: got %v, want ErrAccountInUse", err)
	}
}

func TestAccountSelectionWithTwoAccounts(t *testing.T) {
	t.Parallel()

	second := signal.Account{Number: "+15550101", ACI: secondACI, DeviceID: 3}
	dataDir := seedAccounts(t, signal.Account{Number: seededNumber, ACI: seededACI, DeviceID: 2}, second)

	client, err := signal.Open(t.Context(), signal.Options{DataDir: dataDir})
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	_, err = client.Account(t.Context())
	if !errors.Is(err, signal.ErrAccountRequired) {
		t.Errorf("without -a: got %v, want ErrAccountRequired", err)
	}

	_ = client.Close()

	for _, sel := range []string{second.Number, second.ACI} {
		client, err := signal.Open(t.Context(), signal.Options{DataDir: dataDir, Account: sel})
		if err != nil {
			t.Fatalf("open: %v", err)
		}

		acc, err := client.Account(t.Context())
		if err != nil || acc.ACI != second.ACI || acc.DeviceID != second.DeviceID {
			t.Errorf("-a %s: got %+v, %v", sel, acc, err)
		}

		_ = client.Close()
	}
}

func TestAccountDetailsFromAccountsJSON(t *testing.T) {
	t.Parallel()

	linkedAt := time.Date(2026, 9, 20, 12, 30, 0, 0, time.UTC)
	dataDir := seedAccounts(t, signal.Account{
		Number: seededNumber, ACI: seededACI, DeviceID: 2, DeviceName: deviceName, LinkedAt: linkedAt,
	})

	client, err := signal.Open(t.Context(), signal.Options{DataDir: dataDir})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer client.Close()

	acc, err := client.Account(t.Context())
	if err != nil || acc.DeviceName != deviceName || !acc.LinkedAt.Equal(linkedAt) || acc.PNI == "" {
		t.Errorf("got %+v, %v", acc, err)
	}
}

func TestUnlinkLocalOnly(t *testing.T) {
	t.Parallel()

	second := signal.Account{Number: "+15550101", ACI: secondACI, DeviceID: 3}
	dataDir := seedAccounts(t, signal.Account{Number: seededNumber, ACI: seededACI, DeviceID: 2}, second)

	client, err := signal.Open(t.Context(), signal.Options{DataDir: dataDir, Account: seededNumber})
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	// Reading the account first opens its database, which Unlink must close before deleting it.
	_, err = client.Account(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	acc, err := client.Unlink(t.Context(), signal.UnlinkOptions{LocalOnly: true})
	if err != nil || acc.ACI != seededACI {
		t.Fatalf("unlink: %+v, %v", acc, err)
	}

	_ = client.Close()

	_, err = os.Stat(filepath.Join(dataDir, seededACI))
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("account dir still exists: %v", err)
	}

	// The other account is untouched and now the only one, so -a is no longer needed.
	client, err = signal.Open(t.Context(), signal.Options{DataDir: dataDir})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer client.Close()

	acc, err = client.Account(t.Context())
	if err != nil || acc.ACI != second.ACI {
		t.Errorf("remaining account: %+v, %v", acc, err)
	}
}

func TestUnlinkAccountInUse(t *testing.T) {
	t.Parallel()

	dataDir := seedAccount(t)

	dir, err := store.OpenDir(dataDir, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}

	lock, err := dir.Lock(seededACI)
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = lock.Unlock() }()

	client, err := signal.Open(t.Context(), signal.Options{DataDir: dataDir})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer client.Close()

	_, err = client.Unlink(t.Context(), signal.UnlinkOptions{LocalOnly: true})
	if !errors.Is(err, signal.ErrAccountInUse) {
		t.Fatalf("got %v, want ErrAccountInUse", err)
	}

	accounts, err := dir.Accounts()
	if err != nil || len(accounts) != 1 {
		t.Errorf("account removed although it was in use: %+v, %v", accounts, err)
	}
}

// seedAccount seeds a data dir with the single account seededNumber / seededACI.
func seedAccount(t *testing.T) string {
	t.Helper()

	return seedAccounts(t, signal.Account{Number: seededNumber, ACI: seededACI, DeviceID: 2})
}

// seedAccounts writes the layout `link` would: accounts.json plus a logged-in device in each
// account database. It returns the data dir.
func seedAccounts(t *testing.T, accounts ...signal.Account) string {
	t.Helper()

	dataDir := t.TempDir()

	dir, err := store.OpenDir(dataDir, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}

	for _, acc := range accounts {
		putDevice(t, dir, acc)

		err = dir.PutAccount(store.AccountEntry{
			Number: acc.Number, ACI: acc.ACI, DeviceID: acc.DeviceID,
			DeviceName: acc.DeviceName, LinkedAt: acc.LinkedAt,
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	return dataDir
}

func putDevice(t *testing.T, dir *store.Dir, acc signal.Account) {
	t.Helper()

	data, err := dir.OpenAccount(t.Context(), acc.ACI, zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}

	defer data.Close()

	aciKeys, err := libsignalgo.GenerateIdentityKeyPair()
	if err != nil {
		t.Fatal(err)
	}

	pniKeys, err := libsignalgo.GenerateIdentityKeyPair()
	if err != nil {
		t.Fatal(err)
	}

	err = data.Devices.PutDevice(t.Context(), &mstore.DeviceData{
		ACIIdentityKeyPair: aciKeys,
		PNIIdentityKeyPair: pniKeys,
		ACI:                uuid.MustParse(acc.ACI),
		PNI:                uuid.New(),
		DeviceID:           acc.DeviceID,
		Number:             acc.Number,
		Password:           "secret",
	})
	if err != nil {
		t.Fatal(err)
	}
}
