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
// their typed identities and removes duplicates in input order without changing the input.
func NormalizeGroupRemovalMembers(members []Recipient) ([]Recipient, error) {
	if len(members) == 0 {
		return nil, fmt.Errorf("%w: provide at least one member", ErrInvalidGroupMember)
	}

	out := make([]Recipient, 0, len(members))

	seen := make(map[string]bool, len(members))
	for _, member := range members {
		if member.ACI == "" && member.PNI == "" {
			return nil, fmt.Errorf("%w: group member needs a nonzero ACI or PNI", ErrUnresolvable)
		}

		for _, value := range []*string{&member.ACI, &member.PNI} {
			if *value == "" {
				continue
			}

			id := acceptIdentity(*value)
			if id == uuid.Nil {
				return nil, fmt.Errorf("%w: invalid group member identity", ErrUnresolvable)
			}

			*value = id.String()
		}

		key := member.ACI + "|" + member.PNI
		if !seen[key] {
			seen[key] = true

			out = append(out, member)
		}
	}

	return out, nil
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
	return g.WithRemovedMembersAs(Recipient{ACI: self}, members)
}

// WithRemovedMembersAs also guards against removal of the selected account's PNI invitation.
func (g Group) WithRemovedMembersAs(self Recipient, members []Recipient) (Group, error) {
	targets, err := g.removalTargets(self, members)
	if err != nil {
		return Group{}, err
	}

	return g.withRemovedTargets(targets), nil
}

func (g Group) removalTargets(self Recipient, members []Recipient) ([]Recipient, error) {
	members, err := NormalizeGroupRemovalMembers(members)
	if err != nil {
		return nil, err
	}

	membership, role := g.MembershipOf(self.ACI)
	if membership != MembershipMember {
		return nil, ErrNotAMember
	}

	if role != GroupRoleAdmin {
		return nil, fmt.Errorf("%w: only administrators can remove members", ErrGroupPermission)
	}

	var targets []Recipient

	for _, member := range members {
		if acceptSelfMatches(Recipient{ACI: member.ACI}, self) || acceptSelfMatches(Recipient{PNI: member.PNI}, self) {
			return nil, fmt.Errorf("%w: use groups leave to remove yourself", ErrInvalidGroupMember)
		}

		matched := g.removalMembershipTargets(member)
		if len(matched) == 0 {
			return nil, fmt.Errorf("%w: %s is not a member, invited or requesting", ErrInvalidGroupMember, member)
		}

		targets = append(targets, matched...)
	}

	return NormalizeGroupRemovalMembers(targets)
}

func (g Group) removalMembershipTargets(member Recipient) []Recipient {
	var targets []Recipient

	withoutPending := g
	withoutPending.Pending = nil

	membership, _ := withoutPending.MembershipOf(member.ACI)
	if membership != MembershipNone {
		targets = append(targets, Recipient{ACI: member.ACI})
	}

	for _, pending := range g.Pending {
		if acceptSelfMatches(pending.Recipient, member) {
			targets = append(targets, pending.Recipient)
		}
	}

	return targets
}

func removalMatches(recipient Recipient, targets []Recipient) bool {
	for _, target := range targets {
		if acceptSelfMatches(recipient, target) {
			return true
		}
	}

	return false
}

func (g Group) withRemovedTargets(targets []Recipient) Group {
	g.Members = slices.DeleteFunc(slices.Clone(g.Members), func(m GroupMember) bool {
		return removalMatches(Recipient{ACI: m.Recipient.ACI}, targets)
	})
	g.Pending = slices.DeleteFunc(slices.Clone(g.Pending), func(m PendingMember) bool {
		return removalMatches(m.Recipient, targets)
	})
	g.Requesting = slices.DeleteFunc(slices.Clone(g.Requesting), func(m RequestingMember) bool {
		return removalMatches(Recipient{ACI: m.Recipient.ACI}, targets)
	})

	return g
}
