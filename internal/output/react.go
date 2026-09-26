package output

import "github.com/cwbudde/go-signal/internal/app"

// ReactJSON is the "react" object of docs/json.md: the results as in "send", plus the reaction.
type ReactJSON struct {
	Emoji           string           `json:"emoji"`
	Remove          bool             `json:"remove"`
	TargetAuthor    recipientJSON    `json:"targetAuthor"`
	TargetTimestamp uint64           `json:"targetTimestamp"`
	Timestamp       uint64           `json:"timestamp"`
	Results         []sendResultJSON `json:"results"`
}

type reactResultDoc struct {
	Version int       `json:"version"`
	React   ReactJSON `json:"react"`
}

// DeleteJSON is the "delete" object of docs/json.md: the results as in "send", plus the target.
type DeleteJSON struct {
	TargetTimestamp uint64           `json:"targetTimestamp"`
	Timestamp       uint64           `json:"timestamp"`
	Results         []sendResultJSON `json:"results"`
}

type deleteResultDoc struct {
	Version int        `json:"version"`
	Delete  DeleteJSON `json:"delete"`
}

// NewReactJSON converts res to the "react" object, naming users from names.
func NewReactJSON(res app.ReactResult, names app.Names) ReactJSON {
	printer := &Printer{names: names}

	return printer.reactToJSON(res)
}

// NewDeleteJSON converts res to the "delete" object, naming users from names.
func NewDeleteJSON(res app.DeleteResult, names app.Names) DeleteJSON {
	printer := &Printer{names: names}

	return printer.deleteToJSON(res)
}

// React prints the outcome of `react` like Send, one line (or JSON entry) per recipient.
func (p *Printer) React(res app.ReactResult) error {
	if p.format == JSON {
		return p.writeJSON(reactResultDoc{Version: SchemaVersion, React: p.reactToJSON(res)})
	}

	return p.sendTable(res.SendResult)
}

// Delete prints the outcome of `delete` like Send, one line (or JSON entry) per recipient.
func (p *Printer) Delete(res app.DeleteResult) error {
	if p.format == JSON {
		return p.writeJSON(deleteResultDoc{Version: SchemaVersion, Delete: p.deleteToJSON(res)})
	}

	return p.sendTable(res.SendResult)
}

func (p *Printer) reactToJSON(res app.ReactResult) ReactJSON {
	sent := p.sendToJSON(res.SendResult)

	return ReactJSON{
		Emoji:           res.Emoji,
		Remove:          res.Remove,
		TargetAuthor:    p.recipient(res.TargetAuthor.Recipient),
		TargetTimestamp: res.TargetTimestamp,
		Timestamp:       sent.Timestamp,
		Results:         sent.Results,
	}
}

func (p *Printer) deleteToJSON(res app.DeleteResult) DeleteJSON {
	sent := p.sendToJSON(res.SendResult)

	return DeleteJSON{TargetTimestamp: res.TargetTimestamp, Timestamp: sent.Timestamp, Results: sent.Results}
}
