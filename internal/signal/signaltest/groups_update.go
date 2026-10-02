package signaltest

import (
	"context"
	"fmt"

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

	if c.fake.UpdateGroupErr != nil {
		return signal.Group{}, c.fake.UpdateGroupErr
	}

	c.fake.GroupInfo[groupID] = next
	if c.fake.GroupUpdateFollowUpErr != nil {
		partial := signal.Group{ID: groupID, Revision: next.Revision}

		return partial, fmt.Errorf("change was accepted at revision %d, but fetching the group failed; "+
			"inspect groups show before retrying: %w", next.Revision, c.fake.GroupUpdateFollowUpErr)
	}

	return c.fetch(groupID)
}
