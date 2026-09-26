package output

import (
	"strings"
	"time"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/signal"
)

// InboxEntryJSON is an entry of the MCP server's inbox: an event with the ID that identifies it
// (and is the cursor after it) and when it was received.
type InboxEntryJSON struct {
	ID         string    `json:"id"`
	ReceivedAt time.Time `json:"receivedAt"`
	// Unread marks an incoming message that wasn't marked read yet.
	Unread bool `json:"unread,omitempty"`
	// Event is the event object of docs/json.md.
	Event any `json:"event"`
}

// NewInboxEntryJSON converts entry, naming users and groups from names.
func NewInboxEntryJSON(entry signal.InboxEntry, names app.Names) InboxEntryJSON {
	printer := &Printer{names: names}

	return InboxEntryJSON{
		ID: app.FormatCursor(entry.ID), ReceivedAt: utc(entry.ReceivedAt), Unread: entry.Unread,
		Event: printer.eventDoc(entry.Event),
	}
}

// InboxEntries prints inbox entries. Plain output is one line per entry: "#<id>", "(unread)" for
// unread messages, and the event as `receive` shows it. JSON output is one entry object per
// line.
func (p *Printer) InboxEntries(entries []signal.InboxEntry) error {
	for _, entry := range entries {
		var err error

		if p.format == JSON {
			err = p.writeJSON(NewInboxEntryJSON(entry, p.names))
		} else {
			err = p.writeLine(p.inboxLine(entry))
		}

		if err != nil {
			return err
		}
	}

	return nil
}

func (p *Printer) inboxLine(entry signal.InboxEntry) string {
	words := []string{"#" + app.FormatCursor(entry.ID)}
	if entry.Unread {
		words = append(words, "(unread)")
	}

	return strings.Join(append(words, p.eventLine(entry.Event)), " ")
}
