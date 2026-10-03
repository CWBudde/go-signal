package signaltest

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/google/uuid"
)

//nolint:nonamedreturns,wrapcheck,cyclop // Deferred redaction wraps every stage while preserving partial evidence.
func (c *client) AcceptGroupInvitation(ctx context.Context, ref string) (result signal.GroupAcceptResult, err error) {
	defer func() {
		if err != nil {
			err = signal.GroupAcceptOperationError(err, result)
		}
	}()

	err = signal.CheckGroupAcceptReference(ref)
	if err != nil {
		return result, err
	}

	c.fake.mu.Lock()

	defer c.fake.mu.Unlock()

	err = c.checkGroupOp("accept group invitation")
	if err != nil {
		return result, err
	}

	err = ctx.Err()
	if err != nil {
		return result, err
	}

	account, err := c.fake.account(c.opts)
	if err != nil {
		return result, err
	}

	key, group, err := c.acceptGroupFixture(ref)
	if err != nil {
		return result, err
	}

	result.ID, result.Revision = group.ID, group.Revision

	err = c.fake.GroupErrs[group.ID]
	if err != nil {
		return result, err
	}

	invited, noop, err := group.CheckAcceptInvitation(signal.Recipient{ACI: account.ACI, PNI: account.PNI})
	if err != nil {
		return result, err
	}

	err = ctx.Err()
	if err != nil {
		return result, err
	}

	if noop {
		c.storeAcceptedFixture(key, group)

		return c.cacheGroupAcceptance(group, result), nil
	}

	return c.commitGroupAcceptance(ctx, key, group, invited, account, result)
}

func (c *client) commitGroupAcceptance(ctx context.Context, key string, group signal.Group,
	invited signal.Recipient, account signal.Account, result signal.GroupAcceptResult,
) (signal.GroupAcceptResult, error) {
	result.Revision++

	err := c.fake.AcceptGroupInvitationErr
	if err != nil {
		return result, err
	}

	role := signal.GroupRoleMember

	for _, pending := range group.Pending {
		if acceptSameRecipient(pending.Recipient, invited) {
			role = pending.Role
		}
	}

	if invited.PNI != "" {
		role = signal.GroupRoleMember
	}

	group.Pending = slices.DeleteFunc(group.Pending, func(pending signal.PendingMember) bool {
		return acceptSameRecipient(pending.Recipient, invited) ||
			invited.PNI != "" && acceptSameRecipient(pending.Recipient, signal.Recipient{ACI: account.ACI})
	})

	group.Revision = result.Revision

	group.Members = append(group.Members, signal.GroupMember{
		Recipient: signal.Recipient{ACI: account.ACI}, Role: role, JoinedAtRevision: group.Revision,
	})

	c.storeAcceptedFixture(key, group)

	result.Accepted, result.Changed = true, true

	err = c.fake.GroupAcceptFollowUpErr
	if err != nil {
		return result, err
	}

	err = ctx.Err()
	if err != nil {
		return result, fmt.Errorf("group acceptance: %w", err)
	}

	return c.cacheGroupAcceptance(group, result), nil
}

//nolint:cyclop // Compare nonzero UUIDs only within the same explicit identity type.
func acceptSameRecipient(left, right signal.Recipient) bool {
	if left.ACI != "" && left.PNI == "" && right.ACI != "" && right.PNI == "" {
		leftID, err := uuid.Parse(left.ACI)

		rightID, rightErr := uuid.Parse(right.ACI)

		return err == nil && rightErr == nil && leftID != uuid.Nil && leftID == rightID
	}

	if left.PNI != "" && left.ACI == "" && right.PNI != "" && right.ACI == "" {
		leftID, err := uuid.Parse(left.PNI)

		rightID, rightErr := uuid.Parse(right.PNI)

		return err == nil && rightErr == nil && leftID != uuid.Nil && leftID == rightID
	}

	return false
}

func (c *client) acceptGroupFixture(ref string) (string, signal.Group, error) {
	ref = signal.NormalizeGroupAcceptReference(ref)

	groupID, err := c.groupID(ref)
	if err != nil {
		return "", signal.Group{}, err
	}

	for key, id := range c.fake.GroupJoinKnownKeys[c.connected] {
		if id == groupID {
			if group, ok := c.fake.GroupJoinServer[key]; ok && group.ID == id {
				return key, cloneJoinGroup(group), nil
			}
		}
	}

	if c.fake.joinServerKnows(groupID) {
		return "", signal.Group{}, signal.ErrUnknownGroup
	}

	group, ok := c.fake.GroupInfo[groupID]

	if !ok {
		return "", signal.Group{}, signal.ErrUnknownGroup
	}

	group = cloneJoinGroup(group)

	group.ID = groupID

	key := group.MasterKey

	if key == "" {
		key = groupID
	}

	return key, group, nil
}

// storeAcceptedFixture retains only the selected account's key. Legacy fixtures remain
// untouched: once introduced, their server snapshot cannot leak through global aliases.
func (c *client) storeAcceptedFixture(key string, group signal.Group) {
	if c.fake.GroupJoinServer == nil {
		c.fake.GroupJoinServer = make(map[string]signal.Group)
	}

	c.fake.GroupJoinServer[key] = cloneJoinGroup(group)

	c.retainGroupJoinKey(key, group.ID)
}

func (c *client) cacheGroupAcceptance(group signal.Group, result signal.GroupAcceptResult) signal.GroupAcceptResult {
	group.LeftAt = time.Time{}

	c.fake.cacheJoinGroup(c.connected, group)

	result.Title, result.Verified = group.Title, true

	return result
}
