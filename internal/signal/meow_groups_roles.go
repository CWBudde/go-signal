//go:build cgo || libsignal_go

package signal

import (
	"context"
	"fmt"

	"github.com/cwbudde/mautrix-signal/pkg/signalmeow"
	"github.com/google/uuid"
)

func (c *meowClient) SetGroupMemberRole(ctx context.Context, ref string,
	members []Recipient, role GroupRole,
) (Group, error) {
	members, err := NormalizeGroupRoleMembers(members, role)
	if err != nil {
		return Group{}, err
	}

	cli, done, err := c.groupClient("set group member role")
	if err != nil {
		return Group{}, err
	}
	defer done()

	err = ctx.Err()
	if err != nil {
		return Group{}, fmt.Errorf("set group member role: %w", err)
	}

	ctx = c.zlog.WithContext(ctx)

	gid, err := resolveGroupRef(ctx, c.connDevice.GroupStore, ref)
	if err != nil {
		return Group{}, err
	}
	// Even no-ops must authorize against current full membership and administrator roles.
	cli.GroupCache.Delete(gid)
	defer cli.GroupCache.Delete(gid)

	raw, _, err := cli.RetrieveGroupByID(ctx, gid, 0)
	if err != nil {
		return Group{}, c.lostOr(groupFetchError(gid, err))
	}

	change, err := memberRoleChange(raw, c.ownACI, members, role)
	if err != nil {
		return Group{}, fmt.Errorf("set group member role %s: %w", gid, err)
	}

	if change == nil {
		group := convertGroup(raw, c.ownACI)
		c.cacheGroup(ctx, group)

		return group, nil
	}

	accepted, err := setGroupMemberRoleOnce(ctx, cli, raw, change, func() { cli.GroupCache.Delete(gid) })
	if err != nil {
		partial := Group{}
		if accepted != nil {
			partial.ID, partial.Revision = string(accepted.GroupIdentifier), accepted.Revision
		}

		return partial, c.lostOr(fmt.Errorf("set group member role %s: %w", gid, err))
	}

	group := convertGroup(accepted, c.ownACI)
	c.cacheGroup(ctx, group)

	return group, nil
}

func memberRoleChange(raw *signalmeow.Group, self string, members []Recipient,
	role GroupRole,
) (*signalmeow.GroupChange, error) {
	group := convertGroup(raw, self)

	next, err := group.WithMemberRole(self, members, role)
	if err != nil {
		return nil, err
	}

	if next.Revision == group.Revision {
		return nil, nil //nolint:nilnil // unchanged roles require no patch
	}
	// WithMemberRole validated every ACI, desired role and target before any change.
	members, _ = NormalizeGroupRoleMembers(members, role)

	wireRole := signalmeow.GroupMember_DEFAULT
	if role == GroupRoleAdmin {
		wireRole = signalmeow.GroupMember_ADMINISTRATOR
	}

	change := &signalmeow.GroupChange{}

	for _, member := range members {
		_, currentRole := group.MembershipOf(member.ACI)
		if currentRole != role {
			change.ModifyMemberRoles = append(change.ModifyMemberRoles, &signalmeow.RoleMember{
				ACI: uuid.MustParse(member.ACI), Role: wireRole,
			})
		}
	}

	return change, nil
}

func setGroupMemberRoleOnce(ctx context.Context, cli groupAdditionSender,
	raw *signalmeow.Group, change *signalmeow.GroupChange, invalidate func(),
) (*signalmeow.Group, error) {
	return updateGroupAndFetchOnce(ctx, cli, raw, change, invalidate, "Group member roles updated")
}
