package signal

import "errors"

// ErrCGORequired is returned by operations that need libsignal when built with CGO_ENABLED=0.
var ErrCGORequired = errors.New("this build has no libsignal support (built without cgo)")

// ErrNotLinked means the data dir holds no linked account.
var ErrNotLinked = errors.New("no linked account; run `go-signal link` first")

// ErrAccountNotFound means -a/--account matches none of the linked accounts.
var ErrAccountNotFound = errors.New("account not found")

// ErrLoggedOut means the server no longer accepts this device (e.g. it was unlinked on the phone).
var ErrLoggedOut = errors.New("device was logged out by the server")

// ErrNotConnected is returned by operations that need Connect first.
var ErrNotConnected = errors.New("client is not connected")

// ErrNotImplemented marks facade operations that a later phase fills in.
var ErrNotImplemented = errors.New("not implemented yet")

// Account identifies a linked account on this device.
type Account struct {
	Number   string
	ACI      string
	PNI      string
	DeviceID int
}
