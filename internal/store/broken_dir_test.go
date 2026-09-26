package store_test

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cwbudde/go-signal/internal/store"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()

	err := os.WriteFile(path, []byte(content), 0o600)
	if err != nil {
		t.Fatal(err)
	}
}

func TestOpenDirOnFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "data")
	writeFile(t, path, "")

	_, err := store.OpenDir(path, slog.New(slog.DiscardHandler))
	if err == nil {
		t.Error("opened a file as data dir")
	}
}

func TestCorruptAccountsFile(t *testing.T) {
	t.Parallel()

	dir := openDir(t, io.Discard)
	writeFile(t, filepath.Join(dir.Path(), "accounts.json"), "{not json")

	_, err := dir.Accounts()
	if err == nil || !strings.Contains(err.Error(), "parse accounts.json") {
		t.Errorf("Accounts: %v, want a parse error", err)
	}

	// Changes refuse to overwrite what they can't read.
	err = dir.PutAccount(store.AccountEntry{Number: "+15550100", ACI: testACI})
	if err == nil {
		t.Error("PutAccount replaced a corrupt accounts.json")
	}

	err = dir.RemoveAccount(testACI)
	if err == nil {
		t.Error("RemoveAccount replaced a corrupt accounts.json")
	}

	raw, err := os.ReadFile(filepath.Join(dir.Path(), "accounts.json"))
	if err != nil || string(raw) != "{not json" {
		t.Errorf("accounts.json = %q, %v; want it untouched", raw, err)
	}
}

func TestUnreadableAccountsFile(t *testing.T) {
	t.Parallel()

	dir := openDir(t, io.Discard)

	err := os.Mkdir(filepath.Join(dir.Path(), "accounts.json"), 0o700)
	if err != nil {
		t.Fatal(err)
	}

	_, err = dir.Accounts()
	if err == nil || !strings.Contains(err.Error(), "read accounts.json") {
		t.Errorf("Accounts: %v, want a read error", err)
	}
}

func TestAccountsFileNotWritable(t *testing.T) {
	t.Parallel()

	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}

	dir := openDir(t, io.Discard)

	err := os.Chmod(dir.Path(), 0o500)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = os.Chmod(dir.Path(), 0o700) })

	err = dir.PutAccount(store.AccountEntry{Number: "+15550100", ACI: testACI})
	if err == nil || !strings.Contains(err.Error(), "write accounts.json") {
		t.Errorf("PutAccount: %v, want a write error", err)
	}
}

func TestLockBrokenAccountDir(t *testing.T) {
	t.Parallel()

	dir := openDir(t, io.Discard)

	// A file where the account directory belongs.
	writeFile(t, dir.AccountDir(testACI), "")

	_, err := dir.Lock(testACI)
	if err == nil || !strings.Contains(err.Error(), "create account dir") {
		t.Errorf("Lock: %v, want a mkdir error", err)
	}

	// A directory where the lock file belongs.
	const other = "22222222-2222-2222-2222-222222222222"

	err = os.MkdirAll(filepath.Join(dir.AccountDir(other), "lock"), 0o700)
	if err != nil {
		t.Fatal(err)
	}

	_, err = dir.Lock(other)
	if err == nil || !strings.Contains(err.Error(), "open lock file") {
		t.Errorf("Lock: %v, want an open error", err)
	}
}

func TestLockReleasedTwice(t *testing.T) {
	t.Parallel()

	lock, err := openDir(t, io.Discard).Lock(testACI)
	if err != nil {
		t.Fatal(err)
	}

	err = lock.Unlock()
	if err != nil {
		t.Fatalf("unlock: %v", err)
	}

	err = lock.Unlock()
	if err == nil {
		t.Error("second unlock succeeded")
	}
}
