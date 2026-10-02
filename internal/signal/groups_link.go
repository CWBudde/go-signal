package signal

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/cwbudde/mautrix-signal/pkg/signalmeow/protobuf/signalpb"
	"google.golang.org/protobuf/proto"
)

// GroupLinkState is a group's invite-link access policy.
type GroupLinkState string

const (
	GroupLinkDisabled GroupLinkState = "disabled"
	GroupLinkEnabled  GroupLinkState = "enabled"
	GroupLinkApproval GroupLinkState = "enabled-with-approval"
	// GroupLinkUnknown is an unrecognized server policy and never exposes a URL.
	GroupLinkUnknown GroupLinkState = "unknown"
)

// GroupLink is the dedicated result containing an enabled invite URL.
type GroupLink struct {
	ID       string
	Revision uint32
	State    GroupLinkState
	URL      string
}

// GroupLinkUpdate changes access, rotates the password, or both.
type GroupLinkUpdate struct {
	State *GroupLinkState
	Reset bool
}

const (
	groupLinkKeySize      = 32
	groupLinkPasswordSize = 16
)

// ErrInvalidGroupLinkUpdate means no operation or an unsupported access state was supplied.
var ErrInvalidGroupLinkUpdate = errors.New("invalid group link update")

// Check validates the requested operation without exposing its input.
func (u GroupLinkUpdate) Check() error {
	if u.State == nil && !u.Reset {
		return fmt.Errorf("%w: provide a state or reset", ErrInvalidGroupLinkUpdate)
	}

	if u.State != nil && *u.State != GroupLinkDisabled && *u.State != GroupLinkEnabled && *u.State != GroupLinkApproval {
		return fmt.Errorf("%w: state must be disabled, enabled or enabled-with-approval", ErrInvalidGroupLinkUpdate)
	}

	return nil
}

// Link returns the dedicated invite-link view after validating full membership.
func (g Group) Link(self string, state GroupLinkState, password string) (GroupLink, error) {
	err := g.checkGroupLink(self, false)
	if err != nil {
		return GroupLink{}, err
	}

	link := GroupLink{ID: g.ID, Revision: g.Revision, State: state}
	if state != GroupLinkEnabled && state != GroupLinkApproval {
		if state != GroupLinkDisabled {
			link.State = GroupLinkUnknown
		}

		return link, nil
	}

	key, err := decodeGroupLinkSecret(g.MasterKey, groupLinkKeySize, "master key")
	if err != nil {
		return GroupLink{}, err
	}

	secret, err := decodeGroupLinkSecret(password, groupLinkPasswordSize, "invite password")
	if err != nil {
		return GroupLink{}, err
	}

	message := &signalpb.GroupInviteLink{Contents: &signalpb.GroupInviteLink_ContentsV1{
		ContentsV1: &signalpb.GroupInviteLink_GroupInviteLinkContentsV1{GroupMasterKey: key, InviteLinkPassword: secret},
	}}

	data, err := proto.Marshal(message)
	if err != nil {
		return GroupLink{}, fmt.Errorf("%w: cannot encode invite link", ErrUnknownGroup)
	}

	link.URL = "https://signal.group/#" + base64.RawURLEncoding.EncodeToString(data)

	return link, nil
}

// WithLinkUpdate prepares a validated administrator update without modifying the group.
// The returned password is backend state, never part of a generic group result.
func (g Group) WithLinkUpdate(self string, state GroupLinkState, password string, update GroupLinkUpdate,
) (GroupLink, string, error) {
	err := update.Check()
	if err != nil {
		return GroupLink{}, "", err
	}

	err = g.checkGroupLink(self, true)
	if err != nil {
		return GroupLink{}, "", err
	}

	_, err = decodeGroupLinkSecret(g.MasterKey, groupLinkKeySize, "master key")
	if err != nil {
		return GroupLink{}, "", err
	}

	nextState := state
	if update.State != nil {
		nextState = *update.State
	}

	nextPassword, err := updatedGroupLinkPassword(nextState, password, update.Reset)
	if err != nil {
		return GroupLink{}, "", err
	}

	link, err := g.Link(self, nextState, nextPassword)
	if err != nil {
		return GroupLink{}, "", err
	}

	if nextState != state || nextPassword != password {
		if g.Revision == math.MaxUint32 {
			return GroupLink{}, "", fmt.Errorf("%w: group revision cannot be incremented", ErrUnknownGroup)
		}

		link.Revision++
	}

	return link, nextPassword, nil
}

func (g Group) checkGroupLink(self string, write bool) error {
	membership, role := g.MembershipOf(self)
	if membership != MembershipMember {
		return ErrNotAMember
	}

	if write && role != GroupRoleAdmin {
		return fmt.Errorf("%w: only administrators can change group links", ErrGroupPermission)
	}

	return nil
}

func decodeGroupLinkSecret(value string, size int, kind string) ([]byte, error) {
	decoded, err := base64.StdEncoding.DecodeString(value)
	if err != nil || len(decoded) != size {
		return nil, fmt.Errorf("%w: invalid group link %s", ErrUnknownGroup, kind)
	}

	return decoded, nil
}

type groupLinkReferenceError struct{ cause error }

func (e groupLinkReferenceError) Error() string {
	return "group link: invalid or unknown group reference"
}
func (e groupLinkReferenceError) Unwrap() error { return e.cause }

// GroupLinkReferenceError hides references that may contain a secret master key while
// preserving error identity for callers. It must only wrap non-nil resolution errors.
func GroupLinkReferenceError(err error) error { return groupLinkReferenceError{cause: err} }

func newGroupLinkPassword() (string, error) {
	secret := make([]byte, groupLinkPasswordSize)

	_, err := rand.Read(secret)
	if err != nil {
		return "", fmt.Errorf("generate group link password: %w", err)
	}

	return base64.StdEncoding.EncodeToString(secret), nil
}

func updatedGroupLinkPassword(state GroupLinkState, password string, reset bool) (string, error) {
	if reset || ((state == GroupLinkEnabled || state == GroupLinkApproval) && password == "") {
		return newGroupLinkPassword()
	}

	return password, nil
}

// groupLinkOperationError preserves error identity while directing users to the dedicated
// invite-link view rather than a generic group view that deliberately omits link state.
type groupLinkOperationError struct{ cause error }

func (e groupLinkOperationError) Error() string {
	return strings.ReplaceAll(e.cause.Error(), "inspect groups show", "inspect groups link show")
}
func (e groupLinkOperationError) Unwrap() error { return e.cause }
