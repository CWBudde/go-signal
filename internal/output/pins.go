package output

import (
	"fmt"
	"strings"
	"time"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/signal"
)

const (
	typePin                = "pin"
	typeUnpin              = "unpin"
	pinCompletenessUnknown = "unknown"
)

type pinTargetJSON struct {
	TargetAuthor    recipientJSON `json:"targetAuthor"`
	TargetTimestamp uint64        `json:"targetTimestamp"`
}

type pinDurationJSON struct {
	DurationSeconds *uint32 `json:"durationSeconds,omitempty"`
	Forever         *bool   `json:"forever,omitempty"`
}

func pinDuration(operation string, seconds uint32, forever bool) pinDurationJSON {
	if operation != typePin {
		return pinDurationJSON{}
	}

	return pinDurationJSON{DurationSeconds: &seconds, Forever: &forever}
}

type pinDoc struct {
	eventHead
	envelopeJSON
	pinTargetJSON
	pinDurationJSON
}

type pinSendJSON struct {
	SendJSON
	pinTargetJSON
	pinDurationJSON

	Operation string `json:"operation"`
}

// PinSend prints pin and unpin delivery outcomes, preserving partial group results.
func (p *Printer) PinSend(res app.PinSendResult) error {
	if p.format != JSON {
		return p.sendTable(res.SendResult)
	}

	doc := pinSendJSON{
		SendJSON: p.sendToJSON(res.SendResult), Operation: res.Operation,
		pinTargetJSON: pinTargetJSON{
			TargetAuthor: p.recipient(res.TargetAuthor), TargetTimestamp: res.TargetTimestamp,
		},
		pinDurationJSON: pinDuration(res.Operation, res.DurationSeconds, res.Forever),
	}

	return p.writeJSON(struct {
		Version int         `json:"version"`
		Pin     pinSendJSON `json:"pin"`
	}{SchemaVersion, doc})
}

type pinObservationJSON struct {
	pinTargetJSON
	pinDurationJSON

	Operation     string        `json:"operation"`
	Sender        recipientJSON `json:"sender"`
	Timestamp     uint64        `json:"timestamp"`
	EntryID       int64         `json:"entryId"`
	ReceivedAt    time.Time     `json:"receivedAt"`
	ExpiresAt     time.Time     `json:"expiresAt,omitzero"`
	ExpiryReached *bool         `json:"expiryReached,omitempty"`
	TargetDeleted bool          `json:"targetDeleted"`
}

type pinStateJSON struct {
	Chat           ChatJSON             `json:"chat"`
	Observations   []pinObservationJSON `json:"observations"`
	Completeness   string               `json:"completeness"`
	Scanned        int                  `json:"scanned"`
	FirstEntryID   int64                `json:"firstEntryId"`
	LastEntryID    int64                `json:"lastEntryId"`
	Truncated      bool                 `json:"truncated"`
	IgnoredInvalid int                  `json:"ignoredInvalid"`
	Conflicts      int                  `json:"conflicts"`
}

// PinState prints retained observations with unknown completeness and local receipt-based expiry.
func (p *Printer) PinState(state app.PinState) error {
	if p.format != JSON {
		return p.pinStatePlain(state)
	}

	doc := pinStateJSON{
		Chat: NewChatJSON(state.Chat, p.names), Observations: make([]pinObservationJSON, 0, len(state.Observations)),
		Completeness: pinCompletenessUnknown, Scanned: state.Scanned,
		FirstEntryID: state.FirstEntryID, LastEntryID: state.LastEntryID,
		Truncated: state.Truncated, IgnoredInvalid: state.IgnoredInvalid, Conflicts: state.Conflicts,
	}
	for _, observation := range state.Observations {
		doc.Observations = append(doc.Observations, p.pinObservation(observation))
	}

	return p.writeJSON(struct {
		Version  int          `json:"version"`
		PinState pinStateJSON `json:"pinState"`
	}{SchemaVersion, doc})
}

func (p *Printer) pinObservation(observation app.PinObservation) pinObservationJSON {
	doc := pinObservationJSON{
		pinTargetJSON: pinTargetJSON{
			TargetAuthor: p.recipient(observation.TargetAuthor), TargetTimestamp: observation.TargetTimestamp,
		},
		Operation: observation.Operation, Sender: p.recipient(observation.Sender), Timestamp: observation.Timestamp,
		EntryID: observation.EntryID, ReceivedAt: utc(observation.ReceivedAt), TargetDeleted: observation.TargetDeleted,
		pinDurationJSON: pinDuration(observation.Operation, observation.DurationSeconds, observation.Forever),
	}
	if observation.Operation == typePin && !observation.Forever && !observation.ExpiresAt.IsZero() {
		doc.ExpiresAt = utc(observation.ExpiresAt)
		doc.ExpiryReached = &observation.ExpiryReached
	}

	return doc
}

func (p *Printer) pinStatePlain(state app.PinState) error {
	chat := p.who(state.Chat.Recipient)
	if state.Chat.IsGroup() {
		chat = p.groupLabel(state.Chat.GroupID)
	}

	lines := []string{
		"Pin retained observations in " + chat + "; completeness: unknown.",
		"Finite expiry is receipt-based on this client's local observations.",
		fmt.Sprintf("Scanned %d entries (IDs %d–%d); truncated: %t; ignored invalid: %d; conflicts: %d.",
			state.Scanned, state.FirstEntryID, state.LastEntryID, state.Truncated, state.IgnoredInvalid, state.Conflicts),
	}
	if len(state.Observations) == 0 {
		lines = append(lines, "No retained observations.")
	}

	for _, observation := range state.Observations {
		lines = append(lines, p.pinObservationPlain(observation))
	}

	return p.writeLine(strings.Join(lines, "\n"))
}

func (p *Printer) pinObservationPlain(observation app.PinObservation) string {
	line := fmt.Sprintf("%s %s:%d; sender: %s; timestamp: %d; entry ID: %d; received: %s; target deleted: %t",
		oneLine(observation.Operation), p.who(observation.TargetAuthor), observation.TargetTimestamp,
		p.who(observation.Sender), observation.Timestamp, observation.EntryID,
		p.dateTime(observation.ReceivedAt), observation.TargetDeleted)
	if observation.Operation == typePin {
		line += "; duration: " + pinDurationText(observation.DurationSeconds, observation.Forever)
		if !observation.Forever && !observation.ExpiresAt.IsZero() {
			line += fmt.Sprintf("; receipt-based expiry: %s; expiry reached: %t",
				p.dateTime(observation.ExpiresAt), observation.ExpiryReached)
		}
	}

	return line
}

func pinDurationText(seconds uint32, forever bool) string {
	if forever {
		return "forever"
	}

	return fmt.Sprintf("%d seconds", seconds)
}

func (p *Printer) pinText(pin *signal.Pin) string {
	return fmt.Sprintf("[pin %s:%d; %s]", p.who(pin.TargetAuthor), pin.TargetTimestamp,
		pinDurationText(pin.DurationSeconds, pin.Forever))
}
