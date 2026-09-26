//go:build cgo

package signal

import (
	"context"
	"time"

	"github.com/cwbudde/go-signal/internal/store"
	"github.com/google/uuid"
	"go.mau.fi/mautrix-signal/pkg/libsignalgo"
	"go.mau.fi/mautrix-signal/pkg/signalmeow"
	"go.mau.fi/mautrix-signal/pkg/signalmeow/events"
	"go.mau.fi/mautrix-signal/pkg/signalmeow/protobuf/signalpb"
	mstore "go.mau.fi/mautrix-signal/pkg/signalmeow/store"
	"go.mau.fi/mautrix-signal/pkg/signalmeow/types"
)

// ConvertEvent exposes convertEvent to the signal_test package.
func ConvertEvent(raw events.SignalEvent, ownACI string) Event {
	return convertEvent(raw, ownACI)
}

// ConvertStatus exposes convertStatus to the signal_test package.
func ConvertStatus(status signalmeow.SignalConnectionStatus) Event {
	return convertStatus(status)
}

// DevicesFromResponse exposes devicesFromResponse to the signal_test package.
func DevicesFromResponse(body []byte, keys *libsignalgo.IdentityKeyPair, ownID int) ([]Device, error) {
	return devicesFromResponse(body, keys, ownID)
}

// HPKESeal exposes hpkeSeal to the signal_test package.
func HPKESeal(publicKey, plaintext, info, associatedData []byte) ([]byte, error) {
	return hpkeSeal(publicKey, plaintext, info, associatedData)
}

// HPKEOpen exposes hpkeOpen to the signal_test package.
func HPKEOpen(privateKey, ciphertext, info, associatedData []byte) ([]byte, error) {
	return hpkeOpen(privateKey, ciphertext, info, associatedData)
}

// ConvertLoopStatus exposes convertLoopStatus to the signal_test package.
func ConvertLoopStatus(status signalmeow.SignalConnectionStatus) LoopStatus {
	return convertLoopStatus(status)
}

// UsernameHash exposes usernameHash to the signal_test package.
func UsernameHash(username string) ([]byte, error) {
	return usernameHash(username)
}

// ACIFromUsernameResponse exposes aciFromUsernameResponse to the signal_test package.
func ACIFromUsernameResponse(status int, body []byte) (uuid.UUID, error) {
	return aciFromUsernameResponse(status, body)
}

// DataMessage exposes dataMessage to the signal_test package.
func DataMessage(req SendRequest, attachments []*signalpb.AttachmentPointer, profileKey []byte,
) (*signalpb.DataMessage, error) {
	return dataMessage(req, attachments, profileKey)
}

// PointerMetadata exposes pointerMetadata to the signal_test package.
func PointerMetadata(pointer *signalpb.AttachmentPointer, att OutgoingAttachment, now time.Time,
) *signalpb.AttachmentPointer {
	return pointerMetadata(pointer, att, now)
}

// ConvertRecipientResult exposes recipientResult to the signal_test package.
func ConvertRecipientResult(rcpt Recipient, self bool, sent signalmeow.SendMessageResult) RecipientResult {
	return recipientResult(rcpt, self, sent)
}

// GroupResults exposes groupResults to the signal_test package.
func GroupResults(sent *signalmeow.GroupMessageSendResult) []RecipientResult {
	return groupResults(sent)
}

// ACIServiceID exposes aciServiceID to the signal_test package.
func ACIServiceID(rcpt Recipient) (libsignalgo.ServiceID, error) {
	return aciServiceID(rcpt)
}

// ReceiptContent exposes receiptContent to the signal_test package.
func ReceiptContent(typ ReceiptType, timestamps []uint64) (*signalpb.Content, error) {
	return receiptContent(typ, timestamps)
}

// ContactListHook registers a contact list waiter on client, which must come from Open (see
// Sync), and returns it with the client's signalmeow event handler and the unregister function.
func ContactListHook(client Client) (<-chan int, func(events.SignalEvent) bool, func()) {
	meow, ok := client.(*meowClient)
	if !ok {
		panic("ContactListHook: not a signalmeow-backed client")
	}

	arrived, stop := meow.awaitContactList()

	return arrived, meow.handle, stop
}

// SyncCounts counts what Sync would report for the selected account of client, which must come
// from Open, without connecting.
func SyncCounts(ctx context.Context, client Client) (int, int, error) {
	meow, ok := client.(*meowClient)
	if !ok {
		panic("SyncCounts: not a signalmeow-backed client")
	}

	device, err := meow.device(ctx)
	if err != nil {
		return 0, 0, err
	}

	return meow.syncCounts(ctx, device)
}

// BlockedList exposes blockedList to the signal_test package.
type BlockedList = blockedList

// BlockedFromStorage exposes blockedFromStorage to the signal_test package.
func BlockedFromStorage(update *signalmeow.StorageUpdate) (*BlockedList, error) {
	return blockedFromStorage(update)
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

// SettleOverrides runs settleOverrides on client (see meowOf); storage has the blocked state the
// storage service has, by ACI (nil: unknown).
func SettleOverrides(ctx context.Context, client Client, storage map[string]bool) {
	meow, device := meowOf(ctx, client)

	var state storageState
	if storage != nil {
		state = func(aci string) (bool, bool) {
			blocked, ok := storage[aci]

			return blocked, ok
		}
	}

	meow.settleOverrides(ctx, device.RecipientStore, state)
}

// StorageSynced runs the handler's reaction to contacts changed by a storage sync on client (see
// meowOf).
func StorageSynced(ctx context.Context, client Client, changed []*types.Recipient) {
	meow, device := meowOf(ctx, client)
	meow.storageSynced(ctx, device, changed)
}

// BlockOverrides returns the block overrides in client's store (see meowOf).
func BlockOverrides(ctx context.Context, client Client) ([]store.BlockOverride, error) {
	meow, _ := meowOf(ctx, client)

	return meow.data.BlockOverrides(ctx) //nolint:wrapcheck // test helper
}
