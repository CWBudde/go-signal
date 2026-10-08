//go:build cgo || libsignal_go

package signal

import (
	"context"
	"fmt"

	"github.com/cwbudde/mautrix-signal/pkg/libsignalgo"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow/events"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow/protobuf/signalpb"
	"github.com/google/uuid"
)

// handleIdentityVerification consumes the fork's authenticated, validated own-device update.
// It changes only exact known ACI trust, never imports a key, emits an event or sends a sync.
// Persist in send-only mode too, as with other internal phone-store updates.
func (c *meowClient) handleIdentityVerification(update *events.IdentityVerification) bool {
	if update == nil || update.ACI == uuid.Nil || update.ACI.String() == c.ownACI {
		return true
	}

	var level TrustLevel

	switch update.State {
	case signalpb.Verified_DEFAULT:
		level = TrustUnverified
	case signalpb.Verified_VERIFIED:
		level = TrustVerified
	case signalpb.Verified_UNVERIFIED:
		level = TrustUntrusted
	default:
		return true
	}

	ctx, cancel := context.WithTimeout(c.zlog.WithContext(context.Background()), overrideSettleTimeout)
	defer cancel()

	err := c.applyIdentityVerification(ctx, update, level)
	if err != nil {
		c.log.Warn("persist identity verification", "recipient", update.ACI.String(), "error", err)
		return false
	}
	// Even an ignored stale key consumes this internal envelope and needs the Close ACK flush.
	c.acked.Store(true)

	return true
}

// applyIdentityVerification keeps matching trust and stale-session cleanup atomic.
func (c *meowClient) applyIdentityVerification(
	ctx context.Context, update *events.IdentityVerification, level TrustLevel,
) error {
	err := c.connDevice.DoDecryptionTxn(ctx, func(ctx context.Context) error {
		applied, err := c.data.ApplyIdentityTrust(ctx, c.ownACI, update.ACI.String(), update.IdentityKey, level.String())
		if err != nil {
			return fmt.Errorf("apply verification: %w", err)
		}

		if !applied {
			return nil
		}
		// A legacy old-key session would otherwise overwrite the new trust decision on send.
		_, err = removeStaleSessions(ctx, c.trust.sessions, libsignalgo.NewACIServiceID(update.ACI), update.IdentityKey)

		return err
	})
	if err != nil {
		return fmt.Errorf("identity verification transaction: %w", err)
	}

	return nil
}
