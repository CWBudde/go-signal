package signaltest

import (
	"context"

	"github.com/cwbudde/go-signal/internal/signal"
)

func (c *client) RenameGroup(_ context.Context, ref, title string) (signal.Group, error) {
	c.fake.mu.Lock()
	defer c.fake.mu.Unlock()

	err := signal.ValidateGroupTitle(title)
	if err != nil {
		return signal.Group{}, err //nolint:wrapcheck // signal facade error
	}

	err = c.checkGroupOp("rename group")
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

	err = group.CheckRename(c.connected, title)
	if err != nil {
		return signal.Group{}, err //nolint:wrapcheck // signal facade error
	}

	if group.Title == title {
		return group, nil
	}

	if c.fake.RenameErr != nil {
		return signal.Group{}, c.fake.RenameErr
	}

	group.Title = title
	group.Revision++
	c.fake.GroupInfo[groupID] = group
	c.fake.cacheGroup(group)

	return group, nil
}
