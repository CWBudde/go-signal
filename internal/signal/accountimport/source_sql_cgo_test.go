//go:build linux && cgo && !libsignal_go

package accountimport_test

import _ "github.com/mattn/go-sqlite3" // Register the existing native SQLite driver.

const sourceTestDriver = "sqlite3"
