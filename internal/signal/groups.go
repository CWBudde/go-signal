package signal

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

var (
	// ErrNotAMember means that the server doesn't let us see the group: we left it, were removed,
	// or only asked to join and weren't approved yet. Leaving a group we are not in fails with it
	// too.
	ErrNotAMember = errors.New("not a member of the group")
	// ErrLastAdmin means that we can't leave a group because we are its only admin and other
	// members remain: another member has to be promoted to admin first (LeaveOptions.Promote).
	ErrLastAdmin = errors.New("we are the only admin of the group; promote another member to admin first")
	// ErrInvalidPromotion means that LeaveOptions.Promote names someone who can't be promoted: not
	// another member of the group, or we are no admin ourselves.
	ErrInvalidPromotion = errors.New("invalid promotion")
)

// unknownRole is the name of GroupRoleUnknown.
const unknownRole = "unknown"

// GroupRole is the role of a group member.
type GroupRole int

// The roles of group members.
const (
	GroupRoleUnknown GroupRole = iota
	GroupRoleMember
	GroupRoleAdmin
)

// String returns "member", "admin" or "unknown".
func (r GroupRole) String() string {
	switch r {
	case GroupRoleMember:
		return "member"
	case GroupRoleAdmin:
		return "admin"
	case GroupRoleUnknown:
	}

	return unknownRole
}

// Membership is how a user belongs to a group.
type Membership int

// The kinds of membership.
const (
	// MembershipNone: not in the group (anymore), as far as the group's state shows.
	MembershipNone Membership = iota
	// MembershipMember: a full member.
	MembershipMember
	// MembershipPending: invited by a member, not accepted yet.
	MembershipPending
	// MembershipRequesting: asked to join through a group link, waiting for an admin's approval.
	MembershipRequesting
)

// String returns "none", "member", "pending" or "requesting".
func (m Membership) String() string {
	switch m {
	case MembershipMember:
		return "member"
	case MembershipPending:
		return "pending"
	case MembershipRequesting:
		return "requesting"
	case MembershipNone:
	}

	return "none"
}

// Group is a Signal group (groups v2) as the server reports it.
type Group struct {
	// ID is the base64 group identifier (standard encoding), which is derived from MasterKey.
	ID string
	// MasterKey is the base64 group master key. Whoever has it can read the group's state and
	// ask to join, so it is not printed.
	MasterKey   string
	Title       string
	Description string
	// Revision counts the changes to the group.
	Revision uint32
	// Timer is the disappearing messages timer; zero means off.
	Timer time.Duration
	// AnnouncementsOnly means that only admins can send messages.
	AnnouncementsOnly bool
	Members           []GroupMember
	// Pending are the invited users who haven't accepted yet. Users invited by phone number
	// (PNI) are missing: signalmeow can't decrypt them.
	Pending []PendingMember
	// Requesting are the users who asked to join through a group link.
	Requesting []RequestingMember

	// Membership and Role are ours (the selected account's). Role is the role we have as a
	// member, or will get once we accept an invitation.
	Membership Membership
	Role       GroupRole

	// LeftAt is when we left the group with go-signal, if we did and haven't rejoined since.
	LeftAt time.Time
	// Err is only set by Client.Groups, for a group whose state couldn't be fetched: ErrNotAMember
	// (see there) or ErrUnknownGroup (the server doesn't know it). Then only ID, the last known
	// Title and LeftAt are set, and Membership is MembershipNone.
	Err error
}

// GroupMember is a full member of a group.
type GroupMember struct {
	Recipient Recipient
	Role      GroupRole
	// JoinedAtRevision is the group revision that added the member.
	JoinedAtRevision uint32
}

// PendingMember is a user invited to a group who hasn't accepted yet.
type PendingMember struct {
	Recipient Recipient
	// Role is the role the user gets on accepting.
	Role GroupRole
	// AddedBy is the member who sent the invitation.
	AddedBy   Recipient
	InvitedAt time.Time
}

// RequestingMember is a user who asked to join a group and waits for an admin's approval.
type RequestingMember struct {
	Recipient   Recipient
	RequestedAt time.Time
}

// LeaveOptions configures Client.LeaveGroup.
type LeaveOptions struct {
	// Promote are members (with ACI) to make admins in the same group change; needed when we
	// are the only admin and other members remain. Only admins can promote.
	Promote []Recipient
}

// LeaveResult is what Client.LeaveGroup did.
type LeaveResult struct {
	// Group is the group as it was before we left; Group.Membership tells what we gave up (a
	// membership, an invitation or a join request). LeftAt is set.
	Group Group
	// Revision is the group's revision after the change.
	Revision uint32
	// Promoted are the members that were made admins (without those that already were).
	Promoted []Recipient
}

// MembershipOf returns how the user with the ACI aci belongs to g, and their role as a member (or
// the role an invitation offers).
func (g Group) MembershipOf(aci string) (Membership, GroupRole) {
	if aci == "" {
		return MembershipNone, GroupRoleUnknown
	}

	for _, member := range g.Members {
		if member.Recipient.ACI == aci {
			return MembershipMember, member.Role
		}
	}

	for _, pending := range g.Pending {
		if pending.Recipient.ACI == aci {
			return MembershipPending, pending.Role
		}
	}

	for _, requesting := range g.Requesting {
		if requesting.Recipient.ACI == aci {
			return MembershipRequesting, GroupRoleUnknown
		}
	}

	return MembershipNone, GroupRoleUnknown
}

// Admins returns the number of members who are admins.
func (g Group) Admins() int {
	admins := 0

	for _, member := range g.Members {
		if member.Role == GroupRoleAdmin {
			admins++
		}
	}

	return admins
}

// CheckLeave reports whether the user with the ACI self can leave g, promoting the members
// promote to admin in the same change. self must belong to g (as a member, invited or
// requesting; else ErrNotAMember). Only an admin member can promote, and only other members
// (ErrInvalidPromotion). An admin who is the only one while other members remain must promote
// someone (ErrLastAdmin), as the official clients and signal-cli require.
func (g Group) CheckLeave(self string, promote []Recipient) error {
	membership, role := g.MembershipOf(self)
	admin := membership == MembershipMember && role == GroupRoleAdmin

	switch {
	case membership == MembershipNone:
		return ErrNotAMember
	case len(promote) > 0 && !admin:
		return fmt.Errorf("%w: only admins can promote members", ErrInvalidPromotion)
	case membership != MembershipMember:
		// Declining an invitation or cancelling a join request leaves the members as they are.
		return nil
	}

	newAdmins, err := g.newAdmins(self, promote)
	if err != nil {
		return err
	}

	if admin && g.Admins()+newAdmins == 1 && len(g.Members) > 1 {
		return ErrLastAdmin
	}

	return nil
}

// newAdmins checks that promote are other members than self and returns how many of them
// aren't admins yet.
func (g Group) newAdmins(self string, promote []Recipient) (int, error) {
	promoted := make(map[string]bool, len(promote))

	for _, rcpt := range promote {
		i := slices.IndexFunc(g.Members, func(m GroupMember) bool { return rcpt.ACI != "" && m.Recipient.ACI == rcpt.ACI })
		if i < 0 || rcpt.ACI == self {
			return 0, fmt.Errorf("%w: %s is not another member of the group", ErrInvalidPromotion, rcpt)
		}

		if g.Members[i].Role != GroupRoleAdmin {
			promoted[rcpt.ACI] = true
		}
	}

	return len(promoted), nil
}

// SortGroups sorts groups by title (ignoring case), then by ID; groups without a title go last.
func SortGroups(groups []Group) {
	slices.SortStableFunc(groups, func(a, b Group) int {
		if (a.Title == "") != (b.Title == "") {
			if a.Title == "" {
				return 1
			}

			return -1
		}

		return cmp.Or(
			strings.Compare(strings.ToLower(a.Title), strings.ToLower(b.Title)),
			strings.Compare(a.ID, b.ID),
		)
	})
}
