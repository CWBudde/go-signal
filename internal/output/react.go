package output

import "github.com/cwbudde/go-signal/internal/app"

// reactResultJSON is the "react" object of docs/json.md: the results as in "send", plus the reaction.
type reactResultJSON struct {
	Emoji           string           `json:"emoji"`
	Remove          bool             `json:"remove"`
	TargetAuthor    recipientJSON    `json:"targetAuthor"`
	TargetTimestamp uint64           `json:"targetTimestamp"`
	Timestamp       uint64           `json:"timestamp"`
	Results         []sendResultJSON `json:"results"`
}

type reactResultDoc struct {
	Version int             `json:"version"`
	React   reactResultJSON `json:"react"`
}

// deleteResultJSON is the "delete" object of docs/json.md: the results as in "send", plus the target.
type deleteResultJSON struct {
	TargetTimestamp uint64           `json:"targetTimestamp"`
	Timestamp       uint64           `json:"timestamp"`
	Results         []sendResultJSON `json:"results"`
}

type deleteResultDoc struct {
	Version int              `json:"version"`
	Delete  deleteResultJSON `json:"delete"`
}

// React prints the outcome of `react` like Send, one line (or JSON entry) per recipient.
func (p *Printer) React(res app.ReactResult) error {
	if p.format == JSON {
		sent := sendToJSON(res.SendResult)

		return p.writeJSON(reactResultDoc{Version: SchemaVersion, React: reactResultJSON{
			Emoji:           res.Emoji,
			Remove:          res.Remove,
			TargetAuthor:    recipient(res.TargetAuthor.Recipient),
			TargetTimestamp: res.TargetTimestamp,
			Timestamp:       sent.Timestamp,
			Results:         sent.Results,
		}})
	}

	return p.sendTable(res.SendResult)
}

// Delete prints the outcome of `delete` like Send, one line (or JSON entry) per recipient.
func (p *Printer) Delete(res app.DeleteResult) error {
	if p.format == JSON {
		sent := sendToJSON(res.SendResult)

		return p.writeJSON(deleteResultDoc{Version: SchemaVersion, Delete: deleteResultJSON{
			TargetTimestamp: res.TargetTimestamp,
			Timestamp:       sent.Timestamp,
			Results:         sent.Results,
		}})
	}

	return p.sendTable(res.SendResult)
}
