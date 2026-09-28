//go:build cgo || libsignal_go

package signal

import (
	"context"
	"log/slog"
	"time"

	"github.com/cwbudde/go-signal/internal/store"
	"github.com/cwbudde/mautrix-signal/pkg/libsignalgo"
	mstore "github.com/cwbudde/mautrix-signal/pkg/signalmeow/store"
	"github.com/google/uuid"
)

// IdentityTrust exposes identityTrust to the signal_test package.
type IdentityTrust = identityTrust

// InstallTrust exposes installTrust to the signal_test package.
func InstallTrust(device *mstore.Device, data *store.Store, log *slog.Logger, now func() time.Time) *IdentityTrust {
	return installTrust(device, data, log, now)
}

// Report exposes identityTrust.report to the signal_test package.
func (t *identityTrust) Report(ctx context.Context, emit func(Event) bool) bool {
	return t.report(ctx, emit)
}

// ComputeSafetyNumber exposes computeSafetyNumber to the signal_test package.
func ComputeSafetyNumber(
	ownACI uuid.UUID, ownKey *libsignalgo.PublicKey, theirACI uuid.UUID, theirKey []byte,
) (string, []byte, error) {
	return computeSafetyNumber(ownACI, ownKey, theirACI, theirKey)
}
