//go:build cgo || libsignal_go

package signal

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/cwbudde/go-signal/internal/store"
	"github.com/cwbudde/mautrix-signal/pkg/libsignalgo"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow/protobuf/signalpb"
	"github.com/google/uuid"
)

// identitySyncTimeout bounds an entire best-effort flush, including waiting for another flush.
const identitySyncTimeout = 30 * time.Second

var (
	errInvalidIdentitySync         = errors.New("invalid pending ACI identity verification")
	errIdentityVerificationPending = errors.New("identity verification sync pending")
)

// flushIdentityVerification does not change local trust when delivery fails. No database
// transaction spans network IO; a newly queued decision cannot be cleared by an older send.
func (c *meowClient) flushIdentityVerification(
	ctx context.Context, send func(context.Context, *signalpb.SyncMessage) error,
) error {
	ctx, cancel := context.WithTimeout(c.zlog.WithContext(ctx), identitySyncTimeout)
	defer cancel()

	err := c.identitySyncMu.lock(ctx)
	if err != nil {
		return fmt.Errorf("wait for identity verification sync: %w", err)
	}
	defer c.identitySyncMu.unlock()

	device, err := c.storeDevice(ctx)
	if err != nil {
		return err
	}

	updates, err := c.data.PendingIdentitySync(ctx, device.ACI.String())
	if err != nil {
		return fmt.Errorf("pending identity verification: %w", err)
	}

	for _, update := range updates {
		err = c.sendIdentityVerification(ctx, device.ACI.String(), update, send)
		if err != nil {
			return err
		}
	}

	return nil
}

func (c *meowClient) sendIdentityVerification(ctx context.Context, accountID string, update store.IdentitySync,
	send func(context.Context, *signalpb.SyncMessage) error,
) error {
	current, err := c.data.IdentitySyncCurrent(ctx, accountID, update)
	if err != nil {
		return fmt.Errorf("current identity verification: %w", err)
	}

	if !current {
		return nil
	}

	msg, err := identityVerificationMessage(update)
	if err != nil {
		return err
	}

	err = send(ctx, msg)
	if err != nil {
		return fmt.Errorf("send identity verification for %s: %w", update.ServiceID, err)
	}

	return c.data.MarkIdentitySynced(ctx, update) //nolint:wrapcheck // store error names the operation
}

func identityVerificationMessage(update store.IdentitySync) (*signalpb.SyncMessage, error) {
	aci, err := uuid.Parse(update.ServiceID)
	if err != nil || len(update.ServiceID) != 36 || aci == uuid.Nil {
		return nil, errInvalidIdentitySync
	}

	if len(update.Key) != 33 || update.Key[0] != 5 {
		return nil, errInvalidIdentitySync
	}

	_, err = libsignalgo.DeserializeIdentityKey(update.Key)
	if err != nil {
		return nil, fmt.Errorf("pending verification key: %w", err)
	}

	var state signalpb.Verified_State

	switch ParseTrustLevel(update.Trust) {
	case TrustUnverified:
		state = signalpb.Verified_DEFAULT
	case TrustVerified:
		state = signalpb.Verified_VERIFIED
	case TrustUntrusted:
		return nil, errInvalidIdentitySync
	}

	return &signalpb.SyncMessage{Content: &signalpb.SyncMessage_Verified{Verified: &signalpb.Verified{
		DestinationAci: new(aci.String()), IdentityKey: update.Key, State: &state,
	}}}, nil
}

// syncPendingIdentityVerification is called only by operations whose lifetime Close tracks.
func (c *meowClient) syncPendingIdentityVerification(ctx context.Context) error {
	c.cliMu.Lock()
	cli := c.cli
	c.cliMu.Unlock()

	if cli == nil {
		// Offline trust queues without opening a connection.
		return nil
	}

	return c.flushIdentityVerification(ctx, func(ctx context.Context, msg *signalpb.SyncMessage) error {
		return profileWithCancel(ctx, cli.AuthedWS.ForceReconnect, func() error {
			return sendSyncMessage(ctx, cli, msg)
		})
	})
}
