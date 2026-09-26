//go:build cgo && !purego

package signal

import (
	"context"
	"time"

	"go.mau.fi/mautrix-signal/pkg/signalmeow/events"
	"go.mau.fi/mautrix-signal/pkg/signalmeow/protobuf/signalpb"
)

// ConnectOffline sets client (from Open) up as Connect does for the selected account, without
// taking the lock or starting the receive loops, so that the handler and the per-connection
// helpers can run without a server.
func ConnectOffline(ctx context.Context, client Client, opts ...ConnectOption) {
	meow, device := meowOf(ctx, client)

	acc, err := meow.selectAccount()
	if err != nil {
		panic(err)
	}

	meow.connDevice = device
	meow.trust = installTrust(device, meow.data, meow.log, time.Now)
	meow.ownACI = device.ACI.String()
	meow.account = acc
	meow.sendOnly = NewConnectOptions(opts...).SendOnly

	// What Connect's supervisor leaves for Close; without a signalmeow client, Close skips the
	// ack flush and the loop shutdown.
	supervised := make(chan struct{})
	close(supervised)

	meow.cancelLoops = func() {}
	meow.stopSupervisor = func() {}
	meow.supervised = supervised
}

// LoseConnection records a logout of client (see ConnectOffline), as the handler does in
// send-only mode.
func LoseConnection(client Client) {
	client.(*meowClient).noteConnection(&Connection{State: StateLoggedOut}) //nolint:forcetypeassert // test helper
}

// AddUpload registers an uploaded attachment on client (from Open) as Upload does, without the
// CDN.
func AddUpload(client Client, att OutgoingAttachment) UploadedAttachment {
	meow := client.(*meowClient) //nolint:forcetypeassert // test helper

	return meow.addUpload(pointerMetadata(&signalpb.AttachmentPointer{}, att, time.Now()))
}

// Handle runs signalmeow's event handler of client (from Open) on raw.
func Handle(client Client, raw events.SignalEvent) bool {
	return client.(*meowClient).handle(raw) //nolint:forcetypeassert // test helper
}

// Acked reports whether client (from Open) has acked an event.
func Acked(client Client) bool {
	return client.(*meowClient).acked.Load() //nolint:forcetypeassert // test helper
}

// LostOr runs lostOr of client (from Open).
func LostOr(client Client, err error) error {
	return client.(*meowClient).lostOr(err) //nolint:forcetypeassert // test helper
}

// CacheGroup runs cacheGroup of client (see meowOf).
func CacheGroup(ctx context.Context, client Client, group Group) {
	meow, _ := meowOf(ctx, client)
	meow.cacheGroup(ctx, group)
}

// UnavailableGroup runs unavailableGroup of client (see meowOf).
func UnavailableGroup(ctx context.Context, client Client, id string, err error) Group {
	meow, _ := meowOf(ctx, client)

	return meow.unavailableGroup(ctx, id, err)
}

// StoredMasterKey runs storedMasterKey of client (see ConnectOffline).
func StoredMasterKey(ctx context.Context, client Client) ([]byte, error) {
	return client.(*meowClient).storedMasterKey(ctx) //nolint:forcetypeassert // test helper
}

// OwnProfileKey runs ownProfileKey of client (see ConnectOffline).
func OwnProfileKey(ctx context.Context, client Client) []byte {
	return client.(*meowClient).ownProfileKey(ctx) //nolint:forcetypeassert // test helper
}

// ParseE164 exposes parseE164 to the signal_test package.
func ParseE164(number string) (uint64, error) {
	return parseE164(number)
}

// ErrInvalidNumber exposes errInvalidNumber to the signal_test package.
var ErrInvalidNumber = errInvalidNumber

// SyncErrors joins errs as Sync reports the reasons of an incomplete sync.
func SyncErrors(errs ...error) error {
	return syncErrors(errs)
}
