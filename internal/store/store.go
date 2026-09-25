//go:build cgo

package store

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	// Registers the "sqlite3" database/sql driver.
	_ "github.com/mattn/go-sqlite3"
	"github.com/rs/zerolog"
	"go.mau.fi/mautrix-signal/pkg/signalmeow/store"
	"go.mau.fi/util/dbutil"
)

// dbFile is the database name inside the data dir. Phase 2.2 moves it to <data-dir>/<aci>/.
const dbFile = "signal.db"

// dirPerm keeps keys and messages private to the user.
const dirPerm = 0o700

// Store is an open data dir.
type Store struct {
	db *dbutil.Database

	// Devices holds the signalmeow devices (accounts) and all their protocol state.
	Devices *store.Container
}

// Open creates dataDir if needed, opens the database in it and runs signalmeow's migrations.
func Open(ctx context.Context, dataDir string, log zerolog.Logger) (*Store, error) {
	err := os.MkdirAll(dataDir, dirPerm)
	if err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}

	dsn := (&url.URL{
		Scheme:   "file",
		Opaque:   filepath.Join(dataDir, dbFile),
		RawQuery: "_foreign_keys=on&_journal_mode=WAL&_busy_timeout=5000&_txlock=immediate",
	}).String()

	sqlDB, err := dbutil.NewWithDialect(dsn, "sqlite3")
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	devices := store.NewStore(sqlDB, dbutil.ZeroLogger(log.With().Str("db_section", "signalmeow").Logger()))

	err = devices.Upgrade(ctx)
	if err != nil {
		_ = sqlDB.Close()

		return nil, fmt.Errorf("migrate database: %w", err)
	}

	return &Store{db: sqlDB, Devices: devices}, nil
}

// Close closes the database.
func (s *Store) Close() error {
	err := s.db.Close()
	if err != nil {
		return fmt.Errorf("close database: %w", err)
	}

	return nil
}
