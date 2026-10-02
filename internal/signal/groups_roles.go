package signal

import (
	"fmt"
	"math"
	"slices"
)

// NormalizeGroupRoleMembers checks the desired role and canonicalizes and deduplicates
// nonempty full-member ACI targets without modifying the caller's slice.
func NormalizeGroupRoleMembers(members []Recipient, role GroupRole) ([]Recipient, error) {
	if role != GroupRoleAdmin && role != GroupRoleMember {
		return nil, fmt.Errorf("%w: role must be admin or member", ErrInvalidGroupMember)
	}

	return normalizeGroupMembers(members)
}

// WithMemberRole validates an administrator's role change against the original group.
// Every target must be a full member. Self demotion requires another full administrator,
// including in a singleton group. Existing roles are no-ops but still require authorization.
// The returned group owns its slices and increments the revision once only if roles changed.
func (g Group) WithMemberRole(self string, members []Recipient, role GroupRole) (Group, error) {
	members, err := NormalizeGroupRoleMembers(members, role)
	if err != nil {
		return Group{}, err
	}

	targets, err := g.memberRoleTargets(self, members)
	if err != nil {
		return Group{}, err
	}

	next := g
	next.Members = slices.Clone(g.Members)
	next.Pending = slices.Clone(g.Pending)
	next.Requesting = slices.Clone(g.Requesting)
	changed := false

	for i := range next.Members {
		if targets[next.Members[i].Recipient.ACI] && next.Members[i].Role != role {
			next.Members[i].Role = role
			changed = true
		}
	}

	if next.Admins() == 0 {
		return Group{}, ErrLastAdmin
	}

	if changed {
		if g.Revision == math.MaxUint32 {
			return Group{}, fmt.Errorf("%w: group revision cannot be incremented", ErrUnknownGroup)
		}

		next.Revision++
	}

	next.Membership, next.Role = next.MembershipOf(self)

	return next, nil
}

// memberRoleTargets checks authorization and every target before any role is changed.
func (g Group) memberRoleTargets(self string, members []Recipient) (map[string]bool, error) {
	membership, ownRole := g.MembershipOf(self)
	if membership != MembershipMember {
		return nil, ErrNotAMember
	}

	if ownRole != GroupRoleAdmin {
		return nil, fmt.Errorf("%w: only administrators can change member roles", ErrGroupPermission)
	}

	targets := make(map[string]bool, len(members))
	for _, member := range members {
		membership, _ := g.MembershipOf(member.ACI)
		if membership != MembershipMember {
			return nil, fmt.Errorf("%w: %s is not a full member of the group", ErrInvalidGroupMember, member)
		}

		targets[member.ACI] = true
	}

	return targets, nil
}
