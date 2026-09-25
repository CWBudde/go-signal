//go:build cgo

package signal

import (
	"strings"

	"github.com/google/uuid"
	"go.mau.fi/mautrix-signal/pkg/libsignalgo"
	"go.mau.fi/mautrix-signal/pkg/signalmeow"
	"go.mau.fi/mautrix-signal/pkg/signalmeow/events"
	"go.mau.fi/mautrix-signal/pkg/signalmeow/protobuf/signalpb"
)

// convertEvent maps a signalmeow event to ours. ownACI marks sync transcripts. It returns nil
// for events the facade doesn't expose (yet).
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
	case *events.DecryptionError:
		return &DecryptionFailure{Sender: aciRecipient(evt.Sender), Timestamp: evt.Timestamp, Err: evt.Err}
	case *events.QueueEmpty:
		return &QueueEmpty{}
	case *events.LoggedOut:
		return &Connection{State: StateLoggedOut, Err: evt.Error}
	default:
		return nil
	}
}

func convertChatEvent(evt *events.ChatEvent, ownACI string) Event {
	env := Envelope{
		Sender:          aciRecipient(evt.Info.Sender),
		Chat:            parseChat(evt.Info.ChatID),
		ServerTimestamp: evt.Info.ServerTimestamp,
		Sync:            evt.Info.Sender.String() == ownACI,
	}

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
		return nil
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

	out := &Message{Envelope: env, Body: msg.GetBody()}

	for _, att := range msg.GetAttachments() {
		out.Attachments = append(out.Attachments, Attachment{
			ContentType: att.GetContentType(),
			Filename:    att.GetFileName(),
			Size:        att.GetSize(),
			Caption:     att.GetCaption(),
		})
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
