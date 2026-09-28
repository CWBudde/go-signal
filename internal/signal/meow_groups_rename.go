//go:build cgo || libsignal_go

package signal

import (
	"context"
	"fmt"

	"github.com/cwbudde/mautrix-signal/pkg/signalmeow"
)

func (c *meowClient) RenameGroup(ctx context.Context, ref, title string) (Group, error) {
	err := ValidateGroupTitle(title)
	if err != nil {
		return Group{}, err
	}

	cli, done, err := c.groupClient("rename group")
	if err != nil {
		return Group{}, err
	}
	defer done()

	ctx = c.zlog.WithContext(ctx)

	gid, err := resolveGroupRef(ctx, c.connDevice.GroupStore, ref)
	if err != nil {
		return Group{}, err
	}

	// Check membership and permissions against current state, including after a conflict.
	cli.GroupCache.Delete(gid)

	group, err := c.fetchGroup(ctx, cli, gid)
	if err != nil {
		return Group{}, c.lostOr(err)
	}

	change, err := renameChange(group, c.ownACI, title)
	if err != nil {
		return Group{}, fmt.Errorf("rename group %s: %w", group.ID, err)
	}

	if change == nil {
		return group, nil
	}

	// As with LeaveGroup, signalmeow logs notification failures after a successful patch.
	revision, err := cli.UpdateGroup(ctx, change, gid)
	if err != nil {
		cli.GroupCache.Delete(gid)

		return Group{}, c.lostOr(fmt.Errorf("rename group %s: %w", group.ID, updateGroupError(err)))
	}

	group.Title, group.Revision = title, revision
	c.cacheGroup(ctx, group)

	return group, nil
}

// renameChange changes only the title; returning nil avoids a redundant group update.
func renameChange(group Group, self, title string) (*signalmeow.GroupChange, error) {
	err := group.CheckRename(self, title)
	if err != nil {
		return nil, err
	}

	if group.Title == title {
		return nil, nil //nolint:nilnil // no group change is needed
	}

	return &signalmeow.GroupChange{ModifyTitle: &title}, nil
}
