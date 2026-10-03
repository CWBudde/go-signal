package signaltest

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"math"
	"slices"
	"time"

	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/go-signal/internal/signal/groupinvite"
)

//nolint:nonamedreturns // Deferred redaction requires the final partial result.
func (c *client) JoinGroup(ctx context.Context, link string) (result signal.GroupJoinResult, err error) {
	defer func() {
		if err != nil {
			err = signal.GroupJoinOperationError(err, result)
		}
	}()

	invite, err := groupinvite.Parse(link)
	if err != nil {
		return result, signal.ErrInvalidGroupInviteLink
	}

	c.fake.mu.Lock()
	defer c.fake.mu.Unlock()

	err = c.checkGroupOp("join group")
	if err != nil {
		return result, err
	}

	err = ctx.Err()
	if err != nil {
		return result, fmt.Errorf("group join: %w", err)
	}

	key := base64.StdEncoding.EncodeToString(invite.MasterKey)

	group, exists := c.fake.GroupJoinServer[key]
	if !exists {
		return result, signal.ErrGroupLinkInactive
	}

	group = cloneJoinGroup(group)
	result.ID = group.ID

	existing, err := c.knownJoinGroup(key, group)
	if err != nil {
		return result, err
	}

	if existing.Verified {
		return existing, nil
	}

	result.Revision = group.Revision

	err = c.checkJoinPreview(group, invite.Password)
	if err != nil {
		return result, err
	}

	c.retainGroupJoinKey(key, group.ID)

	return c.submitGroupJoin(key, group, result)
}

func (c *client) submitGroupJoin(key string, group signal.Group,
	result signal.GroupJoinResult,
) (signal.GroupJoinResult, error) {
	membership, _ := group.MembershipOf(c.connected)
	if membership == signal.MembershipRequesting {
		return signal.GroupJoinResult{
			ID: group.ID, Title: group.Title, Revision: group.Revision, Status: signal.GroupJoinRequesting, Verified: true,
		}, nil
	}

	result.Revision++

	err := c.fake.JoinGroupErr
	if err != nil {
		return result, err
	}

	return c.commitGroupJoin(key, group, result)
}

func (c *client) commitGroupJoin(key string, group signal.Group,
	result signal.GroupJoinResult,
) (signal.GroupJoinResult, error) {
	group.Revision++

	status := signal.GroupJoinMember
	if c.linkState(group.ID) == signal.GroupLinkApproval {
		status = signal.GroupJoinRequesting

		group.Requesting = append(group.Requesting, signal.RequestingMember{Recipient: signal.Recipient{ACI: c.connected}})
	} else {
		group.Members = append(group.Members, signal.GroupMember{
			Recipient: signal.Recipient{ACI: c.connected}, Role: signal.GroupRoleMember, JoinedAtRevision: group.Revision,
		})
	}

	c.fake.GroupJoinServer[key] = group

	result.Revision, result.Accepted, result.Changed = group.Revision, true, true

	err := c.fake.GroupJoinFollowUpErr
	if err != nil {
		return result, err
	}

	if status == signal.GroupJoinMember {
		group.LeftAt = time.Time{}
		c.fake.cacheGroup(group)
	}

	result.Title, result.Status, result.Verified = group.Title, status, true

	return result, nil
}

func cloneJoinGroup(group signal.Group) signal.Group {
	group.Members = slices.Clone(group.Members)
	group.Pending = slices.Clone(group.Pending)
	group.Requesting = slices.Clone(group.Requesting)
	group.Banned = slices.Clone(group.Banned)

	return group
}

func (c *client) joinedGroup(ref string) (signal.Group, bool) {
	for key, id := range c.fake.GroupJoinKnownKeys[c.connected] {
		if id == ref || key == ref {
			group, ok := c.fake.GroupJoinServer[key]
			if ok {
				return cloneJoinGroup(group), true
			}
		}
	}

	return signal.Group{}, false
}

func (c *client) knownJoinGroup(key string, group signal.Group) (signal.GroupJoinResult, error) {
	if c.fake.GroupJoinKnownKeys[c.connected][key] != group.ID {
		return signal.GroupJoinResult{}, nil
	}

	fetchErr := c.fake.GroupErrs[group.ID]
	if errors.Is(fetchErr, signal.ErrNotAMember) || errors.Is(fetchErr, signal.ErrUnknownGroup) {
		return signal.GroupJoinResult{}, nil
	}

	if fetchErr != nil {
		return signal.GroupJoinResult{}, fetchErr
	}

	membership, _ := group.MembershipOf(c.connected)
	if membership == signal.MembershipPending {
		return signal.GroupJoinResult{}, signal.ErrGroupInvitationRequiresAcceptance
	}

	if membership != signal.MembershipMember {
		return signal.GroupJoinResult{}, nil
	}

	group.LeftAt = time.Time{}
	c.fake.cacheGroup(group)

	return signal.GroupJoinResult{
		ID: group.ID, Title: group.Title, Revision: group.Revision, Status: signal.GroupJoinMember, Verified: true,
	}, nil
}

func (c *client) checkJoinPreview(group signal.Group, password []byte) error {
	if c.fake.GroupLinkPasswords[group.ID] != base64.StdEncoding.EncodeToString(password) {
		return signal.ErrGroupLinkInactive
	}

	if slices.ContainsFunc(group.Banned, func(member signal.BannedMember) bool {
		return member.Recipient.ACI == c.connected
	}) {
		return signal.ErrGroupLinkInactive
	}

	membership, _ := group.MembershipOf(c.connected)
	if membership == signal.MembershipRequesting {
		return nil
	}

	state := c.linkState(group.ID)
	if state != signal.GroupLinkEnabled && state != signal.GroupLinkApproval {
		return signal.ErrGroupLinkInactive
	}

	if group.Revision == math.MaxUint32 {
		return signal.ErrInvalidGroupInviteLink
	}

	return nil
}

func (c *client) retainGroupJoinKey(key, groupID string) {
	if c.fake.GroupJoinKnownKeys == nil {
		c.fake.GroupJoinKnownKeys = make(map[string]map[string]string)
	}

	if c.fake.GroupJoinKnownKeys[c.connected] == nil {
		c.fake.GroupJoinKnownKeys[c.connected] = make(map[string]string)
	}

	c.fake.GroupJoinKnownKeys[c.connected][key] = groupID
}

func (f *Fake) joinServerKnows(groupID string) bool {
	for _, group := range f.GroupJoinServer {
		if group.ID == groupID {
			return true
		}
	}

	return false
}

func (f *Fake) knowsJoinID(account, groupID string) bool {
	for _, knownID := range f.GroupJoinKnownKeys[account] {
		if knownID == groupID {
			return true
		}
	}

	return false
}
