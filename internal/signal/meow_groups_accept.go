//go:build cgo || libsignal_go

package signal

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/cwbudde/go-signal/internal/store"
	"github.com/cwbudde/mautrix-signal/pkg/libsignalgo"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow/protobuf/signalpb"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow/types"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow/web"
	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"google.golang.org/protobuf/proto"
)

func (c *meowClient) AcceptGroupInvitation(ctx context.Context, ref string) (GroupAcceptResult, error) {
	err := CheckGroupAcceptReference(ref)
	if err != nil {
		return GroupAcceptResult{}, GroupAcceptOperationError(err, GroupAcceptResult{})
	}

	err = groupAcceptContextError(ctx)
	if err != nil {
		return GroupAcceptResult{}, GroupAcceptOperationError(err, GroupAcceptResult{})
	}

	c.mu.Lock()

	closing := c.closing

	c.mu.Unlock()

	if closing {
		return GroupAcceptResult{}, GroupAcceptOperationError(ErrClosed, GroupAcceptResult{})
	}

	cli, done, err := c.groupClient("accept group invitation")
	if err != nil {
		return GroupAcceptResult{}, GroupAcceptOperationError(err, GroupAcceptResult{})
	}

	defer done()

	ops := groupAcceptOperations{
		Resolve: func(ctx context.Context, ref string) (types.GroupIdentifier, types.SerializedGroupMasterKey, error) {
			gid, err := resolveGroupRef(ctx, c.connDevice.GroupStore, NormalizeGroupAcceptReference(ref))
			if err != nil {
				return "", "", err
			}

			key, err := c.connDevice.GroupStore.MasterKeyFromGroupIdentifier(ctx, gid)
			if err != nil {
				return "", "", fmt.Errorf("load invitation key: %w", err)
			}

			return gid, key, nil
		},
		Invalidate: func(gid types.GroupIdentifier) { cli.GroupCache.Delete(gid) },
		Fetch:      cli.FetchGroupForAcceptance,
		Accept:     cli.AcceptGroupInvitationOnce,
		Cache: func(ctx context.Context, group Group) error {
			return c.data.PutGroup(ctx, store.GroupRecord{
				ID: group.ID, Title: group.Title, Revision: group.Revision, UpdatedAt: time.Now(),
			})
		},
		Notify: cli.SendGroupUpdate,
	}

	self := Recipient{ACI: c.ownACI, PNI: c.connDevice.PNI.String()}

	result, err := acceptGroupWithOperations(ctx, ops, self, ref)
	if err != nil {
		return result, GroupAcceptOperationError(c.lostOr(err), result)
	}

	return result, nil
}

type groupAcceptOperations struct {
	Resolve    func(context.Context, string) (types.GroupIdentifier, types.SerializedGroupMasterKey, error)
	Invalidate func(types.GroupIdentifier)
	Fetch      func(context.Context, types.SerializedGroupMasterKey) (*signalmeow.Group, error)
	Accept     func(context.Context, types.SerializedGroupMasterKey, uint32,
		libsignalgo.ServiceID) (signalmeow.GroupInvitationAcceptOutcome, error)
	Cache  func(context.Context, Group) error
	Notify func(context.Context, *signalmeow.Group, *signalpb.GroupContextV2,
		*signalmeow.GroupChange) (*signalmeow.GroupMessageSendResult, error)
}

//nolint:nonamedreturns,cyclop,funlen // Sequential guards preserve redacted evidence at each acceptance stage.
func acceptGroupWithOperations(ctx context.Context, ops groupAcceptOperations, self Recipient, ref string,
) (result GroupAcceptResult, err error) {
	defer func() {
		if err != nil {
			err = GroupAcceptOperationError(err, result)
		}
	}()

	err = CheckGroupAcceptReference(ref)
	if err != nil {
		return result, err
	}

	err = groupAcceptContextError(ctx)
	if err != nil {
		return result, err
	}

	ctx = web.WithSensitiveRequestLogging(zerolog.Nop().WithContext(ctx))

	gid, key, err := resolveAcceptanceKey(ctx, ops, ref)
	if err != nil {
		return result, err
	}

	result.ID = string(gid)

	ops.Invalidate(gid)

	defer ops.Invalidate(gid)

	err = groupAcceptContextError(ctx)
	if err != nil {
		return result, err
	}

	raw, err := ops.Fetch(ctx, key)
	if err != nil {
		return result, groupAcceptForkError(err)
	}

	err = checkAcceptSnapshot(raw, gid, key)
	if err != nil {
		return result, err
	}

	group := convertGroup(raw, self)

	result.Revision = group.Revision

	invited, noop, err := group.CheckAcceptInvitation(self)
	if err != nil {
		return result, err
	}

	if noop {
		result.Title, result.Revision, result.Verified = group.Title, group.Revision, true
	}

	err = groupAcceptContextError(ctx)
	if err != nil {
		return result, err
	}

	if noop {
		return result, ops.Cache(ctx, group)
	}

	return submitGroupAcceptance(ctx, ops, self, gid, key, group, invited, result)
}

func submitGroupAcceptance(ctx context.Context, ops groupAcceptOperations, self Recipient,
	gid types.GroupIdentifier, key types.SerializedGroupMasterKey,
	group Group, invited Recipient, result GroupAcceptResult,
) (GroupAcceptResult, error) {
	serviceID := acceptServiceID(invited)

	outcome, err := ops.Accept(ctx, key, group.Revision, serviceID)

	if outcome.Attempted || outcome.Accepted {
		result.Revision = outcome.Revision
	}

	result.Accepted, result.Changed = outcome.Accepted, outcome.Accepted

	if err != nil {
		return result, groupAcceptForkError(err)
	}

	if !outcome.Accepted || !outcome.Verified || outcome.Revision != group.Revision+1 ||
		outcome.GroupContext == nil || outcome.Change == nil {
		return result, errInvalidGroupChangeResponse
	}

	return completeGroupAcceptance(ctx, ops, self, gid, key, outcome, result)
}

// resolveAcceptanceKey exposes an ID only after binding it to the selected stored key.
func resolveAcceptanceKey(ctx context.Context, ops groupAcceptOperations, ref string,
) (types.GroupIdentifier, types.SerializedGroupMasterKey, error) {
	gid, key, err := ops.Resolve(ctx, ref)
	if err != nil {
		return "", "", err
	}

	master, err := base64.StdEncoding.DecodeString(string(key))
	if err != nil || len(master) != groupKeyLen {
		return "", "", errInvalidGroupChangeResponse
	}

	derived, err := groupIDFromMasterKey(master)
	if err != nil {
		return "", "", err
	}

	if gid != derived {
		return "", "", errInvalidGroupChangeResponse
	}

	return gid, key, nil
}

//nolint:cyclop // Cancellation guards retain accepted and verified evidence through follow-ups.
func completeGroupAcceptance(ctx context.Context, ops groupAcceptOperations, self Recipient,
	gid types.GroupIdentifier, key types.SerializedGroupMasterKey,
	outcome signalmeow.GroupInvitationAcceptOutcome, result GroupAcceptResult,
) (GroupAcceptResult, error) {
	err := groupAcceptContextError(ctx)
	if err != nil {
		return result, err
	}

	ops.Invalidate(gid)

	raw, err := ops.Fetch(ctx, key)
	if err != nil {
		return result, groupAcceptForkError(err)
	}

	err = checkAcceptSnapshot(raw, gid, key)
	if err != nil {
		return result, err
	}

	if raw.Revision < outcome.Revision {
		return result, errInvalidGroupChangeResponse
	}

	group := convertGroup(raw, self)

	_, noop, err := group.CheckAcceptInvitation(self)
	if err != nil || !noop {
		return result, ErrNotAMember
	}

	result.Title, result.Revision, result.Verified = group.Title, group.Revision, true

	err = groupAcceptContextError(ctx)
	if err != nil {
		return result, err
	}

	err = ops.Cache(ctx, group)
	if err != nil {
		return result, err
	}

	err = groupAcceptContextError(ctx)
	if err != nil {
		return result, err
	}
	// Clone preserves the input type.

	groupContext := proto.Clone(outcome.GroupContext).(*signalpb.GroupContextV2) //nolint:forcetypeassert

	notification, err := ops.Notify(ctx, raw, groupContext, cloneAcceptChange(outcome.Change))
	if err != nil {
		return result, err
	}

	return result, groupJoinNotificationError(notification)
}

func acceptServiceID(invited Recipient) libsignalgo.ServiceID {
	if invited.ACI != "" {
		return libsignalgo.NewACIServiceID(acceptIdentity(invited.ACI))
	}

	return libsignalgo.NewPNIServiceID(acceptIdentity(invited.PNI))
}

// checkAcceptSnapshot keeps malformed injected backend records from being silently skipped
// by ordinary group conversion. The dedicated reader already validates its wire records.
//
//nolint:cyclop // Every record is checked before ordinary conversion can skip malformed data.
func checkAcceptSnapshot(raw *signalmeow.Group, gid types.GroupIdentifier, key types.SerializedGroupMasterKey) error {
	if raw == nil || raw.GroupIdentifier != gid || raw.GroupMasterKey != key {
		return errInvalidGroupChangeResponse
	}

	for _, member := range raw.Members {
		if member == nil || member.ACI == uuid.Nil {
			return errInvalidGroupChangeResponse
		}
	}

	for _, pending := range raw.PendingMembers {
		if pending == nil || !acceptValidServiceID(pending.ServiceID) {
			return errInvalidGroupChangeResponse
		}
	}

	for _, request := range raw.RequestingMembers {
		if request == nil || request.ACI == uuid.Nil {
			return errInvalidGroupChangeResponse
		}
	}

	for _, banned := range raw.BannedMembers {
		if banned == nil || !acceptValidServiceID(banned.ServiceID) {
			return errInvalidGroupChangeResponse
		}
	}

	return nil
}

func acceptValidServiceID(id libsignalgo.ServiceID) bool {
	return id.UUID != uuid.Nil && (id.Type == libsignalgo.ServiceIDTypeACI || id.Type == libsignalgo.ServiceIDTypePNI)
}

// The fork's verified acceptance change owns only the single promotion and scalar metadata.
// Copy those records before notifications, retaining their exact profile keys and typed IDs.
func cloneAcceptChange(change *signalmeow.GroupChange) *signalmeow.GroupChange {
	copied := *change

	copied.PromotePendingMembers = slices.Clone(change.PromotePendingMembers)

	for i, promotion := range copied.PromotePendingMembers {
		if promotion != nil {
			member := *promotion

			copied.PromotePendingMembers[i] = &member
		}
	}

	copied.PromotePendingPniAciMembers = slices.Clone(change.PromotePendingPniAciMembers)

	for i, promotion := range copied.PromotePendingPniAciMembers {
		if promotion != nil {
			member := *promotion

			copied.PromotePendingPniAciMembers[i] = &member
		}
	}

	return &copied
}

func groupAcceptForkError(err error) error {
	switch {
	case errors.Is(err, signalmeow.ErrGroupAcceptanceUncertain):
		return errors.Join(ErrGroupUpdateUncertain, err)

	case errors.Is(err, signalmeow.ErrGroupAcceptanceInvalid):
		return errors.Join(errInvalidGroupChangeResponse, err)

	case errors.Is(err, signalmeow.ErrGroupAcceptanceTerminated):
		return errors.Join(ErrGroupTerminated, err)

	case errors.Is(err, signalmeow.ConflictError), errors.Is(err, signalmeow.ContactManifestMismatchError):
		return errors.Join(ErrGroupChanged, err)

	case errors.Is(err, signalmeow.AuthorizationFailedError):
		return errors.Join(ErrNotAMember, err)

	case errors.Is(err, signalmeow.NotFoundError), errors.Is(err, signalmeow.ErrGroupMasterKeyNotFound):
		return errors.Join(ErrUnknownGroup, err)

	default:
		return err
	}
}

func groupAcceptContextError(ctx context.Context) error {
	err := ctx.Err()
	if err != nil {
		return fmt.Errorf("group acceptance: %w", err)
	}

	return nil
}
