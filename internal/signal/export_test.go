//go:build cgo || purego

package signal

import (
	"context"
	"time"

	"github.com/google/uuid"
	"go.mau.fi/mautrix-signal/pkg/libsignalgo"
	"go.mau.fi/mautrix-signal/pkg/signalmeow"
	"go.mau.fi/mautrix-signal/pkg/signalmeow/events"
	"go.mau.fi/mautrix-signal/pkg/signalmeow/protobuf/signalpb"
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

// VerifyStorageStored runs Sync's check that the selected account's store of client (see
// meowOf) holds what signalmeow's storage sync stores from update.
func VerifyStorageStored(ctx context.Context, client Client, update *signalmeow.StorageUpdate) error {
	_, device := meowOf(ctx, client)

	return verifyStorageStored(ctx, device, update)
}

// SetDrainTimeout replaces how long Close of client (from Open) lets sends run before it
// disconnects.
func SetDrainTimeout(client Client, timeout time.Duration) {
	client.(*meowClient).drainTimeout = timeout //nolint:forcetypeassert // test helper
}

// BeginOperation registers a running operation on client (from Open) as the facade methods do.
// It returns a function that reads the store as such an operation would, and the one that ends
// the operation; ok is false once Close has started.
func BeginOperation(client Client) (func(context.Context) error, func(), bool) {
	meow := client.(*meowClient) //nolint:forcetypeassert // test helper
	if !meow.begin(&meow.sending) {
		return nil, nil, false
	}

	read := func(ctx context.Context) error {
		_, err := meow.data.Groups(ctx)

		return err //nolint:wrapcheck // test helper
	}

	return read, meow.sending.Done, true
}
