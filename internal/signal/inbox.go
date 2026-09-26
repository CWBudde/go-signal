package signal

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// ErrNotStorable means that an event can't be stored in the inbox: connection changes and the
// end of the queue aren't.
var ErrNotStorable = errors.New("event can't be stored in the inbox")

// InboxEntry is an event stored in the account's inbox, where `mcp serve` keeps what it received
// until an MCP client asks for it (see Client.InboxAdd).
type InboxEntry struct {
	// ID is set by the store: it grows with every entry and is never reused, not even after the
	// entries before were pruned, so that it works as a cursor.
	ID int64
	// ReceivedAt is when go-signal received the event.
	ReceivedAt time.Time
	// Time is when the event happened as far as known (the sender's timestamp), else ReceivedAt.
	// InboxQuery.Since compares it.
	Time time.Time
	// Chat is the conversation the event belongs to; zero for none.
	Chat  Chat
	Event Event
	// Unread marks an incoming message (not our own) until it is marked read (InboxMarkRead).
	Unread bool
}

// InboxQuery selects inbox entries (Client.InboxList). Zero fields don't filter.
type InboxQuery struct {
	// After and Until bound the IDs: only entries with After < ID <= Until.
	After, Until int64
	// Chat selects the entries whose Chat has this key (see Chat.Key).
	Chat string
	// Since selects the entries whose Time is not before it.
	Since time.Time
	// Unread selects the unread entries.
	Unread bool
	// Limit caps the entries: the first Limit, or the last Limit with Newest. Zero means no limit.
	Limit  int
	Newest bool
}

// InboxChat summarizes the entries of one chat (Client.InboxChats).
type InboxChat struct {
	Chat    Chat
	Entries int
	Unread  int
	// Last is the chat's newest entry.
	Last InboxEntry
}

// Key identifies the chat as a string: "group:<id>" for a group, else the recipient's ACI (or
// number, if it has none); "" for the zero chat.
func (c Chat) Key() string {
	if c.IsGroup() {
		return "group:" + c.GroupID
	}

	return c.Recipient.String()
}

// storedEvent is how the inbox stores an event: its type, the event as JSON with the Go field
// names, and its chat. Renaming a field of an event type loses its value in the entries stored
// before.
type storedEvent struct {
	Type  string          `json:"type"`
	Event json.RawMessage `json:"event"`
	Chat  Chat            `json:"chat"`
}

// storedFailure stores a DecryptionFailure, whose error doesn't marshal.
type storedFailure struct {
	Sender    Recipient
	Timestamp uint64
	Err       string
}

// Stored event types.
const (
	storedMessage           = "message"
	storedEdit              = "edit"
	storedDelete            = "delete"
	storedReaction          = "reaction"
	storedTyping            = "typing"
	storedReceipt           = "receipt"
	storedReadSync          = "readSync"
	storedUnsupported       = "unsupported"
	storedDecryptionFailure = "decryptionFailure"
	storedIdentityChanged   = "identityChanged"
)

// UnreadableEntry is the Unsupported.Type of an inbox entry that can't be decoded any more.
const UnreadableEntry = "unreadableInboxEntry"

// marshalEvent encodes evt and its chat for the inbox.
//
//nolint:cyclop // one case per event type
func marshalEvent(evt Event, chat Chat) ([]byte, error) {
	var (
		typ     string
		payload any = evt
	)

	switch evt := evt.(type) {
	case *Message:
		typ = storedMessage
	case *Edit:
		typ = storedEdit
	case *Delete:
		typ = storedDelete
	case *Reaction:
		typ = storedReaction
	case *Typing:
		typ = storedTyping
	case *Receipt:
		typ = storedReceipt
	case *ReadSync:
		typ = storedReadSync
	case *Unsupported:
		typ = storedUnsupported
	case *DecryptionFailure:
		typ = storedDecryptionFailure
		payload = storedFailure{Sender: evt.Sender, Timestamp: evt.Timestamp, Err: errText(evt.Err)}
	case *IdentityChanged:
		typ = storedIdentityChanged
	default:
		return nil, fmt.Errorf("%w: %T", ErrNotStorable, evt)
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode %s: %w", typ, err)
	}

	out, err := json.Marshal(storedEvent{Type: typ, Event: raw, Chat: chat})
	if err != nil {
		return nil, fmt.Errorf("encode %s: %w", typ, err)
	}

	return out, nil
}

// unmarshalEvent decodes an event and its chat that marshalEvent encoded. An event it can't
// decode becomes an *Unsupported of type UnreadableEntry, so that one bad entry doesn't hide the
// others.
func unmarshalEvent(data []byte) (Event, Chat) {
	var stored storedEvent

	err := json.Unmarshal(data, &stored)
	if err != nil {
		return &Unsupported{Type: UnreadableEntry}, Chat{}
	}

	evt, err := decodeEvent(stored)
	if err != nil {
		return &Unsupported{Type: UnreadableEntry}, stored.Chat
	}

	return evt, stored.Chat
}

//nolint:cyclop // one case per event type
func decodeEvent(stored storedEvent) (Event, error) {
	var (
		evt Event
		err error
	)

	switch stored.Type {
	case storedMessage:
		evt = &Message{}
	case storedEdit:
		evt = &Edit{}
	case storedDelete:
		evt = &Delete{}
	case storedReaction:
		evt = &Reaction{}
	case storedTyping:
		evt = &Typing{}
	case storedReceipt:
		evt = &Receipt{}
	case storedReadSync:
		evt = &ReadSync{}
	case storedUnsupported:
		evt = &Unsupported{}
	case storedIdentityChanged:
		evt = &IdentityChanged{}
	case storedDecryptionFailure:
		var failure storedFailure

		err = json.Unmarshal(stored.Event, &failure)
		if err != nil {
			return nil, fmt.Errorf("decode %s: %w", stored.Type, err)
		}

		out := &DecryptionFailure{Sender: failure.Sender, Timestamp: failure.Timestamp}
		if failure.Err != "" {
			out.Err = errors.New(failure.Err) //nolint:err113 // only the text is stored
		}

		return out, nil
	default:
		return nil, fmt.Errorf("%w: stored type %q", ErrNotStorable, stored.Type)
	}

	err = json.Unmarshal(stored.Event, evt)
	if err != nil {
		return nil, fmt.Errorf("decode %s: %w", stored.Type, err)
	}

	return evt, nil
}

func errText(err error) string {
	if err == nil {
		return ""
	}

	return err.Error()
}
