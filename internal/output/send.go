package output

import (
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/signal"
)

// Target types in the "send" document.
const (
	targetUser  = "user"
	targetSelf  = "self"
	targetGroup = "group"
)

// Statuses in plain send output.
const (
	statusSent    = "sent"
	statusFailed  = "failed"
	statusPartial = "partial"
)

// sendResultJSON is one entry of "results" in the "send" object of docs/json.md.
type sendResultJSON struct {
	Type         string       `json:"type"`
	Number       string       `json:"number,omitempty"`
	Username     string       `json:"username,omitempty"`
	ACI          string       `json:"aci,omitempty"`
	Name         string       `json:"name,omitempty"`
	GroupID      string       `json:"groupId,omitempty"`
	Timestamp    uint64       `json:"timestamp"`
	Success      bool         `json:"success"`
	Unidentified *bool        `json:"unidentified,omitempty"`
	Error        string       `json:"error,omitempty"`
	Members      []memberJSON `json:"members,omitempty"`
}

// memberJSON is the result for one group member.
type memberJSON struct {
	ACI          string `json:"aci,omitempty"`
	PNI          string `json:"pni,omitempty"`
	Name         string `json:"name,omitempty"`
	Success      bool   `json:"success"`
	Unidentified bool   `json:"unidentified"`
	Error        string `json:"error,omitempty"`
}

type sendJSON struct {
	Timestamp uint64           `json:"timestamp"`
	Results   []sendResultJSON `json:"results"`
}

type sendDoc struct {
	Version int      `json:"version"`
	Send    sendJSON `json:"send"`
}

// Send prints the outcome of `send`, one line (or JSON entry) per recipient.
func (p *Printer) Send(res app.SendResult) error {
	if p.format == JSON {
		return p.writeJSON(sendDoc{Version: SchemaVersion, Send: p.sendToJSON(res)})
	}

	return p.sendTable(res)
}

func (p *Printer) sendToJSON(res app.SendResult) sendJSON {
	out := sendJSON{Timestamp: res.Timestamp, Results: make([]sendResultJSON, 0, len(res.Results))}
	for _, result := range res.Results {
		out.Results = append(out.Results, p.sendResultToJSON(res.Timestamp, result))
	}

	return out
}

// sendTable prints the plain table of res, one line per recipient.
func (p *Printer) sendTable(res app.SendResult) error {
	table := tabwriter.NewWriter(p.w, 0, 0, columnGap, ' ', 0)
	fmt.Fprintln(table, "RECIPIENT\tTIMESTAMP\tSTATUS\tDETAILS")

	for _, result := range res.Results {
		status, details := p.sendStatus(result)
		fmt.Fprintf(table, "%s\t%d\t%s\t%s\n", p.recipientLabel(result.Target), res.Timestamp, status, details)
	}

	return flush(table)
}

func (p *Printer) sendResultToJSON(timestamp uint64, result app.TargetResult) sendResultJSON {
	target := result.Target
	out := sendResultJSON{Timestamp: timestamp, Success: result.OK(), Error: errText(result.Err)}

	switch {
	case target.IsGroup():
		out.Type, out.GroupID = targetGroup, target.GroupID

		for _, member := range result.Members {
			out.Members = append(out.Members, memberJSON{
				ACI:          member.Recipient.ACI,
				PNI:          member.Recipient.PNI,
				Name:         p.names.Name(member.Recipient),
				Success:      member.Err == nil,
				Unidentified: member.Unidentified,
				Error:        errText(member.Err),
			})
		}
	default:
		out.Type = targetUser
		if target.Self {
			out.Type = targetSelf
		}

		out.Number, out.Username, out.ACI = target.Recipient.Number, target.Recipient.Username, target.Recipient.ACI
		out.Name = p.names.Name(target.Recipient)
		unidentified := result.Unidentified
		out.Unidentified = &unidentified
	}

	return out
}

// recipientLabel names a target: by name if the printer knows it (see SetNames), else the way
// the user is likely to have written it.
func (p *Printer) recipientLabel(target app.Target) string {
	rcpt := target.Recipient

	switch {
	case target.IsGroup():
		return app.GroupPrefix + target.GroupID
	case target.Self:
		return app.SelfRecipient
	case p.names.Name(rcpt) != "":
		return oneLine(p.names.Label(rcpt))
	case rcpt.Number != "":
		return rcpt.Number
	case rcpt.Username != "":
		return "@" + rcpt.Username
	default:
		return rcpt.String()
	}
}

// sendStatus returns the STATUS and DETAILS columns of a plain send result.
func (p *Printer) sendStatus(result app.TargetResult) (string, string) {
	switch {
	case result.Err != nil:
		return statusFailed, result.Err.Error()
	case result.Target.IsGroup():
		return p.groupStatus(result)
	case result.Target.Self:
		return statusSent, "note to self"
	case result.Unidentified:
		return statusSent, "sealed sender"
	default:
		return statusSent, "-"
	}
}

func (p *Printer) groupStatus(result app.TargetResult) (string, string) {
	total := len(result.Members)

	failed := result.FailedMembers()
	if failed == 0 {
		return statusSent, fmt.Sprintf("%d members", total)
	}

	errs := make([]string, 0, failed)

	for _, member := range result.Members {
		if member.Err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", p.memberLabel(member.Recipient), member.Err))
		}
	}

	status := statusPartial
	if failed == total {
		status = statusFailed
	}

	return status, fmt.Sprintf("%d of %d members failed: %s", failed, total, strings.Join(errs, "; "))
}

func (p *Printer) memberLabel(rcpt signal.Recipient) string {
	if label := p.names.Label(rcpt); label != "" {
		return oneLine(label)
	}

	if rcpt.ACI != "" {
		return rcpt.ACI
	}

	return rcpt.String()
}

// errText returns err's message, or "" for nil.
func errText(err error) string {
	if err == nil {
		return ""
	}

	return err.Error()
}
