package signal

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrUntrustedIdentity means that the identity key of a recipient changed (their safety number
// changed) and the new key hasn't been trusted yet, so go-signal doesn't send to them. See
// UntrustedError.
var ErrUntrustedIdentity = errors.New("identity key not trusted")

// ErrUnknownIdentity means that no identity key is stored for a user: go-signal learns it the
// first time it receives from or sends to them.
var ErrUnknownIdentity = errors.New("no identity key known")

// ErrSafetyNumberMismatch means that a safety number given to verify an identity key is not the
// one of the current key.
var ErrSafetyNumberMismatch = errors.New("safety number doesn't match")

// ErrInvalidSafetyNumber means that a safety number isn't 60 digits (spaces aside).
var ErrInvalidSafetyNumber = errors.New("invalid safety number (want 60 digits)")

// SafetyNumberDigits is the length of a safety number.
const SafetyNumberDigits = 60

// UntrustedError returns ErrUntrustedIdentity for rcpt, with what to do next.
func UntrustedError(rcpt Recipient) error {
	return fmt.Errorf("%w: safety number with %s changed; verify it with `go-signal identities show %s`, "+
		"then run `go-signal identities trust %s`", ErrUntrustedIdentity, rcpt, rcpt, rcpt)
}

// TrustLevel says whether go-signal sends to an identity key.
type TrustLevel int

// Trust levels. The first key seen for a user is trusted without verification (trust on first
// use); a changed key is untrusted until the user trusts it.
const (
	TrustUntrusted TrustLevel = iota + 1
	TrustUnverified
	TrustVerified
)

// String returns the level as the CLI and the JSON output name it.
func (l TrustLevel) String() string {
	switch l {
	case TrustUntrusted:
		return "untrusted"
	case TrustUnverified:
		return "trusted-unverified"
	case TrustVerified:
		return "trusted-verified"
	default:
		return fmt.Sprintf("TrustLevel(%d)", int(l))
	}
}

// Trusted reports whether messages may be sent to a key with this level.
func (l TrustLevel) Trusted() bool {
	return l == TrustUnverified || l == TrustVerified
}

// ParseTrustLevel is the inverse of TrustLevel.String; unknown names give 0.
func ParseTrustLevel(name string) TrustLevel {
	for _, level := range []TrustLevel{TrustUntrusted, TrustUnverified, TrustVerified} {
		if level.String() == name {
			return level
		}
	}

	return 0
}

// Identity is the identity key go-signal knows for another user, and whether it is trusted.
type Identity struct {
	Recipient Recipient
	// Fingerprint is the identity (public) key, hex encoded: 33 bytes, starting with the key type
	// 05.
	Fingerprint string
	Trust       TrustLevel
	// FirstSeen is when go-signal first stored a key of this user; zero if unknown (keys stored
	// before go-signal tracked them, or first seen as a change).
	FirstSeen time.Time
	// ChangedAt is when the key last changed; zero if it never did.
	ChangedAt time.Time
}

// SafetyNumber is what two users compare to verify each other's identity keys.
type SafetyNumber struct {
	Identity Identity
	// Number is the 60-digit safety number, without spaces. Both sides compute the same one.
	Number string
	// Scannable is the content of the QR code the Signal apps show and scan.
	Scannable []byte
}

// NormalizeSafetyNumber removes white space from s and checks that 60 digits remain
// (ErrInvalidSafetyNumber).
func NormalizeSafetyNumber(s string) (string, error) {
	digits := strings.Join(strings.Fields(s), "")

	if len(digits) != SafetyNumberDigits || strings.Trim(digits, "0123456789") != "" {
		return "", fmt.Errorf("%w: %q", ErrInvalidSafetyNumber, s)
	}

	return digits, nil
}

// GroupSafetyNumber splits a safety number into the blocks of five digits the Signal apps show.
func GroupSafetyNumber(number string) []string {
	const blockLen = 5

	blocks := make([]string, 0, len(number)/blockLen+1)
	for len(number) > blockLen {
		blocks = append(blocks, number[:blockLen])
		number = number[blockLen:]
	}

	if number != "" {
		blocks = append(blocks, number)
	}

	return blocks
}

// IdentityChanged reports that a user's identity key changed: they reinstalled Signal or
// re-registered, or someone is impersonating them. The new key is untrusted: sending to them
// fails with ErrUntrustedIdentity until the user trusts it (Client.TrustIdentity); receiving
// from them keeps working.
type IdentityChanged struct {
	Recipient Recipient
	// OldFingerprint is the key trusted before (hex, see Identity); empty if unknown. It equals
	// NewFingerprint when the key changed back to the one trusted before without the change being
	// trusted in between: that key has to be trusted again too.
	OldFingerprint string
	// NewFingerprint is the new, untrusted key.
	NewFingerprint string
	// Time is when go-signal noticed the change.
	Time time.Time
}

func (*IdentityChanged) isEvent() {}
