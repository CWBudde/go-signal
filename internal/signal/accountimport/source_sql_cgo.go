//go:build linux && cgo && !libsignal_go

package accountimport

import _ "github.com/mattn/go-sqlite3" // Register the existing native driver.

const (
	sourceDriver        = "sqlite3"
	sourceDriverOptions = "_foreign_keys=on&_busy_timeout=1000"
)
