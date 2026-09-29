package signal

import (
	"errors"
	"fmt"
	"slices"

	"github.com/google/uuid"
)

// ErrInvalidGroupMember means a membership change names no members or an invalid target.
// For removal, use LeaveGroup to leave or decline our own invitation.
var ErrInvalidGroupMember = errors.New("invalid group member")

// NormalizeGroupRemovalMembers validates nonempty resolved removal targets, canonicalizes
// their ACIs and removes duplicates in input order without changing the input.
func NormalizeGroupRemovalMembers(members []Recipient) ([]Recipient, error) {
	return normalizeGroupMembers(members)
}

func normalizeGroupMembers(members []Recipient) ([]Recipient, error) {
	if len(members) == 0 {
		return nil, fmt.Errorf("%w: provide at least one member", ErrInvalidGroupMember)
	}

	seen := make(map[uuid.UUID]bool, len(members))
	out := make([]Recipient, 0, len(members))

	for _, member := range members {
		aci, err := uuid.Parse(member.ACI)
		if err != nil || aci == uuid.Nil {
			return nil, fmt.Errorf("%w: group member %q needs a nonzero ACI", ErrUnresolvable, member.ACI)
		}

		if !seen[aci] {
			seen[aci] = true
			member.ACI = aci.String()
			out = append(out, member)
		}
	}

	return out, nil
}

// WithRemovedMembers validates an administrator's removal and returns a copy with all
// targets removed, preserving the revision. It changes no server or cached state. Full,
// invited and requesting members can be removed; none are banned.
func (g Group) WithRemovedMembers(self string, members []Recipient) (Group, error) {
	members, err := NormalizeGroupRemovalMembers(members)
	if err != nil {
		return Group{}, err
	}

	membership, role := g.MembershipOf(self)
	if membership != MembershipMember {
		return Group{}, ErrNotAMember
	}

	if role != GroupRoleAdmin {
		return Group{}, fmt.Errorf("%w: only administrators can remove members", ErrGroupPermission)
	}

	removed := make(map[string]bool, len(members))

	for _, member := range members {
		if member.ACI == self {
			return Group{}, fmt.Errorf("%w: use groups leave to remove yourself", ErrInvalidGroupMember)
		}

		membership, _ := g.MembershipOf(member.ACI)
		if membership == MembershipNone {
			return Group{}, fmt.Errorf("%w: %s is not a member, invited or requesting", ErrInvalidGroupMember, member)
		}

		removed[member.ACI] = true
	}

	g.Members = slices.DeleteFunc(slices.Clone(g.Members), func(m GroupMember) bool { return removed[m.Recipient.ACI] })
	g.Pending = slices.DeleteFunc(slices.Clone(g.Pending), func(m PendingMember) bool { return removed[m.Recipient.ACI] })
	g.Requesting = slices.DeleteFunc(slices.Clone(g.Requesting), func(m RequestingMember) bool {
		return removed[m.Recipient.ACI]
	})

	return g, nil
}
