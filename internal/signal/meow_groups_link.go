//go:build cgo || libsignal_go

package signal

import (
	"context"
	"fmt"

	"github.com/cwbudde/mautrix-signal/pkg/signalmeow"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow/types"
)

func (c *meowClient) GroupLink(ctx context.Context, ref string) (GroupLink, error) {
	ctx = c.zlog.WithContext(ctx)

	_, raw, done, err := c.groupLinkRaw(ctx, ref, "group link")
	if err != nil {
		return GroupLink{}, err
	}
	defer done()

	link, err := convertGroupLink(raw, c.ownACI)
	if err != nil {
		return GroupLink{}, err
	}

	c.cacheGroup(ctx, convertGroup(raw, c.ownACI))

	return link, nil
}

func (c *meowClient) UpdateGroupLink(ctx context.Context, ref string, update GroupLinkUpdate) (GroupLink, error) {
	err := update.Check()
	if err != nil {
		return GroupLink{}, err
	}

	ctx = c.zlog.WithContext(ctx)

	cli, raw, done, err := c.groupLinkRaw(ctx, ref, "update group link")
	if err != nil {
		return GroupLink{}, err
	}
	defer done()

	change, err := groupLinkChange(raw, c.ownACI, update)
	if err != nil {
		return GroupLink{}, err
	}

	if change == nil {
		link, convertErr := convertGroupLink(raw, c.ownACI)
		if convertErr != nil {
			return GroupLink{}, convertErr
		}

		c.cacheGroup(ctx, convertGroup(raw, c.ownACI))

		return link, nil
	}

	accepted, err := updateGroupLinkOnce(ctx, cli, raw, change, func() { cli.GroupCache.Delete(raw.GroupIdentifier) })

	partial := GroupLink{}
	if accepted != nil {
		partial.ID = string(accepted.GroupIdentifier)
		partial.Revision = accepted.Revision
	}

	if err != nil {
		return partial, c.lostOr(fmt.Errorf("update group link: %w", err))
	}

	link, err := verifiedGroupLink(accepted, c.ownACI, change.Revision)
	if err != nil {
		return link, err
	}

	c.cacheGroup(ctx, convertGroup(accepted, c.ownACI))

	return link, nil
}

// groupLinkRaw returns fresh state and a cleanup that always evicts it, including no-ops.
func (c *meowClient) groupLinkRaw(ctx context.Context, ref, operation string,
) (*signalmeow.Client, *signalmeow.Group, func(), error) {
	cli, done, err := c.groupClient(operation)
	if err != nil {
		return nil, nil, nil, err
	}

	err = ctx.Err()
	if err != nil {
		done()
		return nil, nil, nil, fmt.Errorf("%s: %w", operation, err)
	}

	gid, err := resolveGroupRef(ctx, c.connDevice.GroupStore, ref)
	if err != nil {
		done()
		return nil, nil, nil, GroupLinkReferenceError(err)
	}

	cli.GroupCache.Delete(gid)

	cleanup := func() { cli.GroupCache.Delete(gid); done() }

	raw, _, err := cli.RetrieveGroupByID(ctx, gid, 0)
	if err != nil {
		cleanup()
		return nil, nil, nil, c.lostOr(groupFetchError(gid, err))
	}

	if raw == nil {
		cleanup()
		return nil, nil, nil, fmt.Errorf("%w: missing group link state", ErrUnknownGroup)
	}

	return cli, raw, cleanup, nil
}

func groupLinkChange(raw *signalmeow.Group, self string, update GroupLinkUpdate) (*signalmeow.GroupChange, error) {
	group := convertGroup(raw, self)
	state, password := rawGroupLink(raw)

	next, nextPassword, err := group.WithLinkUpdate(self, state, password, update)
	if err != nil {
		return nil, err
	}

	if next.Revision == group.Revision {
		return nil, nil //nolint:nilnil // unchanged link requires no patch
	}

	change := &signalmeow.GroupChange{}

	if update.State != nil {
		access := groupLinkAccess(*update.State)
		if raw.AccessControl == nil || raw.AccessControl.AddFromInviteLink != access {
			change.ModifyAddFromInviteLinkAccess = new(access)
		}
	}

	if nextPassword != password {
		change.ModifyInviteLinkPassword = new(types.SerializedInviteLinkPassword(nextPassword))
	}

	return change, nil
}

func convertGroupLink(raw *signalmeow.Group, self string) (GroupLink, error) {
	state, password := rawGroupLink(raw)
	return convertGroup(raw, self).Link(self, state, password)
}

func rawGroupLink(raw *signalmeow.Group) (GroupLinkState, string) {
	state := GroupLinkUnknown

	if raw.AccessControl != nil {
		switch raw.AccessControl.AddFromInviteLink {
		case signalmeow.AccessControl_UNSATISFIABLE:
			state = GroupLinkDisabled
		case signalmeow.AccessControl_ANY:
			state = GroupLinkEnabled
		case signalmeow.AccessControl_ADMINISTRATOR:
			state = GroupLinkApproval
		case signalmeow.AccessControl_UNKNOWN, signalmeow.AccessControl_MEMBER:
		}
	}

	password := ""
	if raw.InviteLinkPassword != nil {
		password = string(*raw.InviteLinkPassword)
	}

	return state, password
}

func groupLinkAccess(state GroupLinkState) signalmeow.AccessControl {
	switch state {
	case GroupLinkDisabled:
		return signalmeow.AccessControl_UNSATISFIABLE
	case GroupLinkEnabled:
		return signalmeow.AccessControl_ANY
	case GroupLinkApproval:
		return signalmeow.AccessControl_ADMINISTRATOR
	case GroupLinkUnknown:
	}

	return signalmeow.AccessControl_UNKNOWN
}

func updateGroupLinkOnce(ctx context.Context, cli groupAdditionSender, raw *signalmeow.Group,
	change *signalmeow.GroupChange, invalidate func(),
) (*signalmeow.Group, error) {
	// Keep original full/pending notification recipients; links do not change membership.
	accepted, err := updateGroupAndFetchOnce(ctx, cli, raw, change, invalidate, "Group link updated")
	if err != nil {
		return accepted, groupLinkOperationError{cause: err}
	}

	return accepted, nil
}

func verifiedGroupLink(raw *signalmeow.Group, self string, committedRevision uint32) (GroupLink, error) {
	link, err := convertGroupLink(raw, self)
	if err != nil {
		return GroupLink{ID: string(raw.GroupIdentifier), Revision: committedRevision}, fmt.Errorf(
			"change was accepted at revision %d, but reading the link failed; inspect groups link show before retrying: %w",
			committedRevision, err,
		)
	}

	return link, nil
}
