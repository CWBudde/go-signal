//go:build cgo

package store_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cwbudde/go-signal/internal/store"
	"github.com/rs/zerolog"
)

func TestOpenMigrates(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "data")

	data, err := store.Open(t.Context(), dir, zerolog.Nop())
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	devices, err := data.Devices.GetAllDevices(t.Context())
	if err != nil {
		t.Fatalf("query devices after migration: %v", err)
	}

	if len(devices) != 0 {
		t.Errorf("fresh store has %d devices", len(devices))
	}

	err = data.Close()
	if err != nil {
		t.Fatalf("close: %v", err)
	}

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat data dir: %v", err)
	}

	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Errorf("data dir perm = %o, want 700", perm)
	}

	// Reopening an already migrated database must be a no-op.
	data, err = store.Open(t.Context(), dir, zerolog.Nop())
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}

	_ = data.Close()
}
