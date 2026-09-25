//go:build cgo

package signal

import (
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
