// Package accountimport defines the offline account-import boundary. Its compatibility
// fixtures exercise protocol restoration; AccountOverlay and AccountGuards build an imported
// account's effective AccountRecord and its privacy guards. Import currently refuses
// without I/O until snapshot conversion and publication are implemented.
package accountimport
