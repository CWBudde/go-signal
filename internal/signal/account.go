package signal

import (
	"errors"
	"time"

	"github.com/cwbudde/go-signal/internal/store"
)

// ErrCGORequired is returned by operations that need libsignal when built with CGO_ENABLED=0.
var ErrCGORequired = errors.New("this build has no libsignal support (built without cgo)")

// ErrNotLinked means the data dir holds no linked account.
var ErrNotLinked = errors.New("no linked account; run `go-signal link` first")

// ErrAccountNotFound means -a/--account matches none of the linked accounts.
var ErrAccountNotFound = errors.New("account not found")

// ErrLoggedOut means the server no longer accepts this device (e.g. it was unlinked on the phone).
var ErrLoggedOut = errors.New("device was logged out by the server")

// ErrAccountInUse means another go-signal process is connected as the same account.
var ErrAccountInUse = store.ErrAccountInUse

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
	// DeviceName is the name this device was linked with; empty if unknown.
	DeviceName string
	// LinkedAt is when this device was linked; zero if unknown.
	LinkedAt time.Time
}

// Device is one device of an account, as the server lists it.
type Device struct {
	ID int
	// Name is the decrypted device name; the primary device usually has none.
	Name string
	// Created is when the device was linked; zero if it couldn't be decrypted.
	Created time.Time
	// LastSeen is when the device last connected (the server only keeps the day).
	LastSeen time.Time
	// Current marks the device this client runs as.
	Current bool
}

// UnlinkOptions configures Client.Unlink.
type UnlinkOptions struct {
	// LocalOnly skips removing the device on the server and only deletes the local data.
	LocalOnly bool
}
