package signal

import (
	"errors"
	"time"
)

var (
	// ErrUnknownContact means that the store has no entry for a user: we have neither synced
	// them from the phone nor exchanged messages with them.
	ErrUnknownContact = errors.New("unknown contact")
	// ErrStorageKeyUnknown means that the storage service key is unknown, so the phone's current
	// blocked list can't be read; `account sync` asks the phone for the key.
	ErrStorageKeyUnknown = errors.New("the storage service key is unknown; run `go-signal account sync` first")
	// ErrBlockedListIncomplete means that the storage service didn't give the complete blocked
	// list (no manifest yet, records that couldn't be read, or blocked entries a blocked-list sync
	// message can't carry). The phone replaces its blocked list with the one we send, so a list
	// built from it would unblock users or groups there; nothing is sent.
	ErrBlockedListIncomplete = errors.New("the storage service's blocked list is incomplete")
)

// BlockOverrideTTL is how long a block or unblock made by SetBlocked is kept at most against a
// storage service that still says otherwise. The phone applies the blocked list we send it and
// then writes it to the storage service; until then, every storage sync would undo our local
// change. An override ends as soon as the storage service is seen at a later version than the
// one the change was made against: the phone has written it since, so its state wins (whether
// it applied our change or the user changed it again there). After BlockOverrideTTL, the storage
// service wins anyway (e.g. when the phone never processed our list).
const BlockOverrideTTL = 7 * 24 * time.Hour

// Contact is what the store knows about a user: the names from the phone's contacts, the storage
// service and their profile, and whether they are blocked.
type Contact struct {
	Recipient

	// ContactName is the name in the phone's address book.
	ContactName string
	// ProfileName is the name the user set in their Signal profile.
	ProfileName string
	// Nickname is the nickname we gave the user in Signal (it overrides the other names).
	Nickname string
	// Blocked reports whether we blocked the user: Signal drops their direct messages, typing
	// indicators and calls (group messages still arrive).
	Blocked bool
	// Accepted reports whether we accepted their message request (share our profile with them);
	// nil if unknown.
	Accepted *bool
}

// Name returns the best name of c, as the Signal apps pick it: the nickname, then the name in
// the phone's contacts, then the profile name. It is empty if c has no name.
func (c Contact) Name() string {
	switch {
	case c.Nickname != "":
		return c.Nickname
	case c.ContactName != "":
		return c.ContactName
	default:
		return c.ProfileName
	}
}

// DisplayName returns Name, or else the number, the ACI or the PNI.
func (c Contact) DisplayName() string {
	switch {
	case c.Name() != "":
		return c.Name()
	case c.Number != "":
		return c.Number
	default:
		return c.String()
	}
}
