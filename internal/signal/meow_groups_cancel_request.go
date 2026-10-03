//go:build cgo || libsignal_go

package signal

import (
	"context"
	"errors"
	"fmt"
	"math"

	"github.com/cwbudde/mautrix-signal/pkg/signalmeow"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow/types"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow/web"
	"github.com/rs/zerolog"
)

func (c *meowClient) CancelGroupJoinRequest(ctx context.Context, ref string) (GroupCancelRequestResult, error) {
	err := CheckGroupCancelRequestReference(ref)
	if err != nil {
		return GroupCancelRequestResult{}, GroupCancelRequestOperationError(err, GroupCancelRequestResult{})
	}

	err = groupAcceptContextError(ctx)
	if err != nil {
		return GroupCancelRequestResult{}, GroupCancelRequestOperationError(err, GroupCancelRequestResult{})
	}

	c.mu.Lock()
	closing := c.closing
	c.mu.Unlock()

	if closing {
		return GroupCancelRequestResult{}, GroupCancelRequestOperationError(ErrClosed, GroupCancelRequestResult{})
	}

	cli, done, err := c.groupClient("cancel group join request")
	if err != nil {
		return GroupCancelRequestResult{}, GroupCancelRequestOperationError(err, GroupCancelRequestResult{})
	}
	defer done()

	ops := groupCancelRequestOperations{
		Resolve: func(ctx context.Context, ref string) (types.GroupIdentifier, types.SerializedGroupMasterKey, error) {
			gid, err := resolveGroupRef(ctx, c.connDevice.GroupStore, NormalizeGroupCancelRequestReference(ref))
			if err != nil {
				return "", "", err
			}

			key, err := c.connDevice.GroupStore.MasterKeyFromGroupIdentifier(ctx, gid)
			if err != nil {
				return "", "", fmt.Errorf("load cancellation key: %w", err)
			}

			return gid, key, nil
		},
		Check:      c.connectionLost,
		Invalidate: func(gid types.GroupIdentifier) { cli.GroupCache.Delete(gid) },
		Preview:    cli.PreviewGroupJoinRequest,
		Cancel:     cli.CancelGroupJoinRequestOnce,
	}

	result, err := cancelGroupRequestWithOperations(ctx, ops, ref)
	if err != nil {
		return result, GroupCancelRequestOperationError(c.lostOr(err), result)
	}

	return result, nil
}

type groupCancelRequestOperations struct {
	Resolve    func(context.Context, string) (types.GroupIdentifier, types.SerializedGroupMasterKey, error)
	Check      func() error
	Invalidate func(types.GroupIdentifier)
	Preview    func(context.Context, types.SerializedGroupMasterKey) (signalmeow.GroupJoinPreview, error)
	Cancel     func(context.Context, types.SerializedGroupMasterKey, uint32) (
		signalmeow.GroupJoinRequestCancelOutcome, error)
}

//nolint:nonamedreturns,funlen,cyclop // Sequential guards retain evidence without postreads or retry paths.
func cancelGroupRequestWithOperations(ctx context.Context, ops groupCancelRequestOperations, ref string,
) (result GroupCancelRequestResult, err error) {
	defer func() {
		if err != nil {
			err = GroupCancelRequestOperationError(err, result)
		}
	}()

	err = CheckGroupCancelRequestReference(ref)
	if err != nil {
		return result, err
	}

	err = groupCancelRequestBoundary(ctx, ops)
	if err != nil {
		return result, err
	}

	ctx = web.WithSensitiveRequestLogging(zerolog.Nop().WithContext(ctx))

	gid, key, err := resolveAcceptanceKey(ctx, groupAcceptOperations{Resolve: ops.Resolve}, ref)
	if err != nil {
		return result, err
	}

	result.ID = string(gid)

	ops.Invalidate(gid)
	defer ops.Invalidate(gid)

	err = groupCancelRequestBoundary(ctx, ops)
	if err != nil {
		return result, err
	}

	preview, err := ops.Preview(ctx, key)
	if err != nil {
		return result, groupCancelRequestForkError(err)
	}

	result.Title, result.Revision = preview.Title, preview.Revision
	result.Verified = !preview.PendingAdminApproval

	err = groupCancelRequestBoundary(ctx, ops)
	if err != nil {
		return result, err
	}

	if result.Verified {
		return result, nil
	}

	if preview.Revision == math.MaxUint32 {
		return result, errInvalidGroupChangeResponse
	}

	outcome, err := ops.Cancel(ctx, key, preview.Revision)
	if outcome.Attempted || outcome.Accepted {
		result.Revision = outcome.Revision
	}

	result.Accepted, result.Changed = outcome.Accepted, outcome.Accepted
	if err != nil {
		return result, groupCancelRequestForkError(err)
	}

	if !outcome.Attempted || !outcome.Accepted || !outcome.Verified || outcome.Revision != preview.Revision+1 ||
		outcome.GroupContext == nil || outcome.Change == nil {
		return result, errInvalidGroupChangeResponse
	}

	result.Verified = true

	return result, groupCancelRequestBoundary(ctx, ops)
}

func groupCancelRequestBoundary(ctx context.Context, ops groupCancelRequestOperations) error {
	err := ctx.Err()
	if err != nil {
		return fmt.Errorf("group request cancellation: %w", err)
	}

	if ops.Check != nil {
		return ops.Check()
	}

	return nil
}

func groupCancelRequestForkError(err error) error {
	switch {
	case errors.Is(err, signalmeow.ErrGroupCancellationUncertain):
		return errors.Join(ErrGroupUpdateUncertain, err)
	case errors.Is(err, signalmeow.ErrGroupCancellationInvalid):
		return errors.Join(errInvalidGroupChangeResponse, err)
	case errors.Is(err, signalmeow.ErrGroupCancellationTerminated):
		return errors.Join(ErrGroupTerminated, err)
	default:
		return groupAcceptForkError(err)
	}
}
