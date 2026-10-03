package signaltest

import (
	"context"
	"fmt"
	"math"
	"slices"

	"github.com/cwbudde/go-signal/internal/signal"
)

//nolint:nonamedreturns,wrapcheck,cyclop,funlen // Deferred wrapping preserves exact safe partial evidence.
func (c *client) CancelGroupJoinRequest(ctx context.Context, ref string,
) (result signal.GroupCancelRequestResult, err error) {
	defer func() {
		if err != nil {
			err = signal.GroupCancelRequestOperationError(err, result)
		}
	}()

	err = signal.CheckGroupCancelRequestReference(ref)
	if err != nil {
		return result, err
	}

	c.fake.mu.Lock()
	defer c.fake.mu.Unlock()

	err = c.checkGroupOp("cancel group join request")
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

	key, group, err := c.cancelRequestFixture(ref)
	if err != nil {
		return result, err
	}

	result.ID = group.ID

	err = c.fake.GroupErrs[group.ID]
	if err != nil {
		return result, err
	}

	result.Title, result.Revision = group.Title, group.Revision
	self := signal.Recipient{ACI: account.ACI}
	pending := slices.ContainsFunc(group.Requesting, func(request signal.RequestingMember) bool {
		return acceptSameRecipient(request.Recipient, self)
	})
	result.Verified = !pending

	err = ctx.Err()
	if err != nil {
		return result, err
	}

	if !pending {
		return result, nil
	}

	if group.Revision == math.MaxUint32 {
		return result, signal.ErrGroupChanged
	}

	result.Revision++

	err = c.fake.CancelGroupJoinRequestErr
	if err != nil {
		return result, err
	}

	group.Requesting = slices.DeleteFunc(group.Requesting, func(request signal.RequestingMember) bool {
		return acceptSameRecipient(request.Recipient, self)
	})
	group.Revision = result.Revision
	// Only owned server state changes; known keys and title/left caches remain untouched.
	c.fake.GroupJoinServer[key] = cloneJoinGroup(group)
	result.Accepted, result.Changed = true, true

	err = c.fake.GroupCancelRequestVerificationErr
	if err != nil {
		return result, err
	}

	result.Verified = true

	err = ctx.Err()
	if err != nil {
		return result, fmt.Errorf("group request cancellation: %w", err)
	}

	return result, nil
}

func (c *client) cancelRequestFixture(ref string) (string, signal.Group, error) {
	groupID, err := c.groupID(signal.NormalizeGroupCancelRequestReference(ref))
	if err != nil {
		return "", signal.Group{}, err
	}

	for key, known := range c.fake.GroupJoinKnownKeys[c.connected] {
		if known == groupID {
			group, ok := c.fake.GroupJoinServer[key]
			if ok && group.ID == groupID {
				return key, cloneJoinGroup(group), nil
			}
		}
	}

	return "", signal.Group{}, signal.ErrUnknownGroup
}
