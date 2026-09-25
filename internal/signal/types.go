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
	// Recipients need their ACI; one with our own ACI is a note-to-self.
	Recipients []Recipient
	// GroupID is the base64 group identifier (standard encoding), instead of Recipients.
	GroupID string
	Body    string
	// Timestamp is the sent timestamp (ms since epoch) that identifies the message; zero means
	// now. Several requests with the same timestamp send the same message to more recipients.
	Timestamp   uint64
	Attachments []string // file paths (Phase 3.4)
	Quote       *Quote   // (Phase 3.4)
}

// SendResult reports the outcome of a SendRequest per recipient.
type SendResult struct {
	// Timestamp is the message's sent timestamp (ms since epoch), which identifies it.
	Timestamp uint64
	// Results has one entry per recipient in request order, or per group member (without us)
	// for a group.
	Results []RecipientResult
}

// RecipientResult is the outcome of sending to one recipient.
type RecipientResult struct {
	Recipient    Recipient
	Unidentified bool // sent with sealed sender
	Err          error
}
