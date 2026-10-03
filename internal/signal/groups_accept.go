package signal

import (
	"encoding/base64"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/google/uuid"
)

// GroupAcceptResult separates HTTP acceptance from fresh own ACI membership.
// ID is the canonical group identifier, never an unresolved reference or master key.
type GroupAcceptResult struct {
	ID       string
	Title    string
	Revision uint32
	Changed  bool
	Accepted bool
	Verified bool
}

const groupAcceptReferenceLimit = 4096

// ErrGroupInvitationNotFound means no invitation matches the selected account's typed identities.
var ErrGroupInvitationNotFound = errors.New("no group invitation for this account")

// CheckGroupAcceptReference validates sensitive input before opening an account.
func CheckGroupAcceptReference(ref string) error {
	if len(ref) > groupAcceptReferenceLimit {
		return ErrUnknownGroup
	}

	ref = strings.TrimSpace(ref)

	if ref == "" {
		return ErrUnknownGroup
	}

	if forbiddenGroupAcceptURL(ref) {
		return ErrUnknownGroup
	}

	if strings.HasPrefix(ref, "group:") && !acceptGroupID(strings.TrimPrefix(ref, "group:")) {
		return ErrUnknownGroup
	}

	return nil
}

// forbiddenGroupAcceptURL recognizes the scheme/authority independently of path and
// fragment parsing, so malformed escapes cannot turn an invite URL into a title.
func forbiddenGroupAcceptURL(ref string) bool {
	scheme, remainder, hasScheme := strings.Cut(ref, ":")
	if !hasScheme {
		return false
	}

	if strings.EqualFold(scheme, "sgnl") {
		return true
	}

	if !strings.EqualFold(scheme, "https") || !strings.HasPrefix(remainder, "//") {
		return false
	}

	authority := strings.TrimPrefix(remainder, "//")
	if boundary := strings.IndexAny(authority, "/?#"); boundary >= 0 {
		authority = authority[:boundary]
	}

	if userinfo := strings.LastIndexByte(authority, '@'); userinfo >= 0 {
		authority = authority[userinfo+1:]
	}

	host, _, _ := strings.Cut(authority, ":")

	return strings.EqualFold(strings.Trim(host, "[]"), "signal.group")
}

// NormalizeGroupAcceptReference canonicalizes already checked group references for
// selected-account resolution, preserving ordinary title and legacy fixture aliases.
func NormalizeGroupAcceptReference(ref string) string {
	ref = strings.TrimPrefix(strings.TrimSpace(ref), "group:")

	raw := acceptReferenceBytes(ref)
	if raw != nil {
		return base64.StdEncoding.EncodeToString(raw)
	}

	return ref
}

func acceptGroupID(value string) bool { return acceptReferenceBytes(value) != nil }

func acceptReferenceBytes(value string) []byte {
	for _, encoding := range []*base64.Encoding{
		base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding,
	} {
		raw, err := encoding.DecodeString(value)
		if err == nil && len(raw) == 32 {
			return raw
		}
	}

	return nil
}

func acceptIdentity(value string) uuid.UUID {
	parsed, err := uuid.Parse(value)
	if err != nil {
		return uuid.Nil
	}

	return parsed
}

func acceptSelfMatches(recipient, self Recipient) bool {
	if recipient.ACI != "" && recipient.PNI == "" {
		id := acceptIdentity(self.ACI)

		return id != uuid.Nil && acceptIdentity(recipient.ACI) == id
	}

	if recipient.PNI != "" && recipient.ACI == "" {
		id := acceptIdentity(self.PNI)

		return id != uuid.Nil && acceptIdentity(recipient.PNI) == id
	}

	return false
}

// CheckAcceptInvitation selects own ACI before own PNI without widening MembershipOf.
// A fresh full ACI membership is a no-op even when stale invitations remain.
//
//nolint:cyclop // Typed ownership and contradictory-state guards remain explicit.
func (g Group) CheckAcceptInvitation(self Recipient) (Recipient, bool, error) {
	ownACI := acceptIdentity(self.ACI)

	if ownACI == uuid.Nil {
		return Recipient{}, false, errInvalidGroupChangeResponse
	}

	for _, member := range g.Members {
		if acceptIdentity(member.Recipient.ACI) == ownACI {
			return Recipient{}, true, nil
		}
	}

	invited, err := g.pendingAcceptInvitation(self)
	if err != nil {
		return Recipient{}, false, err
	}

	for _, banned := range g.Banned {
		if acceptSelfMatches(banned.Recipient, self) {
			return Recipient{}, false, errInvalidGroupChangeResponse
		}
	}

	if invited.IsZero() {
		return Recipient{}, false, ErrGroupInvitationNotFound
	}

	for _, request := range g.Requesting {
		if acceptSelfMatches(request.Recipient, self) {
			return Recipient{}, false, errInvalidGroupChangeResponse
		}
	}

	if g.Revision == math.MaxUint32 {
		return Recipient{}, false, errInvalidGroupChangeResponse
	}

	return invited, false, nil
}

func (g Group) pendingAcceptInvitation(self Recipient) (Recipient, error) {
	var aci, pni Recipient

	var aciCount, pniCount int

	for _, pending := range g.Pending {
		if !acceptSelfMatches(pending.Recipient, self) {
			continue
		}

		if pending.Role != GroupRoleMember && pending.Role != GroupRoleAdmin {
			return Recipient{}, errInvalidGroupChangeResponse
		}

		if pending.Recipient.ACI != "" {
			aci = Recipient{ACI: acceptIdentity(self.ACI).String()}

			aciCount++
		} else {
			pni = Recipient{PNI: acceptIdentity(self.PNI).String()}

			pniCount++
		}
	}

	if aciCount > 1 || pniCount > 1 {
		return Recipient{}, errInvalidGroupChangeResponse
	}

	if aciCount == 1 {
		return aci, nil
	}

	return pni, nil
}

type groupAcceptOperationError struct {
	message string
	cause   error
}

func (e groupAcceptOperationError) Error() string { return e.message }
func (e groupAcceptOperationError) Unwrap() error { return e.cause }

// GroupAcceptOperationError preserves causes without exposing arbitrary nested error text.
// Its partial result must contain only an already validated canonical group ID.
func GroupAcceptOperationError(err error, result GroupAcceptResult) error {
	message := "group invitation acceptance failed"

	identity := ""

	if acceptGroupID(result.ID) {
		identity = fmt.Sprintf("; group %s revision %d", result.ID, result.Revision)
	}

	const inspect = "; inspect groups show or ask your phone or an administrator before retrying"

	switch {
	case result.Accepted:
		message = "invitation accepted" + identity + "; follow-up failed" + inspect

	case errors.Is(err, ErrGroupUpdateUncertain):
		message = "group invitation acceptance is uncertain"
		if identity != "" {
			message = fmt.Sprintf("acceptance of group %s is uncertain at attempted revision %d", result.ID, result.Revision)
		}

		message += inspect

	case errors.Is(err, ErrGroupInvitationNotFound):
		message = ErrGroupInvitationNotFound.Error()

	case errors.Is(err, ErrGroupTerminated):
		message = ErrGroupTerminated.Error()

	case errors.Is(err, ErrGroupChanged):
		message = "group changed before accepting; inspect groups show before retrying"

	case errors.Is(err, ErrUnknownGroup):
		message = "group is not known to this account"
	}

	return groupAcceptOperationError{message: message, cause: err}
}
