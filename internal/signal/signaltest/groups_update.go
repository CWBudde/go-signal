package signaltest

import (
	"context"
	"fmt"
	"slices"

	"github.com/cwbudde/go-signal/internal/signal"
)

func (c *client) UpdateGroup(ctx context.Context, ref string, update signal.GroupUpdate) (signal.Group, error) {
	c.fake.mu.Lock()
	defer c.fake.mu.Unlock()

	err := update.Check()
	if err != nil {
		return signal.Group{}, err //nolint:wrapcheck // facade validation
	}

	err = c.checkGroupOp("update group")
	if err != nil {
		return signal.Group{}, err
	}

	err = ctx.Err()
	if err != nil {
		return signal.Group{}, fmt.Errorf("update group: %w", err)
	}

	groupID, err := c.groupID(ref)
	if err != nil {
		return signal.Group{}, err
	}

	group, err := c.fetch(groupID)
	if err != nil {
		return signal.Group{}, err
	}

	next, err := group.WithUpdate(c.connected, update)
	if err != nil {
		return signal.Group{}, err //nolint:wrapcheck // facade validation
	}

	if next.Revision == group.Revision {
		return group, nil
	}

	err = c.avatarUploadError(update)
	if err != nil {
		return signal.Group{}, err
	}

	if c.fake.UpdateGroupErr != nil {
		return signal.Group{}, c.fake.UpdateGroupErr
	}

	c.applyAvatar(groupID, &next, update)

	return c.acceptedGroupUpdate(groupID, next)
}

func (c *client) acceptedGroupUpdate(groupID string, next signal.Group) (signal.Group, error) {
	c.fake.GroupInfo[groupID] = next
	if c.fake.GroupUpdateFollowUpErr != nil {
		partial := signal.Group{ID: groupID, Revision: next.Revision}

		return partial, fmt.Errorf("change was accepted at revision %d, but fetching the group failed; "+
			"inspect groups show before retrying: %w", next.Revision, c.fake.GroupUpdateFollowUpErr)
	}

	return c.fetch(groupID)
}

func (c *client) avatarUploadError(update signal.GroupUpdate) error {
	if update.Avatar != nil && !update.Avatar.Remove {
		return c.fake.GroupAvatarUploadErr
	}

	return nil
}

func (c *client) applyAvatar(groupID string, next *signal.Group, update signal.GroupUpdate) {
	if update.Avatar == nil {
		return
	}

	if update.Avatar.Remove {
		delete(c.fake.GroupAvatarData, groupID)
		return
	}

	if c.fake.GroupAvatarData == nil {
		c.fake.GroupAvatarData = make(map[string][]byte)
	}

	c.fake.GroupAvatarData[groupID] = slices.Clone(update.Avatar.Data)
	next.AvatarPath = fmt.Sprintf("fake-group-avatar-%d", next.Revision)
}
