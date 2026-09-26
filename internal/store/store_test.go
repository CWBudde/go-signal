//go:build cgo || purego

package store_test

import (
	"io"
	"path/filepath"
	"testing"

	"github.com/cwbudde/go-signal/internal/store"
	"github.com/rs/zerolog"
)

func TestOpenAccount(t *testing.T) {
	t.Parallel()

	dir := openDir(t, io.Discard)
	data := openAccount(t, dir)

	if got := perm(t, filepath.Join(dir.AccountDir(testACI), "account.db")); got != 0o600 {
		t.Errorf("account.db perm = %o, want 600", got)
	}

	devices, err := data.Devices.GetAllDevices(t.Context())
	if err != nil || len(devices) != 0 {
		t.Fatalf("fresh store: %d devices, %v", len(devices), err)
	}
}

func TestMetaSurvivesReopen(t *testing.T) {
	t.Parallel()

	dir := openDir(t, io.Discard)
	data := openAccount(t, dir)

	for _, value := range []string{"v1", "v2"} {
		err := data.SetMeta(t.Context(), "k", value)
		if err != nil {
			t.Fatalf("set meta: %v", err)
		}
	}

	err := data.Close()
	if err != nil {
		t.Fatalf("close: %v", err)
	}

	// Reopening an already migrated database must be a no-op and keep our tables.
	data = openAccount(t, dir)

	value, ok, err := data.Meta(t.Context(), "k")
	if err != nil || !ok || value != "v2" {
		t.Errorf("meta = %q, %v, %v", value, ok, err)
	}

	_, ok, err = data.Meta(t.Context(), "missing")
	if err != nil || ok {
		t.Errorf("missing meta = %v, %v", ok, err)
	}
}

// openAccount opens the testACI database and closes it at the end of the test.
// Both SQLite drivers (mattn in the cgo build, modernc in the purego build) must open the
// database with the same options.
func TestConnectionPragmas(t *testing.T) {
	t.Parallel()

	data := openAccount(t, openDir(t, io.Discard))

	for pragma, want := range map[string]string{
		"foreign_keys": "1",
		"journal_mode": "wal",
		"busy_timeout": "5000",
	} {
		got, err := data.Pragma(t.Context(), pragma)
		if err != nil || got != want {
			t.Errorf("%s = %q, %v; want %q", pragma, got, err, want)
		}
	}
}

func openAccount(t *testing.T, dir *store.Dir) *store.Store {
	t.Helper()

	data, err := dir.OpenAccount(t.Context(), testACI, zerolog.Nop())
	if err != nil {
		t.Fatalf("open account: %v", err)
	}

	t.Cleanup(func() { _ = data.Close() })

	return data
}
