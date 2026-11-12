//go:build linux && libsignal_go

package accountimport

import _ "modernc.org/sqlite" // Register the existing pure-Go driver.

const (
	sourceDriver        = "sqlite"
	sourceDriverOptions = "_pragma=foreign_keys(1)&_pragma=busy_timeout(1000)"
)
