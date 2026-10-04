//go:build cgo || libsignal_go

package signal

import (
	"context"
	"fmt"

	"github.com/cwbudde/go-signal/internal/store"
	"github.com/cwbudde/mautrix-signal/pkg/libsignalgo"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow/events"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow/protobuf/signalpb"
	"github.com/google/uuid"
	"google.golang.org/protobuf/encoding/protowire"
)

// learnChatTimers stores raw direct-chat settings before delivery or contact completion.
func (c *meowClient) learnChatTimers(ctx context.Context, raw events.SignalEvent) error {
	var records []store.ChatTimerRecord

	switch evt := raw.(type) {
	case *events.ChatEvent:
		record, ok := c.chatTimerRecord(evt)
		if ok {
			records = []store.ChatTimerRecord{record}
		}
	case *events.ContactList:
		records = contactTimerRecords(evt)
	}

	if len(records) == 0 {
		return nil
	}

	if c.timerPersistenceContext != nil {
		ctx = c.timerPersistenceContext(ctx)
	}

	err := c.data.MergeChatTimers(ctx, records)
	if err != nil {
		return fmt.Errorf("learn chat timers: %w", err)
	}

	return nil
}

func (c *meowClient) chatTimerRecord(evt *events.ChatEvent) (store.ChatTimerRecord, bool) {
	if evt == nil {
		return store.ChatTimerRecord{}, false
	}

	msg := chatTimerMessage(evt.Event)
	if !eligibleChatTimer(msg) {
		return store.ChatTimerRecord{}, false
	}

	aci := evt.Info.Sender
	if aci.String() == c.ownACI {
		aci = chatTimerDestination(evt.Info.ChatID)
	}

	if aci == uuid.Nil {
		return store.ChatTimerRecord{}, false
	}

	return store.ChatTimerRecord{
		ACI: aci.String(), Seconds: msg.GetExpireTimer(), Version: msg.GetExpireTimerVersion(),
	}, true
}

func chatTimerDestination(chatID string) uuid.UUID {
	destination, err := libsignalgo.ServiceIDFromString(chatID)
	if err != nil || destination.Type != libsignalgo.ServiceIDTypeACI {
		return uuid.Nil
	}

	return destination.UUID
}

func contactTimerRecords(evt *events.ContactList) []store.ChatTimerRecord {
	if evt == nil || evt.IsFromDB {
		return nil
	}

	records := make([]store.ChatTimerRecord, 0, len(evt.Timers))
	for _, timer := range evt.Timers {
		if timer.ACI == uuid.Nil || timer.ExpireTimer == nil || timer.ExpireTimerVersion == nil {
			continue
		}

		records = append(records, store.ChatTimerRecord{
			ACI: timer.ACI.String(), Seconds: *timer.ExpireTimer, Version: *timer.ExpireTimerVersion,
		})
	}

	return records
}

func chatTimerMessage(content signalpb.ChatEventContent) *signalpb.DataMessage {
	switch msg := content.(type) {
	case *signalpb.DataMessage:
		return msg
	case *signalpb.EditMessage:
		return msg.GetDataMessage()
	default:
		return nil
	}
}

func eligibleChatTimer(msg *signalpb.DataMessage) bool {
	if msg == nil || msg.GetGroupV2() != nil || hasLegacyChatGroup(msg) {
		return false
	}

	update := msg.GetFlags()&uint32(signalpb.DataMessage_EXPIRATION_TIMER_UPDATE) != 0

	return (msg.Body != nil || update) && (msg.ExpireTimer != nil || msg.ExpireTimerVersion != nil || update)
}

// SignalService.proto reserves DataMessage field 3 as /*groupV1*/. It survives decoding in
// unknown fields, so legacy group metadata must not become a direct-chat timer.
func hasLegacyChatGroup(msg *signalpb.DataMessage) bool {
	unknown := msg.ProtoReflect().GetUnknown()
	for len(unknown) > 0 {
		number, _, length := protowire.ConsumeField(unknown)
		if length < 0 || number == 3 {
			return true
		}

		unknown = unknown[length:]
	}

	return false
}

// directMessage reads this recipient's settings before constructing independent wire content.
// An unknown chat uses the reference sender's zero/version-one fallback without storing it.
func (c *meowClient) directMessage(ctx context.Context, recipient Recipient,
	fresh func() *signalpb.DataMessage,
) (*signalpb.DataMessage, error) {
	id, err := aciServiceID(recipient)
	if err != nil {
		return nil, err
	}

	timer, known, err := c.data.ChatTimer(ctx, id.UUID.String())
	if err != nil {
		return nil, fmt.Errorf("direct message timer: %w", err)
	}

	if !known {
		timer = store.ChatTimerRecord{Seconds: 0, Version: 1}
	}

	msg := fresh()
	msg.ExpireTimer = new(timer.Seconds)
	msg.ExpireTimerVersion = new(timer.Version)

	return msg, nil
}
