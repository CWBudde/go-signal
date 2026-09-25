//go:build cgo

package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"

	// Registers the "sqlite3" database/sql driver.
	_ "github.com/mattn/go-sqlite3"
	"github.com/rs/zerolog"
	"go.mau.fi/mautrix-signal/pkg/signalmeow/store"
	"go.mau.fi/util/dbutil"
)

// ourVersionTable tracks our migrations separately from signalmeow's.
const ourVersionTable = "gosignal_version"

//go:embed upgrades/*.sql
var upgradeFiles embed.FS

// Store is an open account database.
type Store struct {
	db  *dbutil.Database
	own *dbutil.Database

	// Devices holds the signalmeow device (account) and all its protocol state.
	Devices *store.Container
}

// OpenAccount opens (creating if needed) <data-dir>/<aci>/account.db and runs signalmeow's and
// our migrations. It doesn't take the account lock; see Lock.
func (d *Dir) OpenAccount(ctx context.Context, aci string, log zerolog.Logger) (*Store, error) {
	dir := d.AccountDir(aci)

	err := d.mkdir(dir)
	if err != nil {
		return nil, fmt.Errorf("create account dir: %w", err)
	}

	path := filepath.Join(dir, dbFile)

	err = d.touch(path)
	if err != nil {
		return nil, err
	}

	return openDB(ctx, path, log)
}

func openDB(ctx context.Context, path string, log zerolog.Logger) (*Store, error) {
	dsn := (&url.URL{
		Scheme:   "file",
		Opaque:   path,
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

		return nil, fmt.Errorf("migrate signalmeow tables: %w", err)
	}

	own := sqlDB.Child(ourVersionTable, upgradeTable(),
		dbutil.ZeroLogger(log.With().Str("db_section", "go-signal").Logger()))

	err = own.Upgrade(ctx)
	if err != nil {
		_ = sqlDB.Close()

		return nil, fmt.Errorf("migrate go-signal tables: %w", err)
	}

	return &Store{db: sqlDB, own: own, Devices: devices}, nil
}

func upgradeTable() dbutil.UpgradeTable {
	return dbutil.BuildUpgradeTable().WithFSPath(upgradeFiles, "upgrades").Finish()
}

// Meta returns the value stored under key, and whether there was one.
func (s *Store) Meta(ctx context.Context, key string) (string, bool, error) {
	var value string

	err := s.own.QueryRow(ctx, "SELECT value FROM gosignal_meta WHERE key=$1", key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}

	if err != nil {
		return "", false, fmt.Errorf("read meta %s: %w", key, err)
	}

	return value, true, nil
}

// SetMeta stores value under key.
func (s *Store) SetMeta(ctx context.Context, key, value string) error {
	_, err := s.own.Exec(ctx,
		"INSERT INTO gosignal_meta (key, value) VALUES ($1, $2) ON CONFLICT (key) DO UPDATE SET value=excluded.value",
		key, value)
	if err != nil {
		return fmt.Errorf("write meta %s: %w", key, err)
	}

	return nil
}

// Close closes the database.
func (s *Store) Close() error {
	err := s.db.Close()
	if err != nil {
		return fmt.Errorf("close database: %w", err)
	}

	return nil
}
