package output

import (
	"fmt"
	"text/tabwriter"

	"github.com/cwbudde/go-signal/internal/app"
)

// CheckJSON is the "check" object of docs/json.md.
type CheckJSON struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
	Hint   string `json:"hint,omitempty"`
}

// DoctorJSON is the "doctor" object of docs/json.md. The MCP server's doctor tool returns it as
// structured content.
type DoctorJSON struct {
	Healthy bool        `json:"healthy"`
	Checks  []CheckJSON `json:"checks"`
}

type doctorDoc struct {
	Version int        `json:"version"`
	Doctor  DoctorJSON `json:"doctor"`
}

// NewDoctorJSON converts a health report to its JSON form.
func NewDoctorJSON(checks []app.Check) DoctorJSON {
	out := DoctorJSON{Healthy: app.DoctorError(checks) == nil, Checks: make([]CheckJSON, 0, len(checks))}
	for _, check := range checks {
		out.Checks = append(out.Checks, CheckJSON{
			Name: check.Name, Status: string(check.Status), Detail: check.Detail, Hint: check.Hint,
		})
	}

	return out
}

// Doctor prints a health report (`mcp doctor`): one line per check, its hint below it.
func (p *Printer) Doctor(checks []app.Check) error {
	if p.format == JSON {
		return p.writeJSON(doctorDoc{Version: SchemaVersion, Doctor: NewDoctorJSON(checks)})
	}

	table := tabwriter.NewWriter(p.w, 0, 0, columnGap, ' ', 0)

	for _, check := range checks {
		// The widest status is "warn": keep the columns in place whatever the statuses.
		fmt.Fprintf(table, "%-4s\t%s\t%s\n", check.Status, check.Name, orDash(check.Detail))

		if check.Hint != "" {
			fmt.Fprintf(table, "\t\thint: %s\n", check.Hint)
		}
	}

	return flush(table)
}
