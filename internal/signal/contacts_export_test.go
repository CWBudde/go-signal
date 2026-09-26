//go:build cgo || purego

package signal

import (
	"context"
	"time"

	"github.com/cwbudde/go-signal/internal/store"
	"github.com/google/uuid"
	"go.mau.fi/mautrix-signal/pkg/signalmeow"
	"go.mau.fi/mautrix-signal/pkg/signalmeow/protobuf/signalpb"
	mstore "go.mau.fi/mautrix-signal/pkg/signalmeow/store"
	"go.mau.fi/mautrix-signal/pkg/signalmeow/types"
)

// BlockedList exposes blockedList to the signal_test package.
type BlockedList = blockedList

// BlockedFromStorage returns the complete blocked list of update, as SetBlocked reads it (see
// completeStorage).
func BlockedFromStorage(update *signalmeow.StorageUpdate) (*BlockedList, error) {
	seen, err := completeStorage(update)
	if err != nil {
		return nil, err
	}

	return seen.list, nil
}

// Set exposes blockedList.set to the signal_test package.
func (l *blockedList) Set(aci uuid.UUID, number string, blocked bool, at time.Time) {
	l.set(aci, number, blocked, at)
}

// SyncMessage exposes blockedList.syncMessage to the signal_test package.
func (l *blockedList) SyncMessage() *signalpb.SyncMessage {
	return l.syncMessage()
}

// meowOf returns the signalmeow-backed client behind client, which must come from Open, with
// the selected account's store open.
func meowOf(ctx context.Context, client Client) (*meowClient, *mstore.Device) {
	meow, ok := client.(*meowClient)
	if !ok {
		panic("not a signalmeow-backed client")
	}

	device, err := meow.device(ctx)
	if err != nil {
		panic(err)
	}

	return meow, device
}

// SettleOverrides runs settleOverrides on client (see meowOf) with what a fetch of the storage
// service found (nil: nothing fetched, as on Connect).
func SettleOverrides(ctx context.Context, client Client, update *signalmeow.StorageUpdate) {
	meow, device := meowOf(ctx, client)
	meow.settleOverrides(ctx, device.RecipientStore, readStorage(update))
}

// StorageSynced runs the handler's reaction to contacts changed by a background storage sync on
// client (see meowOf), with fetch in place of the storage service: it gets the manifest version
// asked about and returns nil for "still at that version".
func StorageSynced(ctx context.Context, client Client, changed []*types.Recipient,
	fetch func(ctx context.Context, since uint64) (*signalmeow.StorageUpdate, error),
) {
	meow, device := meowOf(ctx, client)
	meow.storageSyncedWith(ctx, device.RecipientStore, changed, fetch)
}

// ApplyBlocked runs SetBlocked's change on client (see meowOf) against update, the storage
// service as fetched, with send in place of sending the sync message. The recipients need their
// ACI.
func ApplyBlocked(ctx context.Context, client Client, update *signalmeow.StorageUpdate, recipients []Recipient,
	blocked bool, send func(*signalpb.SyncMessage) error,
) error {
	meow, device := meowOf(ctx, client)

	targets := make([]blockTarget, 0, len(recipients))
	for _, rcpt := range recipients {
		targets = append(targets, blockTarget{aci: uuid.MustParse(rcpt.ACI), number: rcpt.Number})
	}

	return meow.applyBlocked(ctx, device.RecipientStore, update, targets, blocked, send)
}

// BlockOverrides returns the block overrides in client's store (see meowOf).
func BlockOverrides(ctx context.Context, client Client) ([]store.BlockOverride, error) {
	meow, _ := meowOf(ctx, client)

	return meow.data.BlockOverrides(ctx) //nolint:wrapcheck // test helper
}
