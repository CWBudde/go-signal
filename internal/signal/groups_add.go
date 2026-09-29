package signal

import "fmt"

// CheckAddMembers validates full membership and member-add permissions. It returns
// unique, canonicalized targets, skipping existing members and pending invitations.
// Requesting members are returned for administrator approval. It changes no state.
func (g Group) CheckAddMembers(self string, members []Recipient) ([]Recipient, error) {
	members, err := normalizeGroupMembers(members)
	if err != nil {
		return nil, err
	}

	membership, role := g.MembershipOf(self)
	if membership != MembershipMember {
		return nil, ErrNotAMember
	}

	if role != GroupRoleAdmin && !g.MembersCanAddMembers {
		return nil, fmt.Errorf("%w: only administrators can add members to this group", ErrGroupPermission)
	}

	targets := make([]Recipient, 0, len(members))
	for _, member := range members {
		membership, _ := g.MembershipOf(member.ACI)
		switch membership {
		case MembershipMember, MembershipPending:
			continue
		case MembershipRequesting:
			if role != GroupRoleAdmin {
				return nil, fmt.Errorf("%w: only administrators can approve join requests", ErrGroupPermission)
			}
		case MembershipNone:
		}

		targets = append(targets, member)
	}

	return targets, nil
}
