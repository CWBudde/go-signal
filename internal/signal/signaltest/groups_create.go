package signaltest

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"

	"github.com/cwbudde/go-signal/internal/signal"
)

func (c *client) CreateGroup(_ context.Context, opts signal.CreateGroupOptions) (signal.Group, error) {
	c.fake.mu.Lock()
	defer c.fake.mu.Unlock()

	err := opts.Check()
	if err != nil {
		return signal.Group{}, err //nolint:wrapcheck // signal facade validation
	}

	err = c.checkGroupOp("create group")
	if err != nil {
		return signal.Group{}, err
	}

	if c.fake.CreateGroupErr != nil {
		return signal.Group{}, c.fake.CreateGroupErr
	}

	// Deterministic IDs keep CLI goldens stable; each invocation still creates a new group.
	id := sha256.Sum256(fmt.Appendf(nil, "fake group %d", len(c.fake.GroupInfo)))

	group := signal.Group{
		ID: base64.StdEncoding.EncodeToString(id[:]), Title: opts.Title,
		Membership: signal.MembershipMember, Role: signal.GroupRoleAdmin,
		MembersCanEditAttributes: true,
		Members: []signal.GroupMember{
			{Recipient: signal.Recipient{ACI: c.connected}, Role: signal.GroupRoleAdmin},
		},
	}
	for _, member := range opts.UniqueMembers(c.connected) {
		group.Members = append(group.Members, signal.GroupMember{Recipient: member, Role: signal.GroupRoleMember})
	}

	if c.fake.GroupInfo == nil {
		c.fake.GroupInfo = make(map[string]signal.Group)
	}

	c.fake.GroupInfo[group.ID] = group
	c.fake.cacheGroup(group)

	return group, nil
}
