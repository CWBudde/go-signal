package signaltest

import (
	"context"
	"fmt"

	"github.com/cwbudde/go-signal/internal/signal"
)

func (c *client) SetGroupMemberRole(ctx context.Context, ref string,
	members []signal.Recipient, role signal.GroupRole,
) (signal.Group, error) {
	c.fake.mu.Lock()
	defer c.fake.mu.Unlock()

	members, err := signal.NormalizeGroupRoleMembers(members, role)
	if err != nil {
		return signal.Group{}, err //nolint:wrapcheck // facade validation
	}

	err = c.checkGroupOp("set group member role")
	if err != nil {
		return signal.Group{}, err
	}

	err = ctx.Err()
	if err != nil {
		return signal.Group{}, fmt.Errorf("set group member role: %w", err)
	}

	groupID, err := c.groupID(ref)
	if err != nil {
		return signal.Group{}, err
	}

	group, err := c.fetch(groupID)
	if err != nil {
		return signal.Group{}, err
	}

	next, err := group.WithMemberRole(c.connected, members, role)
	if err != nil {
		return signal.Group{}, err //nolint:wrapcheck // facade validation
	}

	if next.Revision == group.Revision {
		return next, nil
	}

	if c.fake.SetGroupMemberRoleErr != nil {
		return signal.Group{}, c.fake.SetGroupMemberRoleErr
	}

	c.fake.GroupInfo[groupID] = next
	if c.fake.GroupRoleFollowUpErr != nil {
		return signal.Group{ID: groupID, Revision: next.Revision},
			fmt.Errorf("change was accepted at revision %d, but fetching the group failed; "+
				"inspect groups show before retrying: %w", next.Revision, c.fake.GroupRoleFollowUpErr)
	}

	return c.fetch(groupID)
}
