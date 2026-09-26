package output

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/signal"
)

// Event prints one received event (`receive`). Plain output is one line per event,
// `[time] <sender> → <dest>: <text>`; connection changes and the end of the queue are left to the
// log. JSON output is one document per line (NDJSON) for every event, as docs/json.md describes.
func (p *Printer) Event(evt signal.Event) error {
	if p.format == JSON {
		return p.writeJSON(p.eventDoc(evt))
	}

	line := p.eventLine(evt)
	if line == "" {
		return nil
	}

	return p.writeLine(line)
}

// SavedMessage prints a message like Event, together with the outcome of saving its
// attachments (app.SaveAttachments, one entry per attachment): the local path, or why the
// download failed.
func (p *Printer) SavedMessage(msg *signal.Message, saved []app.SavedAttachment) error {
	if p.format == JSON {
		return p.writeJSON(p.messageDocOf(msg, saved))
	}

	return p.writeLine(p.envelopeLine(msg.Envelope, p.messageText(msg, saved)))
}

func (p *Printer) writeLine(line string) error {
	_, err := fmt.Fprintln(p.w, line)
	if err != nil {
		return fmt.Errorf("write output: %w", err)
	}

	return nil
}

// Event types of the JSON "type" field.
const (
	typeMessage           = "message"
	typeEdit              = "edit"
	typeDelete            = "delete"
	typeReaction          = "reaction"
	typeTyping            = "typing"
	typeReceipt           = "receipt"
	typeReadSync          = "readSync"
	typeUnsupported       = "unsupported"
	typeDecryptionFailure = "decryptionFailure"
	typeQueueEmpty        = "queueEmpty"
	typeConnection        = "connection"
)

// recipientJSON is the "recipient" object of docs/json.md.
type recipientJSON struct {
	ACI      string `json:"aci,omitempty"`
	PNI      string `json:"pni,omitempty"`
	Number   string `json:"number,omitempty"`
	Username string `json:"username,omitempty"`
	Name     string `json:"name,omitempty"`
}

// recipient converts r, with its name if the printer knows it (see SetNames).
func (p *Printer) recipient(r signal.Recipient) recipientJSON {
	return recipientJSON{ACI: r.ACI, PNI: r.PNI, Number: r.Number, Username: r.Username, Name: p.names.Name(r)}
}

type chatJSON struct {
	GroupID   string         `json:"groupId,omitempty"`
	Recipient *recipientJSON `json:"recipient,omitempty"`
}

type eventHead struct {
	Version int    `json:"version"`
	Type    string `json:"type"`
}

func head(typ string) eventHead {
	return eventHead{Version: SchemaVersion, Type: typ}
}

// envelopeJSON holds the fields shared by events that come from a message.
type envelopeJSON struct {
	Sender     recipientJSON `json:"sender"`
	Chat       chatJSON      `json:"chat"`
	Timestamp  uint64        `json:"timestamp"`
	Time       time.Time     `json:"time,omitzero"`
	ServerTime time.Time     `json:"serverTime,omitzero"`
	Sync       bool          `json:"sync"`
}

func (p *Printer) envelope(env signal.Envelope) envelopeJSON {
	out := envelopeJSON{
		Sender:     p.recipient(env.Sender),
		Timestamp:  env.Timestamp,
		Time:       msTime(env.Timestamp),
		ServerTime: msTime(env.ServerTimestamp),
		Sync:       env.Sync,
	}

	switch {
	case env.Chat.IsGroup():
		out.Chat.GroupID = env.Chat.GroupID
	case !env.Chat.Recipient.IsZero():
		rcpt := p.recipient(env.Chat.Recipient)
		out.Chat.Recipient = &rcpt
	}

	return out
}

type attachmentJSON struct {
	ContentType string `json:"contentType"`
	Filename    string `json:"filename,omitempty"`
	Size        uint32 `json:"size,omitempty"`
	Caption     string `json:"caption,omitempty"`
	// Path and DownloadError are only set by receive --download-attachments.
	Path          string `json:"path,omitempty"`
	DownloadError string `json:"downloadError,omitempty"`
}

type stickerJSON struct {
	PackID    string `json:"packId"`
	StickerID uint32 `json:"stickerId"`
	Emoji     string `json:"emoji,omitempty"`
}

type quoteJSON struct {
	Author    recipientJSON `json:"author"`
	Timestamp uint64        `json:"timestamp"`
	Text      string        `json:"text,omitempty"`
}

type messageDoc struct {
	eventHead
	envelopeJSON

	Body        string           `json:"body,omitempty"`
	Attachments []attachmentJSON `json:"attachments,omitempty"`
	Sticker     *stickerJSON     `json:"sticker,omitempty"`
	Quote       *quoteJSON       `json:"quote,omitempty"`
	ViewOnce    bool             `json:"viewOnce,omitempty"`
	Unsupported []string         `json:"unsupported,omitempty"`
}

type editDoc struct {
	eventHead
	envelopeJSON

	TargetTimestamp uint64 `json:"targetTimestamp"`
	Body            string `json:"body"`
}

type deleteDoc struct {
	eventHead
	envelopeJSON

	TargetTimestamp uint64 `json:"targetTimestamp"`
}

type reactionDoc struct {
	eventHead
	envelopeJSON

	Emoji           string        `json:"emoji"`
	Remove          bool          `json:"remove"`
	TargetAuthor    recipientJSON `json:"targetAuthor"`
	TargetTimestamp uint64        `json:"targetTimestamp"`
}

type typingDoc struct {
	eventHead
	envelopeJSON

	Action string `json:"action"`
}

type receiptDoc struct {
	eventHead

	Sender      recipientJSON `json:"sender"`
	ReceiptType string        `json:"receiptType"`
	Timestamps  []uint64      `json:"timestamps"`
}

type readMarkJSON struct {
	Sender    recipientJSON `json:"sender"`
	Timestamp uint64        `json:"timestamp"`
}

type readSyncDoc struct {
	eventHead

	Timestamp uint64         `json:"timestamp"`
	Time      time.Time      `json:"time,omitzero"`
	Messages  []readMarkJSON `json:"messages"`
}

type unsupportedDoc struct {
	eventHead
	envelopeJSON

	Content string `json:"content"`
}

type decryptionFailureDoc struct {
	eventHead

	Sender    recipientJSON `json:"sender"`
	Timestamp uint64        `json:"timestamp"`
	Time      time.Time     `json:"time,omitzero"`
	Error     string        `json:"error,omitempty"`
}

type connectionDoc struct {
	eventHead

	State string `json:"state"`
	Error string `json:"error,omitempty"`
}

//nolint:cyclop // one case per event type
func (p *Printer) eventDoc(evt signal.Event) any {
	switch evt := evt.(type) {
	case *signal.Message:
		return p.messageDocOf(evt, nil)
	case *signal.Edit:
		return editDoc{
			eventHead: head(typeEdit), envelopeJSON: p.envelope(evt.Envelope),
			TargetTimestamp: evt.TargetTimestamp, Body: evt.Body,
		}
	case *signal.Delete:
		return deleteDoc{
			eventHead: head(typeDelete), envelopeJSON: p.envelope(evt.Envelope), TargetTimestamp: evt.TargetTimestamp,
		}
	case *signal.Reaction:
		return reactionDoc{
			eventHead: head(typeReaction), envelopeJSON: p.envelope(evt.Envelope),
			Emoji: evt.Emoji, Remove: evt.Remove,
			TargetAuthor: p.recipient(evt.TargetAuthor), TargetTimestamp: evt.TargetTimestamp,
		}
	case *signal.Typing:
		return typingDoc{eventHead: head(typeTyping), envelopeJSON: p.envelope(evt.Envelope), Action: typingAction(evt)}
	case *signal.Receipt:
		timestamps := evt.Timestamps
		if timestamps == nil {
			timestamps = []uint64{}
		}

		return receiptDoc{
			eventHead: head(typeReceipt), Sender: p.recipient(evt.Sender),
			ReceiptType: evt.Type.String(), Timestamps: timestamps,
		}
	case *signal.ReadSync:
		marks := make([]readMarkJSON, 0, len(evt.Messages))
		for _, mark := range evt.Messages {
			marks = append(marks, readMarkJSON{Sender: p.recipient(mark.Sender), Timestamp: mark.Timestamp})
		}

		return readSyncDoc{
			eventHead: head(typeReadSync), Timestamp: evt.Timestamp, Time: msTime(evt.Timestamp), Messages: marks,
		}
	case *signal.Unsupported:
		return unsupportedDoc{eventHead: head(typeUnsupported), envelopeJSON: p.envelope(evt.Envelope), Content: evt.Type}
	case *signal.DecryptionFailure:
		return decryptionFailureDoc{
			eventHead: head(typeDecryptionFailure), Sender: p.recipient(evt.Sender),
			Timestamp: evt.Timestamp, Time: msTime(evt.Timestamp), Error: errorText(evt.Err),
		}
	case *signal.QueueEmpty:
		return head(typeQueueEmpty)
	case *signal.Connection:
		return connectionDoc{eventHead: head(typeConnection), State: evt.State.String(), Error: errorText(evt.Err)}
	default:
		return unsupportedDoc{eventHead: head(typeUnsupported), Content: fmt.Sprintf("%T", evt)}
	}
}

// messageDocOf renders msg; saved is nil or has one entry per attachment.
func (p *Printer) messageDocOf(msg *signal.Message, saved []app.SavedAttachment) messageDoc {
	doc := messageDoc{
		eventHead:    head(typeMessage),
		envelopeJSON: p.envelope(msg.Envelope),
		Body:         msg.Body,
		ViewOnce:     msg.ViewOnce,
		Unsupported:  msg.Unsupported,
	}

	for i, att := range msg.Attachments {
		doc.Attachments = append(doc.Attachments, attachmentJSON{
			ContentType: att.ContentType, Filename: att.Filename, Size: att.Size, Caption: att.Caption,
		})

		if i < len(saved) {
			doc.Attachments[i].Path = saved[i].Path
			doc.Attachments[i].DownloadError = errorText(saved[i].Err)
		}
	}

	if msg.Sticker != nil {
		doc.Sticker = &stickerJSON{PackID: msg.Sticker.PackID, StickerID: msg.Sticker.StickerID, Emoji: msg.Sticker.Emoji}
	}

	if msg.Quote != nil {
		doc.Quote = &quoteJSON{
			Author: p.recipient(msg.Quote.Author), Timestamp: msg.Quote.Timestamp, Text: msg.Quote.Text,
		}
	}

	return doc
}

func typingAction(evt *signal.Typing) string {
	if evt.Started {
		return "started"
	}

	return "stopped"
}

func errorText(err error) string {
	if err == nil {
		return ""
	}

	return err.Error()
}

// msTime converts a Signal timestamp (ms since epoch) to UTC; 0 means unknown.
func msTime(ms uint64) time.Time {
	if ms == 0 || ms > math.MaxInt64 {
		return time.Time{}
	}

	return time.UnixMilli(int64(ms)).UTC()
}

// self stands for our own account in plain output.
const self = "me"

// eventLine renders evt for plain output; "" means evt isn't printed.
//
//nolint:cyclop // one case per event type
func (p *Printer) eventLine(evt signal.Event) string {
	switch evt := evt.(type) {
	case *signal.Message:
		return p.envelopeLine(evt.Envelope, p.messageText(evt, nil))
	case *signal.Edit:
		return p.envelopeLine(evt.Envelope,
			"[edit of message sent "+p.msDateTime(evt.TargetTimestamp)+"] "+oneLine(evt.Body))
	case *signal.Delete:
		return p.envelopeLine(evt.Envelope, "[deleted message sent "+p.msDateTime(evt.TargetTimestamp)+"]")
	case *signal.Reaction:
		return p.envelopeLine(evt.Envelope, p.reactionText(evt))
	case *signal.Typing:
		if evt.Started {
			return p.envelopeLine(evt.Envelope, "[typing]")
		}

		return p.envelopeLine(evt.Envelope, "[stopped typing]")
	case *signal.Receipt:
		return p.receiptLine(evt)
	case *signal.ReadSync:
		return p.readSyncLine(evt)
	case *signal.Unsupported:
		return p.envelopeLine(evt.Envelope, "[unsupported "+evt.Type+"]")
	case *signal.DecryptionFailure:
		return p.timePrefix(evt.Timestamp) + p.who(evt.Sender) + " → " + self + ": [decryption failed: " +
			oneLine(errorText(evt.Err)) + "]"
	case *signal.QueueEmpty, *signal.Connection:
		return ""
	default:
		return fmt.Sprintf("[unsupported %T]", evt)
	}
}

// envelopeLine is `[time] <sender> → <dest>: text`. Sync transcripts are sent by us ("me") to
// the chat; other 1:1 messages are sent to us. A sync event without a chat is `[time] me: text`.
func (p *Printer) envelopeLine(env signal.Envelope, text string) string {
	route := p.who(env.Sender) + " → " + self

	switch {
	case env.Chat.IsGroup() && env.Sync:
		route = self + " → group:" + env.Chat.GroupID
	case env.Chat.IsGroup():
		route = p.who(env.Sender) + " → group:" + env.Chat.GroupID
	case env.Sync && env.Chat.Recipient.IsZero():
		route = self
	case env.Sync && env.Chat.Recipient == env.Sender:
		route = self + " → " + self
	case env.Sync:
		route = self + " → " + p.who(env.Chat.Recipient)
	}

	return p.timePrefix(env.Timestamp) + route + ": " + text
}

// messageText renders msg; saved is nil or has one entry per attachment.
func (p *Printer) messageText(msg *signal.Message, saved []app.SavedAttachment) string {
	var parts []string

	if msg.Quote != nil {
		quote := "[quote " + p.who(msg.Quote.Author) + " " + p.msDateTime(msg.Quote.Timestamp)
		if msg.Quote.Text != "" {
			quote += ": " + oneLine(truncate(msg.Quote.Text, quoteLength))
		}

		parts = append(parts, quote+"]")
	}

	if msg.Body != "" {
		parts = append(parts, oneLine(msg.Body))
	}

	for i, att := range msg.Attachments {
		var outcome *app.SavedAttachment
		if i < len(saved) {
			outcome = &saved[i]
		}

		parts = append(parts, attachmentText(att, msg.ViewOnce, outcome))
	}

	if msg.Sticker != nil {
		sticker := "[sticker"
		if msg.Sticker.Emoji != "" {
			sticker += " " + oneLine(msg.Sticker.Emoji)
		}

		parts = append(parts, sticker+"]")
	}

	for _, name := range msg.Unsupported {
		parts = append(parts, "[unsupported "+name+"]")
	}

	return strings.Join(parts, " ")
}

// quoteLength is how many characters of a quoted message plain output shows.
const quoteLength = 40

// attachmentText is "[attachment <type> <size> <name>]", plus "→ <path>" or the download error
// when the attachment was saved (saved is not nil).
func attachmentText(att signal.Attachment, viewOnce bool, saved *app.SavedAttachment) string {
	words := []string{"attachment"}
	if viewOnce {
		words = []string{"view-once", "attachment"}
	}

	if att.ContentType != "" {
		words = append(words, oneLine(att.ContentType))
	}

	if att.Size > 0 {
		words = append(words, humanSize(att.Size))
	}

	if att.Filename != "" {
		words = append(words, oneLine(att.Filename))
	}

	switch {
	case saved == nil:
	case saved.Err != nil:
		words = append(words, "(download failed: "+oneLine(saved.Err.Error())+")")
	default:
		words = append(words, "→", oneLine(saved.Path))
	}

	return "[" + strings.Join(words, " ") + "]"
}

func (p *Printer) reactionText(evt *signal.Reaction) string {
	target := "message of " + p.who(evt.TargetAuthor) + " sent " + p.msDateTime(evt.TargetTimestamp)
	if evt.Remove {
		return "[removed reaction " + oneLine(evt.Emoji) + " from " + target + "]"
	}

	return "[reaction " + oneLine(evt.Emoji) + " to " + target + "]"
}

// receiptLine has no time: signalmeow doesn't pass on when the receipt was sent.
func (p *Printer) receiptLine(evt *signal.Receipt) string {
	sent := make([]string, 0, len(evt.Timestamps))
	for _, ts := range evt.Timestamps {
		sent = append(sent, p.msDateTime(ts))
	}

	noun := "messages"
	if len(sent) == 1 {
		noun = "message"
	}

	return p.who(evt.Sender) + " → " + self + ": [" + evt.Type.String() + " receipt for " + noun + " sent " +
		strings.Join(sent, ", ") + "]"
}

func (p *Printer) readSyncLine(evt *signal.ReadSync) string {
	marks := make([]string, 0, len(evt.Messages))
	for _, mark := range evt.Messages {
		marks = append(marks, p.who(mark.Sender)+" "+p.msDateTime(mark.Timestamp))
	}

	return p.timePrefix(evt.Timestamp) + self + ": [read on another device: " + strings.Join(marks, ", ") + "]"
}

// timePrefix is "[time] " for a Signal timestamp, or "" when it is unknown.
func (p *Printer) timePrefix(ms uint64) string {
	t := msTime(ms)
	if t.IsZero() {
		return ""
	}

	return "[" + p.dateTime(t) + "] "
}

// msDateTime formats a Signal timestamp for plain output.
func (p *Printer) msDateTime(ms uint64) string {
	return p.dateTime(msTime(ms))
}

// who names a user in plain output: "me" for the account, else the label from the names (see
// SetNames), else their ACI (or number).
func (p *Printer) who(rcpt signal.Recipient) string {
	switch {
	case rcpt.IsZero():
		return "unknown"
	case p.names.IsSelf(rcpt):
		return self
	}

	if label := p.names.Label(rcpt); label != "" {
		return oneLine(label)
	}

	return rcpt.String()
}

// Size units for humanSize.
const (
	kilo = 1000
	mega = kilo * kilo
	giga = mega * kilo
)

// humanSize formats a byte count with decimal units, e.g. "12.3 KB".
func humanSize(size uint32) string {
	switch {
	case size >= giga:
		return strconv.FormatFloat(float64(size)/giga, 'f', 1, 64) + " GB"
	case size >= mega:
		return strconv.FormatFloat(float64(size)/mega, 'f', 1, 64) + " MB"
	case size >= kilo:
		return strconv.FormatFloat(float64(size)/kilo, 'f', 1, 64) + " KB"
	default:
		return strconv.FormatUint(uint64(size), 10) + " B"
	}
}

// truncate shortens s to at most n characters, marking a cut with "…".
func truncate(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}

	return string(runes[:n-1]) + "…"
}

// oneLine escapes line breaks and other control characters, so that an event stays on one line
// and a message can't send escape sequences to the terminal.
func oneLine(s string) string {
	var out strings.Builder

	for _, char := range s {
		switch {
		case char == '\n':
			out.WriteString(`\n`)
		case char == '\r':
			out.WriteString(`\r`)
		case char == '\t':
			out.WriteString(`\t`)
		case unicode.IsControl(char) || unicode.Is(unicode.Bidi_Control, char):
			fmt.Fprintf(&out, `\u%04x`, char)
		default:
			out.WriteRune(char)
		}
	}

	return out.String()
}
