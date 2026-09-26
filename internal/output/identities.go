package output

import (
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/mdp/qrterminal/v3"
)

// IdentityJSON is the "identity" object of docs/json.md. The MCP server can return it as
// structured content.
type IdentityJSON struct {
	ACI         string    `json:"aci"`
	Number      string    `json:"number,omitempty"`
	Username    string    `json:"username,omitempty"`
	Fingerprint string    `json:"fingerprint"`
	Trust       string    `json:"trust"`
	FirstSeen   time.Time `json:"firstSeen,omitzero"`
	ChangedAt   time.Time `json:"changedAt,omitzero"`
	// SafetyNumber and Scannable are only set by `identities show`.
	SafetyNumber string `json:"safetyNumber,omitempty"`
	Scannable    []byte `json:"scannable,omitempty"`
}

// NewIdentityJSON converts id to its JSON form.
func NewIdentityJSON(identity signal.Identity) IdentityJSON {
	return IdentityJSON{
		ACI:         identity.Recipient.ACI,
		Number:      identity.Recipient.Number,
		Username:    identity.Recipient.Username,
		Fingerprint: identity.Fingerprint,
		Trust:       identity.Trust.String(),
		FirstSeen:   utc(identity.FirstSeen),
		ChangedAt:   utc(identity.ChangedAt),
	}
}

type identitiesDoc struct {
	Version    int            `json:"version"`
	Identities []IdentityJSON `json:"identities"`
}

type identityDoc struct {
	Version  int          `json:"version"`
	Identity IdentityJSON `json:"identity"`
}

// Identities prints the stored identity keys (`identities list`).
func (p *Printer) Identities(ids []signal.Identity) error {
	if p.format == JSON {
		doc := identitiesDoc{Version: SchemaVersion, Identities: make([]IdentityJSON, 0, len(ids))}
		for _, id := range ids {
			doc.Identities = append(doc.Identities, NewIdentityJSON(id))
		}

		return p.writeJSON(doc)
	}

	table := tabwriter.NewWriter(p.w, 0, 0, columnGap, ' ', 0)
	fmt.Fprintln(table, "RECIPIENT\tFINGERPRINT\tTRUST\tFIRST SEEN\tCHANGED")

	for _, id := range ids {
		fmt.Fprintf(table, "%s\t%s\t%s\t%s\t%s\n", p.who(id.Recipient), orDash(id.Fingerprint), id.Trust,
			p.dateTime(id.FirstSeen), p.dateTime(id.ChangedAt))
	}

	return flush(table)
}

// SafetyNumber prints the safety number with a user and their identity key (`identities show`).
// Plain output adds the QR code the Signal apps scan, and what to do next.
func (p *Printer) SafetyNumber(number signal.SafetyNumber) error {
	if p.format == JSON {
		out := NewIdentityJSON(number.Identity)
		out.SafetyNumber, out.Scannable = number.Number, number.Scannable

		return p.writeJSON(identityDoc{Version: SchemaVersion, Identity: out})
	}

	err := p.identityTable(number.Identity)
	if err != nil {
		return err
	}

	const blocksPerLine = 4

	blocks := signal.GroupSafetyNumber(number.Number)

	var text strings.Builder

	text.WriteString("\nSafety number:\n")

	for len(blocks) > 0 {
		n := min(blocksPerLine, len(blocks))
		text.WriteString("  " + strings.Join(blocks[:n], " ") + "\n")
		blocks = blocks[n:]
	}

	text.WriteString("\n")

	_, err = fmt.Fprint(p.w, text.String())
	if err != nil {
		return fmt.Errorf("write output: %w", err)
	}

	if len(number.Scannable) > 0 {
		qrterminal.GenerateHalfBlock(string(number.Scannable), qrterminal.L, p.w)
	}

	aci := number.Identity.Recipient.ACI

	_, err = fmt.Fprintf(p.w, "Compare this with the safety number in Signal on your phone (open the chat, tap the "+
		"name, View safety number), or scan the code there. If they match, run:\n"+
		"  go-signal identities trust %s --safety-number %s\n", aci, number.Number)
	if err != nil {
		return fmt.Errorf("write output: %w", err)
	}

	return nil
}

// TrustedIdentity confirms `identities trust`.
func (p *Printer) TrustedIdentity(identity signal.Identity) error {
	if p.format == JSON {
		return p.writeJSON(identityDoc{Version: SchemaVersion, Identity: NewIdentityJSON(identity)})
	}

	how := "verified by its safety number"
	if identity.Trust != signal.TrustVerified {
		how = "not verified; compare the safety number with `go-signal identities show " + identity.Recipient.ACI + "`"
	}

	_, err := fmt.Fprintf(p.w, "Trusted the identity key of %s (%s).\n", p.who(identity.Recipient), how)
	if err != nil {
		return fmt.Errorf("write output: %w", err)
	}

	return nil
}

// identityTable prints the details of identity as "Key: value" lines.
func (p *Printer) identityTable(identity signal.Identity) error {
	table := tabwriter.NewWriter(p.w, 0, 0, 1, ' ', 0)
	if name := p.who(identity.Recipient); name != identity.Recipient.ACI {
		fmt.Fprintf(table, "Recipient:\t%s\n", name)
	}

	fmt.Fprintf(table, "ACI:\t%s\n", orDash(identity.Recipient.ACI))
	fmt.Fprintf(table, "Fingerprint:\t%s\n", orDash(identity.Fingerprint))
	fmt.Fprintf(table, "Trust:\t%s\n", identity.Trust)
	fmt.Fprintf(table, "First seen:\t%s\n", p.dateTime(identity.FirstSeen))
	fmt.Fprintf(table, "Changed:\t%s\n", p.dateTime(identity.ChangedAt))

	return flush(table)
}
