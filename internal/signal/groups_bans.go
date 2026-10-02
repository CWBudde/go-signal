package signal

import (
	"fmt"
	"math"
	"slices"
	"time"
)

// NormalizeGroupBanMembers canonicalizes and deduplicates nonempty ACI targets.
func NormalizeGroupBanMembers(members []Recipient) ([]Recipient, error) {
	return normalizeGroupMembers(members)
}

// WithBannedMembers validates an administrator's ban change against the original group.
// Bans remove full membership, invitations and requests atomically, including inconsistent
// active membership of an already banned user. Unbans never add membership. Self is rejected
// for both operations. The result owns all slices; its revision advances once when changed.
func (g Group) WithBannedMembers(self string, members []Recipient, banned bool, bannedAt time.Time) (Group, error) {
	members, err := NormalizeGroupBanMembers(members)
	if err != nil {
		return Group{}, err
	}

	targets, err := g.banTargets(self, members)
	if err != nil {
		return Group{}, err
	}

	next := g
	next.Members = slices.Clone(g.Members)
	next.Pending = slices.Clone(g.Pending)
	next.Requesting = slices.Clone(g.Requesting)
	next.Banned = slices.Clone(g.Banned)

	if banned {
		next.removeBannedTargets(targets)

		for _, member := range members {
			if !g.bannedACI(member.ACI) {
				next.Banned = append(next.Banned, BannedMember{Recipient: member, BannedAt: bannedAt})
			}
		}
	} else {
		next.Banned = slices.DeleteFunc(next.Banned, func(m BannedMember) bool {
			return m.Recipient.ACI != "" && targets[m.Recipient.ACI]
		})
	}

	changed := g.banMembershipChanged(next)
	if changed {
		if g.Revision == math.MaxUint32 {
			return Group{}, fmt.Errorf("%w: group revision cannot be incremented", ErrUnknownGroup)
		}

		next.Revision++
	}

	next.Membership, next.Role = next.MembershipOf(self)

	return next, nil
}

func (g Group) banTargets(self string, members []Recipient) (map[string]bool, error) {
	membership, role := g.MembershipOf(self)
	if membership != MembershipMember {
		return nil, ErrNotAMember
	}

	if role != GroupRoleAdmin {
		return nil, fmt.Errorf("%w: only administrators can ban or unban users", ErrGroupPermission)
	}

	targets := make(map[string]bool, len(members))
	for _, member := range members {
		if member.ACI == self {
			return nil, fmt.Errorf("%w: cannot ban or unban yourself", ErrInvalidGroupMember)
		}

		targets[member.ACI] = true
	}

	return targets, nil
}

func (g Group) bannedACI(aci string) bool {
	for _, member := range g.Banned {
		if member.Recipient.ACI != "" && member.Recipient.ACI == aci {
			return true
		}
	}

	return false
}

// removeBannedTargets works on cloned slices after all targets have been validated.
func (g *Group) removeBannedTargets(targets map[string]bool) {
	g.Members = slices.DeleteFunc(g.Members, func(m GroupMember) bool { return targets[m.Recipient.ACI] })
	g.Pending = slices.DeleteFunc(g.Pending, func(m PendingMember) bool {
		return m.Recipient.ACI != "" && targets[m.Recipient.ACI]
	})
	g.Requesting = slices.DeleteFunc(g.Requesting, func(m RequestingMember) bool { return targets[m.Recipient.ACI] })
}

func (g Group) banMembershipChanged(next Group) bool {
	return len(next.Members) != len(g.Members) || len(next.Pending) != len(g.Pending) ||
		len(next.Requesting) != len(g.Requesting) || len(next.Banned) != len(g.Banned)
}
