package signaltest

import (
	"context"
	"slices"
	"time"

	"github.com/cwbudde/go-signal/internal/signal"
)

func (c *client) AddGroupMembers(_ context.Context, ref string, members []signal.Recipient) (signal.Group, error) {
	c.fake.mu.Lock()
	defer c.fake.mu.Unlock()

	err := c.checkGroupOp("add group members")
	if err != nil {
		return signal.Group{}, err
	}

	groupID, err := c.groupID(ref)
	if err != nil {
		return signal.Group{}, err
	}

	group, err := c.fetch(groupID)
	if err != nil {
		return signal.Group{}, err
	}

	targets, err := group.CheckAddMembers(c.connected, members)
	if err != nil {
		return signal.Group{}, err //nolint:wrapcheck // facade validation
	}

	if len(targets) == 0 {
		return group, nil
	}

	if c.fake.AddGroupMembersErr != nil {
		return signal.Group{}, c.fake.AddGroupMembersErr
	}

	group = c.addMembers(group, targets)
	c.fake.GroupInfo[groupID] = group
	c.fake.cacheGroup(group)

	return group, nil
}

func (c *client) addMembers(group signal.Group, targets []signal.Recipient) signal.Group {
	group.Members = slices.Clone(group.Members)
	group.Pending = slices.Clone(group.Pending)
	group.Requesting = slices.Clone(group.Requesting)
	group.Revision++

	invitedAt := c.fake.GroupInviteTime
	if invitedAt.IsZero() {
		invitedAt = time.Now()
	}

	for _, target := range targets {
		membership, _ := group.MembershipOf(target.ACI)
		if membership == signal.MembershipRequesting {
			group.Requesting = slices.DeleteFunc(group.Requesting, func(m signal.RequestingMember) bool {
				return m.Recipient.ACI == target.ACI
			})
		}

		if membership != signal.MembershipRequesting && c.fake.GroupInvitees[target.ACI] {
			group.Pending = append(group.Pending, signal.PendingMember{
				Recipient: target, Role: signal.GroupRoleMember,
				AddedBy: signal.Recipient{ACI: c.connected}, InvitedAt: invitedAt,
			})
		} else {
			group.Members = append(group.Members, signal.GroupMember{
				Recipient: target, Role: signal.GroupRoleMember, JoinedAtRevision: group.Revision,
			})
		}
	}

	return group
}
