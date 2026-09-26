package store_test

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/internal/store"
)

const (
	testACI   = "11111111-1111-1111-1111-111111111111"
	secondACI = "33333333-3333-3333-3333-333333333333"
)

func openDir(t *testing.T, logs io.Writer) *store.Dir {
	t.Helper()

	dir, err := store.OpenDir(filepath.Join(t.TempDir(), "data"), slog.New(slog.NewTextHandler(logs, nil)))
	if err != nil {
		t.Fatalf("open dir: %v", err)
	}

	return dir
}

func perm(t *testing.T, path string) os.FileMode {
	t.Helper()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	return info.Mode().Perm()
}

func TestOpenDirCreatesPrivateDir(t *testing.T) {
	t.Parallel()

	var logs bytes.Buffer

	dir := openDir(t, &logs)

	if got := perm(t, dir.Path()); got != 0o700 {
		t.Errorf("data dir perm = %o, want 700", got)
	}

	if logs.Len() != 0 {
		t.Errorf("unexpected warnings: %s", logs.String())
	}
}

func TestOpenDirWarnsAboutLoosePermissions(t *testing.T) {
	t.Parallel()

	path := t.TempDir()

	err := os.Chmod(path, 0o755)
	if err != nil {
		t.Fatal(err)
	}

	var logs bytes.Buffer

	_, err = store.OpenDir(path, slog.New(slog.NewTextHandler(&logs, nil)))
	if err != nil {
		t.Fatalf("loose permissions must not fail: %v", err)
	}

	if !strings.Contains(logs.String(), "level=WARN") || !strings.Contains(logs.String(), "mode=755") {
		t.Errorf("missing warning: %q", logs.String())
	}
}

func TestOpenDirResolvesRelativePath(t *testing.T) {
	t.Parallel()

	dir, err := store.OpenDir(t.TempDir()+"/./x/..", slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}

	if !filepath.IsAbs(dir.Path()) || strings.Contains(dir.Path(), "..") {
		t.Errorf("path not cleaned: %s", dir.Path())
	}
}

func TestAttachmentsDir(t *testing.T) {
	t.Parallel()

	base := t.TempDir()

	got, err := store.AttachmentsDir(base+"/./x/..", testACI)
	if err != nil || got != filepath.Join(base, testACI, "attachments") {
		t.Errorf("AttachmentsDir = %q, %v", got, err)
	}

	_, err = os.Stat(filepath.Join(base, testACI))
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("AttachmentsDir created something: %v", err)
	}
}

func TestAccountsRoundTrip(t *testing.T) {
	t.Parallel()

	dir := openDir(t, io.Discard)

	accounts, err := dir.Accounts()
	if err != nil || len(accounts) != 0 {
		t.Fatalf("fresh dir: %v, %v", accounts, err)
	}

	first := store.AccountEntry{Number: "+15550100", ACI: testACI, DeviceID: 2, LinkedAt: time.Unix(1, 0).UTC()}
	second := store.AccountEntry{Number: "+15550101", ACI: secondACI, DeviceID: 3}

	putAccount(t, dir, first)
	putAccount(t, dir, second)

	// Relinking the first account replaces its entry in place.
	first.DeviceID = 4
	putAccount(t, dir, first)

	accounts, err = dir.Accounts()
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	if len(accounts) != 2 || accounts[0] != first || accounts[1] != second {
		t.Errorf("got %+v", accounts)
	}

	if got := perm(t, filepath.Join(dir.Path(), "accounts.json")); got != 0o600 {
		t.Errorf("accounts.json perm = %o, want 600", got)
	}
}

func putAccount(t *testing.T, dir *store.Dir, entry store.AccountEntry) {
	t.Helper()

	err := dir.PutAccount(entry)
	if err != nil {
		t.Fatalf("put %s: %v", entry.Number, err)
	}
}

func TestAccountsRejectsNewerVersion(t *testing.T) {
	t.Parallel()

	dir := openDir(t, io.Discard)

	err := os.WriteFile(filepath.Join(dir.Path(), "accounts.json"), []byte(`{"version":99}`), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	_, err = dir.Accounts()
	if !errors.Is(err, store.ErrAccountsVersion) {
		t.Fatalf("got %v, want ErrAccountsVersion", err)
	}
}

func TestLockIsExclusive(t *testing.T) {
	t.Parallel()

	dir := openDir(t, io.Discard)

	lock, err := dir.Lock(testACI)
	if err != nil {
		t.Fatalf("first lock: %v", err)
	}

	_, err = dir.Lock(testACI)
	if !errors.Is(err, store.ErrAccountInUse) {
		t.Fatalf("second lock: got %v, want ErrAccountInUse", err)
	}

	if !strings.Contains(err.Error(), "pid ") {
		t.Errorf("error does not name the holder: %v", err)
	}

	if got := perm(t, dir.AccountDir(testACI)); got != 0o700 {
		t.Errorf("account dir perm = %o, want 700", got)
	}

	err = lock.Unlock()
	if err != nil {
		t.Fatalf("unlock: %v", err)
	}

	lock, err = dir.Lock(testACI)
	if err != nil {
		t.Fatalf("lock after unlock: %v", err)
	}

	_ = lock.Unlock()
}

func TestRemoveAccount(t *testing.T) {
	t.Parallel()

	dir := openDir(t, io.Discard)

	first := store.AccountEntry{Number: "+15550100", ACI: testACI, DeviceID: 2, DeviceName: "laptop"}
	second := store.AccountEntry{Number: "+15550101", ACI: secondACI, DeviceID: 3}

	putAccount(t, dir, first)
	putAccount(t, dir, second)

	lock, err := dir.Lock(testACI)
	if err != nil {
		t.Fatal(err)
	}

	err = dir.RemoveAccount(testACI)
	if err != nil {
		t.Fatalf("remove: %v", err)
	}

	err = lock.Unlock()
	if err != nil {
		t.Errorf("unlock after remove: %v", err)
	}

	accounts, err := dir.Accounts()
	if err != nil || len(accounts) != 1 || accounts[0] != second {
		t.Errorf("accounts after remove: %+v, %v", accounts, err)
	}

	_, err = os.Stat(dir.AccountDir(testACI))
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("account dir still exists: %v", err)
	}

	// Removing it again is a no-op.
	err = dir.RemoveAccount(testACI)
	if err != nil {
		t.Errorf("second remove: %v", err)
	}
}

func TestMarkUnlinked(t *testing.T) {
	t.Parallel()

	dir := openDir(t, io.Discard)

	first := store.AccountEntry{Number: "+15550100", ACI: testACI, DeviceID: 2}
	second := store.AccountEntry{Number: "+15550101", ACI: secondACI, DeviceID: 3}

	putAccount(t, dir, first)
	putAccount(t, dir, second)

	unlinkedAt := time.Date(2026, 9, 25, 8, 0, 0, 0, time.UTC)
	markUnlinked(t, dir, testACI, unlinkedAt)
	// A later detection keeps the first mark, and an unknown ACI is ignored.
	markUnlinked(t, dir, testACI, unlinkedAt.Add(time.Hour))
	markUnlinked(t, dir, "44444444-4444-4444-4444-444444444444", unlinkedAt)

	accounts, err := dir.Accounts()
	if err != nil || len(accounts) != 2 || !accounts[0].UnlinkedAt.Equal(unlinkedAt) || accounts[1] != second {
		t.Fatalf("after mark: %+v, %v", accounts, err)
	}

	// Relinking the account replaces its entry and clears the mark.
	putAccount(t, dir, first)

	accounts, err = dir.Accounts()
	if err != nil || accounts[0] != first {
		t.Errorf("after relink: %+v, %v", accounts, err)
	}
}

func markUnlinked(t *testing.T, dir *store.Dir, aci string, unlinkedAt time.Time) {
	t.Helper()

	err := dir.MarkUnlinked(aci, unlinkedAt)
	if err != nil {
		t.Fatalf("mark %s: %v", aci, err)
	}
}
