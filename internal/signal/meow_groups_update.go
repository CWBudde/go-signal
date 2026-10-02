//go:build cgo || libsignal_go

package signal

import (
	"context"
	"errors"
	"fmt"
	"math"

	"github.com/cwbudde/mautrix-signal/pkg/signalmeow"
)

func settingsChange(raw *signalmeow.Group, self string, update GroupUpdate) (*signalmeow.GroupChange, error) {
	group := convertGroup(raw, self)

	next, err := group.WithUpdate(self, update)
	if err != nil {
		return nil, err
	}

	change := &signalmeow.GroupChange{}
	if next.Description != group.Description {
		change.ModifyDescription = new(next.Description)
	}

	if next.Timer != group.Timer {
		change.ModifyDisappearingMessagesDuration = new(*update.TimerSeconds)
	}

	if next.AnnouncementsOnly != group.AnnouncementsOnly {
		change.ModifyAnnouncementsOnly = new(next.AnnouncementsOnly)
	}

	settingsAccessChanges(raw.AccessControl, update, change)

	if next.Revision == group.Revision && change.ModifyAttributesAccess == nil && change.ModifyMemberAccess == nil {
		return nil, nil //nolint:nilnil // unchanged raw settings require no patch
	}

	// A permission normalization may be necessary even when facade booleans match.
	if raw.Revision == math.MaxUint32 {
		return nil, fmt.Errorf("%w: group revision cannot be incremented", ErrUnknownGroup)
	}

	return change, nil
}

// settingsAccessChanges compares exact server permissions; conservative facade booleans
// deliberately map absent and unrecognized access to false, which is not necessarily admins.
func settingsAccessChanges(raw *signalmeow.GroupAccessControl, update GroupUpdate, change *signalmeow.GroupChange) {
	attributes, members := signalmeow.AccessControl_UNKNOWN, signalmeow.AccessControl_UNKNOWN
	if raw != nil {
		attributes, members = raw.Attributes, raw.Members
	}

	if update.MembersCanEditAttributes != nil && attributes != settingsAccess(*update.MembersCanEditAttributes) {
		change.ModifyAttributesAccess = new(settingsAccess(*update.MembersCanEditAttributes))
	}

	if update.MembersCanAddMembers != nil && members != settingsAccess(*update.MembersCanAddMembers) {
		change.ModifyMemberAccess = new(settingsAccess(*update.MembersCanAddMembers))
	}
}

func settingsAccess(members bool) signalmeow.AccessControl {
	if members {
		return signalmeow.AccessControl_MEMBER
	}

	return signalmeow.AccessControl_ADMINISTRATOR
}

func updateGroupSettingsOnce(ctx context.Context, cli groupAdditionSender,
	raw *signalmeow.Group, change *signalmeow.GroupChange, invalidate func(),
) (*signalmeow.Group, error) {
	var committed *signalmeow.Group

	err := changeGroupOnce(ctx, cli, raw, change, func() {
		committed = &signalmeow.Group{GroupIdentifier: raw.GroupIdentifier, Revision: change.Revision}

		invalidate()
	}, "Group settings updated")
	if err != nil {
		if committed == nil {
			err = settingsPatchError(raw, change.Revision, err)
		}

		return committed, err
	}

	invalidate()

	accepted, _, err := cli.RetrieveGroupByID(ctx, raw.GroupIdentifier, change.Revision)
	if err != nil {
		return committed, fmt.Errorf("change was accepted at revision %d, but fetching the group failed; "+
			"inspect groups show before retrying: %w", change.Revision, groupFetchError(raw.GroupIdentifier, err))
	}

	if accepted == nil || accepted.Revision < change.Revision || accepted.GroupIdentifier != raw.GroupIdentifier {
		return committed, fmt.Errorf("change was accepted at revision %d, but fetching the group returned invalid state: %w",
			change.Revision, errInvalidGroupChangeResponse)
	}

	return accepted, nil
}

func (c *meowClient) UpdateGroup(ctx context.Context, ref string, update GroupUpdate) (Group, error) {
	err := update.Check()
	if err != nil {
		return Group{}, err
	}

	cli, done, err := c.groupClient("update group")
	if err != nil {
		return Group{}, err
	}
	defer done()

	ctx = c.zlog.WithContext(ctx)

	gid, err := resolveGroupRef(ctx, c.connDevice.GroupStore, ref)
	if err != nil {
		return Group{}, err
	}
	// Current membership and permissions must come from the server, including no-ops.
	cli.GroupCache.Delete(gid)
	defer cli.GroupCache.Delete(gid)

	raw, _, err := cli.RetrieveGroupByID(ctx, gid, 0)
	if err != nil {
		return Group{}, c.lostOr(groupFetchError(gid, err))
	}

	change, err := settingsChange(raw, c.ownACI, update)
	if err != nil {
		return Group{}, fmt.Errorf("update group %s: %w", gid, err)
	}

	if change == nil {
		group := convertGroup(raw, c.ownACI)
		c.cacheGroup(ctx, group)

		return group, nil
	}

	accepted, err := updateGroupSettingsOnce(ctx, cli, raw, change, func() { cli.GroupCache.Delete(gid) })
	if err != nil {
		partial := Group{}
		if accepted != nil {
			partial.ID, partial.Revision = string(accepted.GroupIdentifier), accepted.Revision
		}

		return partial, c.lostOr(fmt.Errorf("update group %s: %w", gid, err))
	}

	group := convertGroup(accepted, c.ownACI)
	c.cacheGroup(ctx, group)

	return group, nil
}

// settingsPatchError cannot distinguish encryption/request errors from errors decoding an
// already accepted PATCH response. Only explicit rejections or our preflight are definite.
func settingsPatchError(raw *signalmeow.Group, revision uint32, err error) error {
	for _, rejected := range []error{
		ErrUnknownGroup, ErrGroupChanged,
		signalmeow.AuthorizationFailedError, signalmeow.NotFoundError,
		signalmeow.GroupPatchNotAcceptedError, signalmeow.RateLimitError,
		signalmeow.DeprecatedVersionError,
	} {
		if errors.Is(err, rejected) {
			return err
		}
	}

	return fmt.Errorf("%w for group %s at attempted revision %d; inspect groups show %s before retrying: %w",
		ErrGroupUpdateUncertain, raw.GroupIdentifier, revision, raw.GroupIdentifier, err)
}
