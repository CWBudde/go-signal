//go:build cgo || libsignal_go

package signal

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/cwbudde/mautrix-signal/pkg/libsignalgo"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow"
	"github.com/google/uuid"
)

func (c *meowClient) SetGroupBanned(ctx context.Context, ref string,
	members []Recipient, banned bool,
) (Group, error) {
	members, err := NormalizeGroupBanMembers(members)
	if err != nil {
		return Group{}, err
	}

	cli, done, err := c.groupClient("set group banned")
	if err != nil {
		return Group{}, err
	}
	defer done()

	err = ctx.Err()
	if err != nil {
		return Group{}, fmt.Errorf("set group banned: %w", err)
	}

	ctx = c.zlog.WithContext(ctx)

	gid, err := resolveGroupRef(ctx, c.connDevice.GroupStore, ref)
	if err != nil {
		return Group{}, err
	}
	// Even no-ops must authorize against current full membership and administrator roles.
	cli.GroupCache.Delete(gid)
	defer cli.GroupCache.Delete(gid)

	raw, _, err := cli.RetrieveGroupByID(ctx, gid, 0)
	if err != nil {
		return Group{}, c.lostOr(groupFetchError(gid, err))
	}

	change, err := bannedMembersChange(raw, c.ownACI, members, banned, time.Now().UTC().Truncate(time.Millisecond))
	if err != nil {
		return Group{}, fmt.Errorf("set group banned %s: %w", gid, err)
	}

	if change == nil {
		group := convertGroup(raw, c.ownACI)
		c.cacheGroup(ctx, group)

		return group, nil
	}

	accepted, err := setGroupBannedOnce(ctx, cli, raw, change, func() { cli.GroupCache.Delete(gid) })
	if err != nil {
		partial := Group{}
		if accepted != nil {
			partial.ID, partial.Revision = string(accepted.GroupIdentifier), accepted.Revision
		}

		return partial, c.lostOr(fmt.Errorf("set group banned %s: %w", gid, err))
	}

	group := convertGroup(accepted, c.ownACI)
	c.cacheGroup(ctx, group)

	return group, nil
}

func bannedMembersChange(raw *signalmeow.Group, self string, members []Recipient, banned bool, bannedAt time.Time,
) (*signalmeow.GroupChange, error) {
	group := convertGroup(raw, self)

	next, err := group.WithBannedMembers(self, members, banned, bannedAt)
	if err != nil {
		return nil, err
	}

	if next.Revision == group.Revision {
		return nil, nil //nolint:nilnil // unchanged bans require no patch
	}

	members, _ = NormalizeGroupBanMembers(members)
	change := &signalmeow.GroupChange{}

	for _, member := range members {
		aci := uuid.MustParse(member.ACI)
		sid := libsignalgo.NewACIServiceID(aci)

		if !banned {
			if group.bannedACI(member.ACI) {
				change.DeleteBannedMembers = append(change.DeleteBannedMembers, &sid)
			}

			continue
		}
		// Inspect all original lists: inconsistent duplicate membership must also be removed.
		if slices.ContainsFunc(group.Members, func(m GroupMember) bool { return m.Recipient.ACI == member.ACI }) {
			change.DeleteMembers = append(change.DeleteMembers, &aci)
		}

		if slices.ContainsFunc(group.Pending, func(m PendingMember) bool { return m.Recipient.ACI == member.ACI }) {
			change.DeletePendingMembers = append(change.DeletePendingMembers, &sid)
		}

		if slices.ContainsFunc(group.Requesting, func(m RequestingMember) bool { return m.Recipient.ACI == member.ACI }) {
			change.DeleteRequestingMembers = append(change.DeleteRequestingMembers, &aci)
		}

		if !group.bannedACI(member.ACI) {
			change.AddBannedMembers = append(change.AddBannedMembers, &signalmeow.BannedMember{
				ServiceID: sid, Timestamp: uint64(bannedAt.UnixMilli()), //nolint:gosec // current epoch milliseconds fit
			})
		}
	}

	return change, nil
}

func setGroupBannedOnce(ctx context.Context, cli groupAdditionSender, raw *signalmeow.Group,
	change *signalmeow.GroupChange, invalidate func(),
) (*signalmeow.Group, error) {
	// SendGroupUpdate already includes original full and pending members. Add only removed
	// requesters to a copy so they receive their rejection, without notifying absent targets.
	notify := *raw
	notify.Members = slices.Clone(raw.Members)

	for _, aci := range change.DeleteRequestingMembers {
		if !slices.ContainsFunc(notify.Members, func(m *signalmeow.GroupMember) bool { return m != nil && m.ACI == *aci }) {
			notify.Members = append(notify.Members, &signalmeow.GroupMember{ACI: *aci, Role: signalmeow.GroupMember_DEFAULT})
		}
	}

	return updateGroupAndFetchOnce(ctx, cli, &notify, change, invalidate, "Group bans updated")
}
