//go:build cgo || libsignal_go

package signal

import (
	"context"
	"fmt"

	"github.com/cwbudde/mautrix-signal/pkg/signalmeow"
	"github.com/google/uuid"
)

func (c *meowClient) CreateGroup(ctx context.Context, opts CreateGroupOptions) (Group, error) {
	err := opts.Check()
	if err != nil {
		return Group{}, err
	}

	cli, done, err := c.groupClient("create group")
	if err != nil {
		return Group{}, err
	}
	defer done()

	ctx = c.zlog.WithContext(ctx)

	// The creator must be a full member. Other users may fall back to invitations in
	// signalmeow.EncryptGroup if their profile credentials are unavailable.
	_, err = cli.FetchExpiringProfileKeyCredentialById(ctx, cli.Store.ACI)
	if err != nil {
		return Group{}, c.lostOr(fmt.Errorf("fetch creator profile credential: %w", err))
	}

	raw := newGroup(opts, cli.Store.ACI)

	_, err = signalmeow.PrepareGroupCreation(raw)
	if err != nil {
		return Group{}, fmt.Errorf("prepare group: %w", err)
	}

	created, err := cli.CreateGroup(ctx, raw)
	if err != nil {
		// CreateGroup can fail after the PUT succeeds, while notifying members. Never
		// automatically retry with a new key, which would create a second group.
		return Group{ID: string(raw.GroupIdentifier)}, c.lostOr(fmt.Errorf(
			"create group %s (creation may have succeeded; inspect with groups show before retrying): %w",
			raw.GroupIdentifier, err))
	}

	group := c.convertGroup(created)
	c.cacheGroup(ctx, group)

	return group, nil
}

func newGroup(opts CreateGroupOptions, self uuid.UUID) *signalmeow.Group {
	group := &signalmeow.Group{
		Title: opts.Title,
		Members: []*signalmeow.GroupMember{
			{ACI: self, Role: signalmeow.GroupMember_ADMINISTRATOR},
		},
		AccessControl: &signalmeow.GroupAccessControl{
			Members:           signalmeow.AccessControl_MEMBER,
			Attributes:        signalmeow.AccessControl_MEMBER,
			AddFromInviteLink: signalmeow.AccessControl_UNSATISFIABLE,
		},
	}

	for _, member := range opts.UniqueMembers(self.String()) {
		group.Members = append(group.Members, &signalmeow.GroupMember{
			ACI: uuid.MustParse(member.ACI), Role: signalmeow.GroupMember_DEFAULT,
		})
	}

	return group
}
