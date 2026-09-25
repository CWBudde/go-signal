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
	Timestamp uint64
	// Attachments were uploaded with Upload on the same client.
	Attachments []UploadedAttachment
	// Quote makes the message a reply; the author needs their ACI.
	Quote *Quote
	// Mentions mark users mentioned in Body; they need their ACI.
	Mentions []Mention
}

// Mention marks a user mentioned in a message body. Start and Length count UTF-16 code units, as
// Signal's body ranges do; the mention usually covers a single U+FFFC placeholder, which clients
// show as the user's name.
type Mention struct {
	Start     uint32
	Length    uint32
	Recipient Recipient
}

// OutgoingAttachment is a file to upload with Upload.
type OutgoingAttachment struct {
	Data        []byte
	ContentType string
	Filename    string
	// Width and Height are the dimensions of an image in pixels; zero if unknown.
	Width  uint32
	Height uint32
}

// UploadedAttachment is an attachment on Signal's CDN, ready to be sent with SendRequest.
type UploadedAttachment struct {
	// ID identifies the upload within the client that made it.
	ID          string
	ContentType string
	Filename    string
	Size        uint32
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
