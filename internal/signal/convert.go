//go:build cgo

package signal

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"go.mau.fi/mautrix-signal/pkg/libsignalgo"
	"go.mau.fi/mautrix-signal/pkg/signalmeow"
	"go.mau.fi/mautrix-signal/pkg/signalmeow/events"
	"go.mau.fi/mautrix-signal/pkg/signalmeow/protobuf/signalpb"
)

// convertEvent maps a signalmeow event to ours. ownACI marks sync transcripts. Content we can't
// show yet becomes an *Unsupported event. It returns nil only for events that concern
// signalmeow's store alone (ContactList, ACIFound) and for unknown event types.
//
//nolint:cyclop // one case per event type
func convertEvent(raw events.SignalEvent, ownACI string) Event {
	switch evt := raw.(type) {
	case *events.ChatEvent:
		return convertChatEvent(evt, ownACI)
	case *events.Receipt:
		return &Receipt{
			Sender:     aciRecipient(evt.Sender),
			Type:       convertReceiptType(evt.Content.GetType()),
			Timestamps: evt.Content.GetTimestamp(),
		}
	case *events.ReadSelf:
		marks := make([]ReadMark, 0, len(evt.Messages))
		for _, msg := range evt.Messages {
			marks = append(marks, ReadMark{
				Sender:    Recipient{ACI: msg.GetSenderAci()},
				Timestamp: msg.GetTimestamp(),
			})
		}

		return &ReadSync{Timestamp: evt.Timestamp, Messages: marks}
	case *events.Call:
		env := envelope(evt.Info, ownACI)
		env.Timestamp = evt.Timestamp

		return &Unsupported{Envelope: env, Type: "call"}
	case *events.DeleteForMe:
		return &Unsupported{Envelope: syncEnvelope(ownACI, Chat{}, evt.Timestamp), Type: "deleteForMe"}
	case *events.MessageRequestResponse:
		chat := Chat{Recipient: aciRecipient(evt.ThreadACI)}
		if evt.GroupID != nil {
			chat = Chat{GroupID: base64.StdEncoding.EncodeToString(evt.GroupID[:])}
		}

		return &Unsupported{Envelope: syncEnvelope(ownACI, chat, evt.Timestamp), Type: "messageRequestResponse"}
	case *events.DecryptionError:
		return &DecryptionFailure{Sender: aciRecipient(evt.Sender), Timestamp: evt.Timestamp, Err: evt.Err}
	case *events.QueueEmpty:
		return &QueueEmpty{}
	case *events.LoggedOut:
		return &Connection{State: StateLoggedOut, Err: evt.Error}
	default: // *events.ContactList, *events.ACIFound: store updates, nothing to show
		return nil
	}
}

// envelope converts the metadata of a chat event. signalmeow reports a sync transcript with our
// own ACI as the sender and its destination (recipient or group) as the chat.
func envelope(info events.MessageInfo, ownACI string) Envelope {
	return Envelope{
		Sender:          aciRecipient(info.Sender),
		Chat:            parseChat(info.ChatID),
		ServerTimestamp: info.ServerTimestamp,
		Sync:            info.Sender.String() == ownACI,
	}
}

// syncEnvelope is the envelope of a sync message from another of our devices.
func syncEnvelope(ownACI string, chat Chat, timestamp uint64) Envelope {
	return Envelope{Sender: Recipient{ACI: ownACI}, Chat: chat, Timestamp: timestamp, Sync: true}
}

func convertChatEvent(evt *events.ChatEvent, ownACI string) Event {
	env := envelope(evt.Info, ownACI)

	switch content := evt.Event.(type) {
	case *signalpb.DataMessage:
		env.Timestamp = content.GetTimestamp()

		return convertDataMessage(env, content)
	case *signalpb.EditMessage:
		env.Timestamp = content.GetDataMessage().GetTimestamp()

		return &Edit{
			Envelope:        env,
			TargetTimestamp: content.GetTargetSentTimestamp(),
			Body:            content.GetDataMessage().GetBody(),
		}
	case *signalpb.TypingMessage:
		env.Timestamp = content.GetTimestamp()

		return &Typing{Envelope: env, Started: content.GetAction() == signalpb.TypingMessage_STARTED}
	default:
		return &Unsupported{Envelope: env, Type: fmt.Sprintf("%T", content)}
	}
}

func convertDataMessage(env Envelope, msg *signalpb.DataMessage) Event {
	if reaction := msg.GetReaction(); reaction != nil {
		return &Reaction{
			Envelope:        env,
			Emoji:           reaction.GetEmoji(),
			Remove:          reaction.GetRemove(),
			TargetAuthor:    Recipient{ACI: reaction.GetTargetAuthorAci()},
			TargetTimestamp: reaction.GetTargetSentTimestamp(),
		}
	}

	if del := msg.GetDelete(); del != nil {
		return &Delete{Envelope: env, TargetTimestamp: del.GetTargetSentTimestamp()}
	}

	unsupported := unsupportedParts(msg)

	if msg.GetBody() == "" && len(msg.GetAttachments()) == 0 && msg.GetSticker() == nil {
		return &Unsupported{Envelope: env, Type: controlType(msg, unsupported)}
	}

	out := &Message{
		Envelope:    env,
		Body:        msg.GetBody(),
		ViewOnce:    msg.GetIsViewOnce(),
		Unsupported: unsupported,
	}

	for _, att := range msg.GetAttachments() {
		out.Attachments = append(out.Attachments, convertAttachment(att))
	}

	if sticker := msg.GetSticker(); sticker != nil {
		out.Sticker = &Sticker{
			PackID:    hex.EncodeToString(sticker.GetPackId()),
			StickerID: sticker.GetStickerId(),
			Emoji:     sticker.GetEmoji(),
		}
	}

	if quote := msg.GetQuote(); quote != nil {
		out.Quote = &Quote{
			Author:    Recipient{ACI: quote.GetAuthorAci()},
			Timestamp: quote.GetId(),
			Text:      quote.GetText(),
		}
	}

	return out
}

// unsupportedParts names the parts of msg that we can't show yet (see Unsupported).
func unsupportedParts(msg *signalpb.DataMessage) []string {
	var parts []string

	for _, part := range []struct {
		name    string
		present bool
	}{
		{"contact", len(msg.GetContact()) > 0},
		{"payment", msg.GetPayment() != nil},
		{"giftBadge", msg.GetGiftBadge() != nil},
		{"pollCreate", msg.GetPollCreate() != nil},
		{"pollVote", msg.GetPollVote() != nil},
		{"pollTerminate", msg.GetPollTerminate() != nil},
		{"pinMessage", msg.GetPinMessage() != nil},
		{"unpinMessage", msg.GetUnpinMessage() != nil},
		{"adminDelete", msg.GetAdminDelete() != nil},
		{"storyReply", msg.GetStoryContext() != nil},
	} {
		if part.present {
			parts = append(parts, part.name)
		}
	}

	return parts
}

// endSessionFlag is DataMessage.Flags' END_SESSION, which the current protos no longer list.
const endSessionFlag = 1

// controlType names a data message without body, attachments or sticker: its first unsupported
// part, else what its flags or group context say it is.
func controlType(msg *signalpb.DataMessage, unsupported []string) string {
	flags := msg.GetFlags()

	switch {
	case len(unsupported) > 0:
		return unsupported[0]
	case flags&endSessionFlag != 0:
		return "endSession"
	case flags&uint32(signalpb.DataMessage_EXPIRATION_TIMER_UPDATE) != 0:
		return "expirationTimerUpdate"
	case flags&uint32(signalpb.DataMessage_PROFILE_KEY_UPDATE) != 0:
		return "profileKeyUpdate"
	case msg.GetGroupV2() != nil:
		return "groupUpdate"
	default:
		return "dataMessage"
	}
}

// parseChat decodes signalmeow's chat ID: a service ID for 1:1 chats, else a group identifier.
func parseChat(chatID string) Chat {
	serviceID, err := libsignalgo.ServiceIDFromString(chatID)
	if err != nil {
		return Chat{GroupID: chatID}
	}

	if serviceID.Type == libsignalgo.ServiceIDTypePNI {
		return Chat{Recipient: Recipient{PNI: serviceID.UUID.String()}}
	}

	return Chat{Recipient: aciRecipient(serviceID.UUID)}
}

func aciRecipient(id uuid.UUID) Recipient {
	if id == uuid.Nil {
		return Recipient{}
	}

	return Recipient{ACI: id.String()}
}

func convertReceiptType(typ signalpb.ReceiptMessage_Type) ReceiptType {
	switch typ {
	case signalpb.ReceiptMessage_DELIVERY:
		return ReceiptDelivery
	case signalpb.ReceiptMessage_READ:
		return ReceiptRead
	case signalpb.ReceiptMessage_VIEWED:
		return ReceiptViewed
	default:
		return 0
	}
}

func convertStatus(status signalmeow.SignalConnectionStatus) Event {
	switch status.Event {
	case signalmeow.SignalConnectionEventConnected:
		return &Connection{State: StateConnected}
	case signalmeow.SignalConnectionEventDisconnected:
		return &Connection{State: StateDisconnected, Err: status.Err}
	case signalmeow.SignalConnectionEventLoggedOut:
		return &Connection{State: StateLoggedOut, Err: status.Err}
	case signalmeow.SignalConnectionEventFatalError:
		if isUnauthorized(status.Err) {
			return &Connection{State: StateLoggedOut, Err: status.Err}
		}

		return &Connection{State: StateError, Err: status.Err}
	case signalmeow.SignalConnectionEventError:
		return &Connection{State: StateError, Err: status.Err}
	case signalmeow.SignalConnectionEventNone, signalmeow.SignalConnectionCleanShutdown:
		return nil
	default:
		return nil
	}
}

// convertLoopStatus maps a status of signalmeow's receive loops for the supervisor. After a
// fatal error, signalmeow's websockets stop retrying, so the loops have to be restarted.
func convertLoopStatus(status signalmeow.SignalConnectionStatus) loopStatus {
	out := loopStatus{Stopped: status.Event == signalmeow.SignalConnectionEventFatalError}

	if conn, ok := convertStatus(status).(*Connection); ok {
		out.State, out.Err = conn.State, conn.Err
	}

	return out
}

// websocketUnauthorized is how signalmeow reports a 401 when opening the websocket. It only maps
// 403 to a logout and gives up on any other 4xx with an unwrapped error, so the status can only
// be recognised by its text.
const websocketUnauthorized = "opening websocket: 401"

// isUnauthorized reports whether err is signalmeow's websocket 401, which (like 403) means the
// server no longer accepts the device's credentials.
func isUnauthorized(err error) bool {
	return err != nil && strings.Contains(err.Error(), websocketUnauthorized)
}
