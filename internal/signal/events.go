package signal

// Event is an incoming event. It is a closed sum type; switch on the concrete pointer types:
// *Message, *Edit, *Delete, *Reaction, *Typing, *Receipt, *ReadSync, *Unsupported,
// *DecryptionFailure, *QueueEmpty and *Connection.
type Event interface {
	isEvent()
}

// Envelope is the metadata shared by events that come from a message.
//
// Chat is the conversation: the group, or for 1:1 chats the other party. For an incoming 1:1
// message that is the sender; for a sync transcript (Sync, Sender is our own ACI) it is the
// recipient the message was sent to, which is our own ACI for note-to-self.
type Envelope struct {
	Sender Recipient
	Chat   Chat
	// Timestamp is the sender's timestamp (ms since epoch); together with Sender it identifies
	// the message.
	Timestamp       uint64
	ServerTimestamp uint64
	// Sync is set for messages sent from another of our own devices (sync transcripts).
	Sync bool
}

// Message is a regular data message: it has a body, attachments or a sticker. With
// Envelope.Sync it is a sync transcript of a message we sent from another device.
type Message struct {
	Envelope

	Body        string
	Attachments []Attachment
	Sticker     *Sticker
	Quote       *Quote
	// ViewOnce marks a view-once message (its attachments can be opened once).
	ViewOnce bool
	// Unsupported names parts of the message that go-signal can't show yet, e.g. "contact" or
	// "storyReply" (see Unsupported for the names).
	Unsupported []string
}

// Sticker is a sticker from a sticker pack.
type Sticker struct {
	// PackID is the hex-encoded ID of the sticker pack.
	PackID    string
	StickerID uint32
	// Emoji is the emoji the sticker stands for, if the sender set one.
	Emoji string
}

// Edit replaces the body of an earlier message.
type Edit struct {
	Envelope

	TargetTimestamp uint64
	Body            string
}

// Delete is a remote delete of an earlier message.
type Delete struct {
	Envelope

	TargetTimestamp uint64
}

// Reaction adds or removes an emoji reaction on a message.
type Reaction struct {
	Envelope

	Emoji           string
	Remove          bool
	TargetAuthor    Recipient
	TargetTimestamp uint64
}

// Typing is a typing indicator.
type Typing struct {
	Envelope

	Started bool
}

// ReceiptType says what a receipt confirms.
type ReceiptType int

// Receipt types.
const (
	ReceiptDelivery ReceiptType = iota + 1
	ReceiptRead
	ReceiptViewed
)

func (t ReceiptType) String() string {
	switch t {
	case ReceiptDelivery:
		return "delivery"
	case ReceiptRead:
		return "read"
	case ReceiptViewed:
		return "viewed"
	default:
		return "unknown"
	}
}

// Receipt confirms delivery, reading or viewing of our messages.
type Receipt struct {
	Sender     Recipient
	Type       ReceiptType
	Timestamps []uint64
}

// ReadMark identifies one message marked as read.
type ReadMark struct {
	Sender    Recipient
	Timestamp uint64
}

// ReadSync says that another of our devices has read messages.
type ReadSync struct {
	Timestamp uint64
	Messages  []ReadMark
}

// Unsupported reports content that go-signal doesn't handle yet, so that it isn't dropped
// silently. Type names what it is:
//
//   - "call" (1:1 call offer or hangup, or a group call update)
//   - data messages without body, attachments or sticker: "groupUpdate", "expirationTimerUpdate",
//     "profileKeyUpdate", "endSession", "contact", "payment", "giftBadge", "pollCreate",
//     "pollVote", "pollTerminate", "pinMessage", "unpinMessage", "adminDelete", or
//     "dataMessage" when nothing is recognised
//   - sync messages from our other devices: "deleteForMe" (messages deleted locally there) and
//     "messageRequestResponse" (a message request accepted, blocked, …)
//
// Envelope.Timestamp may be 0 when the content carries none.
type Unsupported struct {
	Envelope

	Type string
}

// DecryptionFailure reports an envelope that could not be decrypted.
type DecryptionFailure struct {
	Sender    Recipient
	Timestamp uint64
	Err       error
}

// QueueEmpty says that the server has delivered all queued messages.
type QueueEmpty struct{}

// ConnectionState is the state reported by a *Connection event.
type ConnectionState int

// Connection states.
const (
	StateConnected ConnectionState = iota + 1
	StateDisconnected
	StateLoggedOut
	StateError
	StateFailed
)

func (s ConnectionState) String() string {
	switch s {
	case StateConnected:
		return "connected"
	case StateDisconnected:
		return "disconnected"
	case StateLoggedOut:
		return "logged-out"
	case StateError:
		return "error"
	case StateFailed:
		return "failed"
	default:
		return "unknown"
	}
}

// Connection reports a change of the connection state. StateDisconnected and StateError are
// transient: the client reconnects on its own. StateLoggedOut and StateFailed are final: the
// device was unlinked or its credentials are no longer valid (Err wraps ErrDeviceUnlinked), or
// the client gave up reconnecting (Err wraps ErrConnectionFailed).
type Connection struct {
	State ConnectionState
	Err   error
}

func (*Message) isEvent()           {}
func (*Edit) isEvent()              {}
func (*Delete) isEvent()            {}
func (*Reaction) isEvent()          {}
func (*Typing) isEvent()            {}
func (*Receipt) isEvent()           {}
func (*ReadSync) isEvent()          {}
func (*Unsupported) isEvent()       {}
func (*DecryptionFailure) isEvent() {}
func (*QueueEmpty) isEvent()        {}
func (*Connection) isEvent()        {}
