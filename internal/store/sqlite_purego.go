//go:build purego

package store

import (
	"net/url"

	// Registers the "sqlite" database/sql driver.
	_ "modernc.org/sqlite"
)

// sqliteDriver is modernc.org/sqlite (pure Go). dbutil derives the SQLite dialect from the name.
const sqliteDriver = "sqlite"

// sqliteDSN sets the same options as the cgo driver's DSN, in modernc's pragma syntax.
func sqliteDSN(path string) string {
	return (&url.URL{
		Scheme: "file",
		Opaque: path,
		RawQuery: "_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)" +
			"&_txlock=immediate",
	}).String()
}
