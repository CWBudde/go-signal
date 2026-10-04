package signaltest

import (
	"context"

	"github.com/cwbudde/go-signal/internal/signal"
)

func (c *client) RemoveGroupMembers(_ context.Context, ref string, members []signal.Recipient) (signal.Group, error) {
	c.fake.mu.Lock()
	defer c.fake.mu.Unlock()

	members, err := signal.NormalizeGroupRemovalMembers(members)
	if err != nil {
		return signal.Group{}, err //nolint:wrapcheck // signal facade error
	}

	err = c.checkGroupOp("remove group members")
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

	group, err = group.WithRemovedMembersAs(c.selfRecipient(), members)
	if err != nil {
		return signal.Group{}, err //nolint:wrapcheck // signal facade error
	}

	if c.fake.RemoveGroupMembersErr != nil {
		return signal.Group{}, c.fake.RemoveGroupMembersErr
	}

	group.Revision++
	c.fake.GroupInfo[groupID] = group
	c.fake.cacheGroup(group)

	return group, nil
}
