//go:build linux && libsignal_go

package accountimport_test

import _ "modernc.org/sqlite" // Register the existing pure-Go SQLite driver.

const sourceTestDriver = "sqlite"
