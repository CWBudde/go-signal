package signal

// Recipient identifies a Signal user. At least one field is set; ACI is the stable identity,
// Number and Username are only known when the server or a contact told us.
type Recipient struct {
	ACI      string
	PNI      string
	Number   string
	Username string
}

// String returns the most stable identifier that is set.
func (r Recipient) String() string {
	switch {
	case r.ACI != "":
		return r.ACI
	case r.PNI != "":
		return "PNI:" + r.PNI
	case r.Number != "":
		return r.Number
	case r.Username != "":
		return "@" + r.Username
	default:
		return ""
	}
}

// IsZero reports whether no identifier is set.
func (r Recipient) IsZero() bool {
	return r == Recipient{}
}

// Chat is the conversation an event belongs to: a group, or else a 1:1 chat with Recipient.
type Chat struct {
	GroupID   string
	Recipient Recipient
}

// IsGroup reports whether the chat is a group.
func (c Chat) IsGroup() bool {
	return c.GroupID != ""
}

// Attachment describes an attachment of an incoming message. Downloading comes in Phase 3.7.
type Attachment struct {
	ContentType string
	Filename    string
	Size        uint32
	Caption     string
}

// Quote references the message a reply quotes.
type Quote struct {
	Author    Recipient
	Timestamp uint64
	Text      string
}

// SendRequest is one outgoing message, either to Recipients or to a group.
type SendRequest struct {
	Recipients  []Recipient
	GroupID     string
	Body        string
	Attachments []string // file paths
	Quote       *Quote
}

// SendResult reports the outcome of a SendRequest per recipient.
type SendResult struct {
	// Timestamp is the message's sent timestamp (ms since epoch), which identifies it.
	Timestamp uint64
	Results   []RecipientResult
}

// RecipientResult is the outcome of sending to one recipient.
type RecipientResult struct {
	Recipient    Recipient
	Unidentified bool // sent with sealed sender
	Err          error
}
