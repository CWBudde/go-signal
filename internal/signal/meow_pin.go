//go:build cgo || libsignal_go

package signal

import (
	"fmt"

	"github.com/cwbudde/mautrix-signal/pkg/signalmeow/protobuf/signalpb"
	"github.com/google/uuid"
)

const invalidPinType = "invalidPin"

func addPin(msg *signalpb.DataMessage, req SendRequest) error {
	if pin := req.Pin; pin != nil {
		author, err := aciBytes(pin.TargetAuthor)
		if err != nil {
			return fmt.Errorf("pin target: %w", err)
		}

		wire := &signalpb.DataMessage_PinMessage{TargetAuthorAciBinary: author, TargetSentTimestamp: new(pin.TargetTimestamp)}
		if pin.Forever {
			wire.PinDuration = &signalpb.DataMessage_PinMessage_PinDurationForever{PinDurationForever: true}
		} else {
			wire.PinDuration = &signalpb.DataMessage_PinMessage_PinDurationSeconds{PinDurationSeconds: pin.DurationSeconds}
		}

		msg.PinMessage = wire
	}

	if unpin := req.Unpin; unpin != nil {
		author, err := aciBytes(unpin.TargetAuthor)
		if err != nil {
			return fmt.Errorf("unpin target: %w", err)
		}

		msg.UnpinMessage = &signalpb.DataMessage_UnpinMessage{
			TargetAuthorAciBinary: author, TargetSentTimestamp: new(unpin.TargetTimestamp),
		}
	}

	return nil
}

func hasWirePin(msg *signalpb.DataMessage) bool {
	return msg.GetPinMessage() != nil || msg.GetUnpinMessage() != nil
}

func pinHasWireContent(msg *signalpb.DataMessage) bool {
	return msg.GetFlags() != 0 || msg.GetGroupCallUpdate() != nil || hasWirePoll(msg) || pollHasWireContent(msg)
}

func convertPin(env Envelope, msg *signalpb.DataMessage) Event {
	invalid := &Unsupported{Envelope: env, Type: invalidPinType}
	if env.Timestamp == 0 || (msg.GetPinMessage() != nil && msg.GetUnpinMessage() != nil) ||
		pinHasWireContent(msg) {
		return invalid
	}

	if wire := msg.GetPinMessage(); wire != nil {
		return convertPinMessage(env, wire)
	}

	wire := msg.GetUnpinMessage()

	author, err := uuid.FromBytes(wire.GetTargetAuthorAciBinary())
	if err != nil {
		return invalid
	}

	unpin := OutgoingUnpin{TargetAuthor: Recipient{ACI: author.String()}, TargetTimestamp: wire.GetTargetSentTimestamp()}
	if unpin.Check() != nil {
		return invalid
	}

	return &Unpin{Envelope: env, OutgoingUnpin: unpin}
}

func convertPinMessage(env Envelope, wire *signalpb.DataMessage_PinMessage) Event {
	invalid := &Unsupported{Envelope: env, Type: invalidPinType}

	author, err := uuid.FromBytes(wire.GetTargetAuthorAciBinary())
	if err != nil {
		return invalid
	}

	pin := OutgoingPin{
		TargetAuthor: Recipient{ACI: author.String()}, TargetTimestamp: wire.GetTargetSentTimestamp(),
		DurationSeconds: wire.GetPinDurationSeconds(), Forever: wire.GetPinDurationForever(),
	}
	if pin.Check() != nil {
		return invalid
	}

	return &Pin{Envelope: env, OutgoingPin: pin}
}
