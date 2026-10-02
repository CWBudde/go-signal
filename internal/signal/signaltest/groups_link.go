package signaltest

import (
	"context"
	"fmt"

	"github.com/cwbudde/go-signal/internal/signal"
)

func (c *client) GroupLink(ctx context.Context, ref string) (signal.GroupLink, error) {
	c.fake.mu.Lock()
	defer c.fake.mu.Unlock()

	group, err := c.linkGroup(ctx, ref, "group link")
	if err != nil {
		return signal.GroupLink{}, err
	}

	if c.fake.GroupLinkErr != nil {
		return signal.GroupLink{}, c.fake.GroupLinkErr
	}

	state, password := c.linkState(group.ID), c.fake.GroupLinkPasswords[group.ID]

	return group.Link(c.connected, state, password) //nolint:wrapcheck // facade policy
}

func (c *client) UpdateGroupLink(ctx context.Context, ref string, update signal.GroupLinkUpdate,
) (signal.GroupLink, error) {
	c.fake.mu.Lock()
	defer c.fake.mu.Unlock()

	err := update.Check()
	if err != nil {
		return signal.GroupLink{}, err //nolint:wrapcheck // facade validation
	}

	group, err := c.linkGroup(ctx, ref, "update group link")
	if err != nil {
		return signal.GroupLink{}, err
	}

	link, password, err := group.WithLinkUpdate(
		c.connected, c.linkState(group.ID), c.fake.GroupLinkPasswords[group.ID], update,
	)
	if err != nil {
		return signal.GroupLink{}, err //nolint:wrapcheck // facade policy
	}

	if link.Revision == group.Revision {
		return link, nil
	}

	if c.fake.UpdateGroupLinkErr != nil {
		return signal.GroupLink{}, c.fake.UpdateGroupLinkErr
	}

	if c.fake.GroupLinkStates == nil {
		c.fake.GroupLinkStates = make(map[string]signal.GroupLinkState)
	}

	if c.fake.GroupLinkPasswords == nil {
		c.fake.GroupLinkPasswords = make(map[string]string)
	}

	c.fake.GroupLinkStates[group.ID] = link.State
	c.fake.GroupLinkPasswords[group.ID] = password
	group.Revision = link.Revision
	c.fake.GroupInfo[group.ID] = group
	c.fake.cacheGroup(group)

	if c.fake.GroupLinkFollowUpErr != nil {
		return signal.GroupLink{ID: group.ID, Revision: link.Revision}, fmt.Errorf(
			"change was accepted at revision %d, but fetching the group failed; inspect groups link show before retrying: %w",
			link.Revision, c.fake.GroupLinkFollowUpErr,
		)
	}

	return link, nil
}

func (c *client) linkGroup(ctx context.Context, ref, operation string) (signal.Group, error) {
	err := c.checkGroupOp(operation)
	if err != nil {
		return signal.Group{}, err
	}

	err = ctx.Err()
	if err != nil {
		return signal.Group{}, fmt.Errorf("%s: %w", operation, err)
	}

	id, err := c.groupID(ref)
	if err != nil {
		return signal.Group{}, signal.GroupLinkReferenceError(err) //nolint:wrapcheck // hides secret reference
	}

	return c.fetch(id)
}

func (c *client) linkState(id string) signal.GroupLinkState {
	state, ok := c.fake.GroupLinkStates[id]
	if !ok {
		return signal.GroupLinkDisabled
	}

	return state
}
