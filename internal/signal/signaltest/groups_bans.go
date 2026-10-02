package signaltest

import (
	"context"
	"fmt"
	"time"

	"github.com/cwbudde/go-signal/internal/signal"
)

func (c *client) SetGroupBanned(ctx context.Context, ref string,
	members []signal.Recipient, banned bool,
) (signal.Group, error) {
	c.fake.mu.Lock()
	defer c.fake.mu.Unlock()

	members, err := signal.NormalizeGroupBanMembers(members)
	if err != nil {
		return signal.Group{}, err //nolint:wrapcheck // facade validation
	}

	err = c.checkGroupOp("set group banned")
	if err != nil {
		return signal.Group{}, err
	}

	err = ctx.Err()
	if err != nil {
		return signal.Group{}, fmt.Errorf("set group banned: %w", err)
	}

	groupID, err := c.groupID(ref)
	if err != nil {
		return signal.Group{}, err
	}

	group, err := c.fetch(groupID)
	if err != nil {
		return signal.Group{}, err
	}

	at := groupBanTimestamp(c.fake.GroupBanTime)

	next, err := group.WithBannedMembers(c.connected, members, banned, at)
	if err != nil {
		return signal.Group{}, err //nolint:wrapcheck // facade validation
	}

	if next.Revision == group.Revision {
		return next, nil
	}

	if c.fake.SetGroupBannedErr != nil {
		return signal.Group{}, c.fake.SetGroupBannedErr
	}

	c.fake.GroupInfo[groupID] = next
	if c.fake.GroupBanFollowUpErr != nil {
		return signal.Group{ID: groupID, Revision: next.Revision},
			fmt.Errorf("change was accepted at revision %d, but fetching the group failed; "+
				"inspect groups show before retrying: %w", next.Revision, c.fake.GroupBanFollowUpErr)
	}

	return c.fetch(groupID)
}

func groupBanTimestamp(at time.Time) time.Time {
	if at.IsZero() {
		return time.Now().UTC().Truncate(time.Millisecond)
	}

	return at
}
