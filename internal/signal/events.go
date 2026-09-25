package signal

// Event is an incoming event. It is a closed sum type; switch on the concrete pointer types:
// *Message, *Edit, *Delete, *Reaction, *Typing, *Receipt, *ReadSync, *DecryptionFailure,
// *QueueEmpty and *Connection.
type Event interface {
	isEvent()
}

// Envelope is the metadata shared by events that come from a message.
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

// Message is a regular data message.
type Message struct {
	Envelope

	Body        string
	Attachments []Attachment
	Quote       *Quote
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
	default:
		return "unknown"
	}
}

// Connection reports a change of the connection state. StateLoggedOut is final: the device was
// unlinked or its credentials are no longer valid.
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
func (*DecryptionFailure) isEvent() {}
func (*QueueEmpty) isEvent()        {}
func (*Connection) isEvent()        {}
