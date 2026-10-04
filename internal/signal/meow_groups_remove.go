//go:build cgo || libsignal_go

package signal

import (
	"context"
	"encoding/base64"
	"fmt"
	"math"

	"github.com/cwbudde/mautrix-signal/pkg/signalmeow"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow/protobuf/signalpb"
	"github.com/rs/zerolog"
	"google.golang.org/protobuf/proto"
)

func (c *meowClient) RemoveGroupMembers(ctx context.Context, ref string, members []Recipient) (Group, error) {
	members, err := NormalizeGroupRemovalMembers(members)
	if err != nil {
		return Group{}, err
	}

	cli, done, err := c.groupClient("remove group members")
	if err != nil {
		return Group{}, err
	}
	defer done()

	ctx = c.zlog.WithContext(ctx)

	gid, err := resolveGroupRef(ctx, c.connDevice.GroupStore, ref)
	if err != nil {
		return Group{}, err
	}

	cli.GroupCache.Delete(gid)

	raw, _, err := cli.RetrieveGroupByID(ctx, gid, 0)
	if err != nil {
		return Group{}, c.lostOr(groupFetchError(gid, err))
	}

	group := c.convertGroup(raw)

	change, next, err := removeMembersChangeAs(group, Recipient{ACI: c.ownACI, PNI: c.connDevice.PNI.String()}, members)
	if err != nil {
		return Group{}, fmt.Errorf("remove group members %s: %w", group.ID, err)
	}

	// Evict on success and failure: the next operation must fetch current membership and
	// endorsements. Keep raw unchanged so notifications include the removed members.
	defer cli.GroupCache.Delete(gid)

	err = removeGroupMembersOnce(ctx, cli, raw, change, func() { cli.GroupCache.Delete(gid) })
	if err != nil {
		return Group{}, c.lostOr(fmt.Errorf("remove group members %s: %w", group.ID, err))
	}

	next.Revision = change.Revision
	c.cacheGroup(ctx, next)

	return next, nil
}

func removeMembersChangeAs(group Group, self Recipient, members []Recipient) (*signalmeow.GroupChange, Group, error) {
	targets, err := group.removalTargets(self, members)
	if err != nil {
		return nil, Group{}, err
	}

	change := &signalmeow.GroupChange{}

	for _, member := range group.Members {
		if removalMatches(Recipient{ACI: member.Recipient.ACI}, targets) {
			aci := acceptIdentity(member.Recipient.ACI)
			change.DeleteMembers = append(change.DeleteMembers, &aci)
		}
	}

	for _, pending := range group.Pending {
		if removalMatches(pending.Recipient, targets) {
			serviceID := acceptServiceID(pending.Recipient)
			change.DeletePendingMembers = append(change.DeletePendingMembers, &serviceID)
		}
	}

	for _, request := range group.Requesting {
		if removalMatches(Recipient{ACI: request.Recipient.ACI}, targets) {
			aci := acceptIdentity(request.Recipient.ACI)
			change.DeleteRequestingMembers = append(change.DeleteRequestingMembers, &aci)
		}
	}

	return change, group.withRemovedTargets(targets), nil
}

type groupRemovalSender interface {
	EncryptAndSignGroupChange(ctx context.Context, change *signalmeow.GroupChange) (*signalpb.GroupChangeResponse, error)
	SendGroupUpdate(ctx context.Context, group *signalmeow.Group, groupContext *signalpb.GroupContextV2,
		change *signalmeow.GroupChange) (*signalmeow.GroupMessageSendResult, error)
}

// removeGroupMembersOnce avoids signalmeow.UpdateGroup's automatic conflict rebase:
// changed membership must be reviewed against a fresh group before another attempt.
func removeGroupMembersOnce(ctx context.Context, cli groupRemovalSender,
	raw *signalmeow.Group, change *signalmeow.GroupChange, invalidate func(),
) error {
	return changeGroupOnce(ctx, cli, raw, change, invalidate, "Group members removed")
}

// changeGroupOnce commits a single change without automatic conflict rebasing.
func changeGroupOnce(ctx context.Context, cli groupRemovalSender,
	raw *signalmeow.Group, change *signalmeow.GroupChange, invalidate func(), operation string,
) error {
	masterKey, err := base64.StdEncoding.DecodeString(string(raw.GroupMasterKey))
	if err != nil || len(masterKey) != groupKeyLen || raw.Revision == math.MaxUint32 {
		return fmt.Errorf("%w: invalid group key or revision", ErrUnknownGroup)
	}

	change.GroupMasterKey = raw.GroupMasterKey
	change.Revision = raw.Revision + 1

	response, err := cli.EncryptAndSignGroupChange(ctx, change)
	if err != nil {
		return updateGroupError(err)
	}

	invalidate()

	if len(response.GetGroupChange().GetActions()) == 0 {
		return errInvalidGroupChangeResponse
	}

	signed, err := proto.Marshal(response.GetGroupChange())
	if err != nil {
		return fmt.Errorf("encode accepted group change; inspect groups show before retrying: %w", err)
	}

	groupContext := &signalpb.GroupContextV2{Revision: &change.Revision, GroupChange: signed, MasterKey: masterKey}

	_, err = cli.SendGroupUpdate(ctx, raw, groupContext, change)
	if err != nil {
		// The server mutation succeeded. Match other group updates: report notification
		// trouble in logs without inviting a retry of the already committed mutation.
		zerolog.Ctx(ctx).Error().Err(err).Msgf("%s, but notifying group members failed", operation)
	}

	return nil
}
