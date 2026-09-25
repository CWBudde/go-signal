package signal

import (
	"errors"
	"fmt"
	"time"

	"github.com/cwbudde/go-signal/internal/store"
)

// ErrCGORequired is returned by operations that need libsignal when built with CGO_ENABLED=0.
var ErrCGORequired = errors.New("this build has no libsignal support (built without cgo)")

// ErrNotLinked means the data dir holds no linked account.
var ErrNotLinked = errors.New("no linked account; run `go-signal link` first")

// ErrAccountNotFound means -a/--account matches none of the linked accounts.
var ErrAccountNotFound = errors.New("account not found")

// ErrDeviceUnlinked means the server no longer accepts this device: it was unlinked (e.g. on the
// phone) or its credentials are no longer valid. The account is then marked as unlinked in
// accounts.json, and later commands fail with it right away (see UnlinkedError).
var ErrDeviceUnlinked = errors.New("this device was unlinked from the account")

// ErrAccountInUse means another go-signal process is connected as the same account.
var ErrAccountInUse = store.ErrAccountInUse

// ErrNotConnected is returned by operations that need Connect first.
var ErrNotConnected = errors.New("client is not connected")

// ErrClosed is returned by operations started after Close.
var ErrClosed = errors.New("client is closed")

// ErrNotOnSignal means that a phone number or username belongs to no (discoverable) Signal
// account.
var ErrNotOnSignal = errors.New("not on Signal")

// ErrUnresolvable means that a Recipient has no identifier that can be resolved to an ACI.
var ErrUnresolvable = errors.New("recipient has no number, username or ACI")

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
	// UnlinkedAt is when go-signal found out that the device was unlinked; zero while linked.
	UnlinkedAt time.Time
}

// Unlinked reports whether the account is marked as unlinked.
func (a Account) Unlinked() bool {
	return !a.UnlinkedAt.IsZero()
}

// UnlinkedError returns ErrDeviceUnlinked for acc, with what to do next.
func UnlinkedError(acc Account) error {
	return fmt.Errorf("%w %s; run `go-signal -a %s account unlink --yes --local-only` to delete its local data, "+
		"then `go-signal link` to link it again", ErrDeviceUnlinked, acc.Number, acc.Number)
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
