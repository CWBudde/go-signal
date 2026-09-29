//go:build cgo || libsignal_go

package signal

import (
	"context"
	"fmt"
	"slices"

	"github.com/cwbudde/mautrix-signal/pkg/libsignalgo"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow/types"
	"github.com/google/uuid"
)

func (c *meowClient) AddGroupMembers(ctx context.Context, ref string, members []Recipient) (Group, error) {
	members, err := normalizeGroupMembers(members)
	if err != nil {
		return Group{}, err
	}

	cli, done, err := c.groupClient("add group members")
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

	group := convertGroup(raw, c.ownACI)

	change, err := addMembersChange(raw, c.ownACI, members)
	if err != nil {
		return Group{}, fmt.Errorf("add group members %s: %w", gid, err)
	}

	if len(change.AddMembers) == 0 && len(change.PromoteRequestingMembers) == 0 {
		c.cacheGroup(ctx, group)
		return group, nil
	}

	defer cli.GroupCache.Delete(gid)

	accepted, err := addGroupMembersOnce(ctx, cli, raw, change, func() { cli.GroupCache.Delete(gid) })
	if err != nil {
		partial := Group{}
		if accepted != nil {
			partial.ID, partial.Revision = string(accepted.GroupIdentifier), accepted.Revision
		}

		return partial, c.lostOr(fmt.Errorf("add group members %s: %w", gid, err))
	}

	group = convertGroup(accepted, c.ownACI)
	c.cacheGroup(ctx, group)

	return group, nil
}

func addMembersChange(raw *signalmeow.Group, self string, members []Recipient) (*signalmeow.GroupChange, error) {
	group := convertGroup(raw, self)

	targets, err := group.CheckAddMembers(self, members)
	if err != nil {
		return nil, err
	}

	change := &signalmeow.GroupChange{}

	for _, target := range targets {
		aci := uuid.MustParse(target.ACI) // CheckAddMembers validated and canonicalized each ACI.
		for _, banned := range raw.BannedMembers {
			if banned != nil && banned.ServiceID == libsignalgo.NewACIServiceID(aci) {
				return nil, fmt.Errorf("%w: %s is banned from the group", ErrInvalidGroupMember, target)
			}
		}

		membership, _ := group.MembershipOf(target.ACI)
		if membership == MembershipRequesting {
			change.PromoteRequestingMembers = append(change.PromoteRequestingMembers, &signalmeow.RoleMember{
				ACI: aci, Role: signalmeow.GroupMember_DEFAULT,
			})
		} else {
			change.AddMembers = append(change.AddMembers, &signalmeow.AddMember{
				GroupMember: signalmeow.GroupMember{ACI: aci, Role: signalmeow.GroupMember_DEFAULT},
			})
		}
	}

	return change, nil
}

type groupAdditionSender interface {
	groupRemovalSender
	RetrieveGroupByID(ctx context.Context, gid types.GroupIdentifier, revision uint32) (
		*signalmeow.Group, *signalmeow.SendEndorsementCache, error,
	)
}

func addGroupMembersOnce(ctx context.Context, cli groupAdditionSender,
	raw *signalmeow.Group, change *signalmeow.GroupChange, invalidate func(),
) (*signalmeow.Group, error) {
	// SendGroupUpdate includes AddMembers but not PromoteRequestingMembers as recipients.
	// Add approved requesters to a copy so they receive the update too.
	notify := *raw

	notify.Members = slices.Clone(raw.Members)
	for _, requester := range change.PromoteRequestingMembers {
		notify.Members = append(notify.Members, &signalmeow.GroupMember{ACI: requester.ACI, Role: requester.Role})
	}

	var committed *signalmeow.Group

	err := changeGroupMembersOnce(ctx, cli, &notify, change, func() {
		// The callback runs only after the server accepts the patch. Do not return
		// our proposed revision as accepted state on a rejected or uncertain request.
		committed = &signalmeow.Group{GroupIdentifier: raw.GroupIdentifier, Revision: change.Revision}

		invalidate()
	}, "added")
	if err != nil {
		return committed, err
	}

	// Credential lookup may have converted AddMembers to invitations. Never infer the
	// accepted membership from our requested change or apply it to the cache locally.
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
