//go:build cgo && !purego

package store

import (
	"net/url"

	// Registers the "sqlite3" database/sql driver.
	_ "github.com/mattn/go-sqlite3"
)

// sqliteDriver is mattn/go-sqlite3 (cgo). dbutil derives the SQLite dialect from the name.
const sqliteDriver = "sqlite3"

// sqliteDSN enables foreign keys and WAL, waits up to 5 s for locks and starts write
// transactions immediately.
func sqliteDSN(path string) string {
	return (&url.URL{
		Scheme:   "file",
		Opaque:   path,
		RawQuery: "_foreign_keys=on&_journal_mode=WAL&_busy_timeout=5000&_txlock=immediate",
	}).String()
}
