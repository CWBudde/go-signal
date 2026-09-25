// Package output renders command results as plain text for people or as JSON for scripts. The
// JSON schema is documented in docs/json.md; every document carries SchemaVersion.
package output

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"
)

// SchemaVersion is the version of the JSON schema in docs/json.md. It changes only when a field
// is removed or changes meaning; adding fields keeps it.
const SchemaVersion = 1

// ErrInvalidFormat means -o/--output names an unknown format.
var ErrInvalidFormat = errors.New("invalid output format (want plain or json)")

// Format selects the renderer.
type Format string

// The supported formats.
const (
	Plain Format = "plain"
	JSON  Format = "json"
)

// ParseFormat validates an -o/--output value.
func ParseFormat(value string) (Format, error) {
	switch format := Format(value); format {
	case Plain, JSON:
		return format, nil
	default:
		return "", fmt.Errorf("%w: %q", ErrInvalidFormat, value)
	}
}

// Printer writes command results in one format.
type Printer struct {
	w      io.Writer
	format Format
	loc    *time.Location
}

// New returns a Printer writing to w. Plain output shows times in loc (nil means time.Local);
// JSON output always uses UTC.
func New(w io.Writer, format Format, loc *time.Location) *Printer {
	if loc == nil {
		loc = time.Local //nolint:gosmopolitan // plain output is for the local user
	}

	return &Printer{w: w, format: format, loc: loc}
}

// Format returns the printer's format.
func (p *Printer) Format() Format {
	return p.format
}

// writeJSON writes doc as one compact line.
func (p *Printer) writeJSON(doc any) error {
	err := json.NewEncoder(p.w).Encode(doc)
	if err != nil {
		return fmt.Errorf("write json: %w", err)
	}

	return nil
}

// dateTime formats t for plain output; zero means unknown.
func (p *Printer) dateTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}

	return t.In(p.loc).Format("2006-01-02 15:04:05 MST")
}

// date formats t as a day for plain output; zero means unknown.
func (p *Printer) date(t time.Time) string {
	if t.IsZero() {
		return "-"
	}

	return t.In(p.loc).Format(time.DateOnly)
}

// orDash returns s, or "-" when it is empty.
func orDash(s string) string {
	if s == "" {
		return "-"
	}

	return s
}

// utc converts t for JSON; the zero time stays zero so that omitzero drops it.
func utc(t time.Time) time.Time {
	if t.IsZero() {
		return t
	}

	return t.UTC()
}
