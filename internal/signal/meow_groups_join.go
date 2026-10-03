//go:build cgo || libsignal_go

package signal

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/cwbudde/go-signal/internal/signal/groupinvite"
	"github.com/cwbudde/go-signal/internal/store"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow/protobuf/signalpb"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow/types"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow/web"
	"github.com/rs/zerolog"
)

func (c *meowClient) JoinGroup(ctx context.Context, link string) (GroupJoinResult, error) {
	err := CheckGroupInviteLink(link)
	if err != nil {
		return GroupJoinResult{}, err
	}

	c.mu.Lock()
	closing := c.closing
	c.mu.Unlock()

	if closing {
		return GroupJoinResult{}, GroupJoinOperationError(ErrClosed, GroupJoinResult{})
	}

	cli, done, err := c.groupClient("join group")
	if err != nil {
		return GroupJoinResult{}, GroupJoinOperationError(err, GroupJoinResult{})
	}

	defer done()

	ops := groupJoinOperations{
		Known: func(ctx context.Context, gid types.GroupIdentifier) (bool, error) {
			return knownGroup(ctx, c.connDevice.GroupStore, gid)
		},
		Persist:    c.connDevice.GroupStore.StoreMasterKey,
		Invalidate: func(gid types.GroupIdentifier) { cli.GroupCache.Delete(gid) },
		Fetch: func(ctx context.Context, gid types.GroupIdentifier, revision uint32) (*signalmeow.Group, error) {
			raw, _, err := cli.RetrieveGroupByID(ctx, gid, revision)
			if err != nil {
				return nil, groupFetchError(gid, err)
			}

			return raw, nil
		},
		Cache: func(ctx context.Context, group Group) error {
			return c.data.PutGroup(ctx, store.GroupRecord{
				ID: group.ID, Title: group.Title, Revision: group.Revision, LeftAt: group.LeftAt, UpdatedAt: time.Now(),
			})
		},
		Preview: cli.PreviewGroupJoin,
		Join:    cli.JoinGroupOnce,
		Notify:  cli.SendGroupUpdate,
	}

	result, err := joinGroupWithOperations(ctx, ops, c.ownACI, link)
	if err != nil {
		return result, GroupJoinOperationError(c.lostOr(err), result)
	}

	return result, nil
}

type groupJoinOperations struct {
	Known      func(context.Context, types.GroupIdentifier) (bool, error)
	Persist    func(context.Context, types.GroupIdentifier, types.SerializedGroupMasterKey) error
	Invalidate func(types.GroupIdentifier)
	Fetch      func(context.Context, types.GroupIdentifier, uint32) (*signalmeow.Group, error)
	Cache      func(context.Context, Group) error
	Preview    func(context.Context, types.SerializedGroupMasterKey, []byte) (signalmeow.GroupJoinPreview, error)
	Join       func(context.Context, types.SerializedGroupMasterKey, []byte,
		signalmeow.GroupJoinPreview) (signalmeow.GroupJoinOutcome, error)
	Notify func(context.Context, *signalmeow.Group, *signalpb.GroupContextV2,
		*signalmeow.GroupChange) (*signalmeow.GroupMessageSendResult, error)
}

// joinGroupWithOperations keeps all stages under the caller's cancellation and sensitive
// logging policy. It never writes preview state to the full-state title cache.
//
//nolint:nonamedreturns // Deferred error redaction must observe the final partial result.
func joinGroupWithOperations(ctx context.Context, ops groupJoinOperations, self, link string) (
	result GroupJoinResult, err error,
) {
	defer func() {
		if err != nil {
			err = GroupJoinOperationError(err, result)
		}
	}()

	invite, err := groupinvite.Parse(link)
	if err != nil {
		return result, ErrInvalidGroupInviteLink
	}

	err = joinContextError(ctx)
	if err != nil {
		return result, err
	}

	ctx = web.WithSensitiveRequestLogging(zerolog.Nop().WithContext(ctx))

	gid, err := groupIDFromMasterKey(invite.MasterKey)
	if err != nil {
		return result, err
	}

	key := types.SerializedGroupMasterKey(base64.StdEncoding.EncodeToString(invite.MasterKey))
	result.ID = string(gid)

	ops.Invalidate(gid)
	defer ops.Invalidate(gid)

	existing, err := knownGroupJoin(ctx, ops, self, gid)
	if existing.Verified {
		return existing, err
	}

	if err != nil {
		return result, err
	}

	return submitGroupJoin(ctx, ops, self, gid, key, invite.Password, result)
}

// knownGroupJoin authorizes a no-op only from fresh full state.
//
//nolint:cyclop // Sequential lifecycle guards preserve distinct failure outcomes.
func knownGroupJoin(ctx context.Context, ops groupJoinOperations, self string,
	gid types.GroupIdentifier,
) (GroupJoinResult, error) {
	known, err := ops.Known(ctx, gid)
	if err != nil || !known {
		return GroupJoinResult{}, err
	}

	raw, err := ops.Fetch(ctx, gid, 0)
	if errors.Is(err, ErrNotAMember) || errors.Is(err, ErrUnknownGroup) {
		return GroupJoinResult{}, nil
	}

	if err != nil {
		return GroupJoinResult{}, err
	}

	if raw == nil || raw.GroupIdentifier != gid {
		return GroupJoinResult{}, errInvalidGroupChangeResponse
	}

	group := convertGroup(raw, self)
	if group.Membership == MembershipPending {
		return GroupJoinResult{}, ErrGroupInvitationRequiresAcceptance
	}

	if group.Membership != MembershipMember {
		return GroupJoinResult{}, nil
	}

	err = joinContextError(ctx)
	if err != nil {
		return GroupJoinResult{}, err
	}

	result := GroupJoinResult{
		ID: group.ID, Title: group.Title, Revision: group.Revision, Status: GroupJoinMember, Verified: true,
	}
	err = ops.Cache(ctx, group)

	return result, err
}

//nolint:cyclop // Sequential lifecycle guards preserve distinct failure outcomes.
func submitGroupJoin(ctx context.Context, ops groupJoinOperations, self string, gid types.GroupIdentifier,
	key types.SerializedGroupMasterKey, password []byte, result GroupJoinResult,
) (GroupJoinResult, error) {
	err := joinContextError(ctx)
	if err != nil {
		return result, err
	}

	preview, err := ops.Preview(ctx, key, password)
	if err != nil {
		return result, groupJoinForkError(err)
	}

	result.Revision = preview.Revision
	if !preview.PendingAdminApproval {
		if preview.Access != signalmeow.AccessControl_ANY && preview.Access != signalmeow.AccessControl_ADMINISTRATOR {
			return result, ErrGroupLinkInactive
		}

		if preview.Revision == math.MaxUint32 {
			return result, ErrInvalidGroupInviteLink
		}
	}

	err = joinContextError(ctx)
	if err != nil {
		return result, err
	}

	err = ops.Persist(ctx, gid, key)
	if err != nil {
		return result, err
	}

	if preview.PendingAdminApproval {
		result.Title, result.Status, result.Verified = preview.Title, GroupJoinRequesting, true

		return result, nil
	}

	err = joinContextError(ctx)
	if err != nil {
		return result, err
	}

	outcome, err := ops.Join(ctx, key, password, preview)
	if outcome.Attempted || outcome.Accepted {
		result.Revision = outcome.Revision
	}

	result.Accepted, result.Changed = outcome.Accepted, outcome.Accepted
	if err != nil {
		return result, groupJoinForkError(err)
	}

	return completeGroupJoin(ctx, ops, self, gid, preview, outcome, result)
}

func completeGroupJoin(ctx context.Context, ops groupJoinOperations, self string, gid types.GroupIdentifier,
	preview signalmeow.GroupJoinPreview, outcome signalmeow.GroupJoinOutcome, result GroupJoinResult,
) (GroupJoinResult, error) {
	if !outcome.Accepted || !outcome.Verified || outcome.GroupContext == nil || outcome.Change == nil {
		return result, errInvalidGroupChangeResponse
	}

	if outcome.Requesting {
		if preview.Access != signalmeow.AccessControl_ADMINISTRATOR {
			return result, errInvalidGroupChangeResponse
		}

		result.Title, result.Status, result.Verified = preview.Title, GroupJoinRequesting, true

		return result, nil
	}

	if preview.Access != signalmeow.AccessControl_ANY {
		return result, errInvalidGroupChangeResponse
	}

	return verifyDirectGroupJoin(ctx, ops, self, gid, outcome, result)
}

//nolint:cyclop // Sequential lifecycle guards preserve distinct failure outcomes.
func verifyDirectGroupJoin(ctx context.Context, ops groupJoinOperations, self string, gid types.GroupIdentifier,
	outcome signalmeow.GroupJoinOutcome, result GroupJoinResult,
) (GroupJoinResult, error) {
	err := joinContextError(ctx)
	if err != nil {
		return result, err
	}

	ops.Invalidate(gid)

	raw, err := ops.Fetch(ctx, gid, outcome.Revision)
	if err != nil {
		return result, err
	}

	if raw == nil || raw.GroupIdentifier != gid || raw.Revision < outcome.Revision {
		return result, errInvalidGroupChangeResponse
	}

	group := convertGroup(raw, self)
	if group.Membership != MembershipMember {
		return result, ErrNotAMember
	}

	err = joinContextError(ctx)
	if err != nil {
		return result, err
	}

	result.Title, result.Status, result.Verified, result.Revision = group.Title, GroupJoinMember, true, group.Revision

	err = ops.Cache(ctx, group)
	if err != nil {
		return result, err
	}

	err = joinContextError(ctx)
	if err != nil {
		return result, err
	}

	notification, err := ops.Notify(ctx, raw, outcome.GroupContext, outcome.Change)
	if err != nil {
		return result, err
	}

	return result, groupJoinNotificationError(notification)
}

func groupJoinNotificationError(notification *signalmeow.GroupMessageSendResult) error {
	if notification == nil {
		return errInvalidGroupChangeResponse
	}

	if len(notification.FailedToSendTo) == 0 {
		return nil
	}

	var failures []error

	for _, failure := range notification.FailedToSendTo {
		if failure.Error == nil {
			failures = append(failures, errInvalidGroupChangeResponse)
		} else {
			failures = append(failures, failure.Error)
		}
	}

	return errors.Join(failures...)
}

func groupJoinForkError(err error) error {
	switch {
	case errors.Is(err, signalmeow.ErrGroupJoinUncertain):
		return errors.Join(ErrGroupUpdateUncertain, err)
	case errors.Is(err, signalmeow.ConflictError), errors.Is(err, signalmeow.ContactManifestMismatchError):
		return errors.Join(ErrGroupChanged, err)
	case errors.Is(err, signalmeow.ErrGroupJoinTerminated):
		return errors.Join(ErrGroupTerminated, err)
	case errors.Is(err, signalmeow.ErrGroupJoinInactive), errors.Is(err, signalmeow.AuthorizationFailedError),
		errors.Is(err, signalmeow.NotFoundError):
		return errors.Join(ErrGroupLinkInactive, err)
	default:
		return err
	}
}

func joinContextError(ctx context.Context) error {
	err := ctx.Err()
	if err != nil {
		return fmt.Errorf("group join: %w", err)
	}

	return nil
}
