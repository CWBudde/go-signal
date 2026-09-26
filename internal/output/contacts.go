package output

import (
	"fmt"
	"text/tabwriter"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/signal"
)

// ContactJSON is the "contact" object of docs/json.md. The MCP server can return it as
// structured content.
type ContactJSON struct {
	ACI         string `json:"aci,omitempty"`
	PNI         string `json:"pni,omitempty"`
	Number      string `json:"number,omitempty"`
	Name        string `json:"name,omitempty"`
	Nickname    string `json:"nickname,omitempty"`
	ContactName string `json:"contactName,omitempty"`
	ProfileName string `json:"profileName,omitempty"`
	Blocked     bool   `json:"blocked"`
	// Accepted is whether we accepted their message request; nil if unknown.
	Accepted *bool `json:"messageRequestAccepted,omitempty"`
}

// NewContactJSON converts contact to its JSON form.
func NewContactJSON(contact signal.Contact) ContactJSON {
	return ContactJSON{
		ACI:         contact.ACI,
		PNI:         contact.PNI,
		Number:      contact.Number,
		Name:        contact.Name(),
		Nickname:    contact.Nickname,
		ContactName: contact.ContactName,
		ProfileName: contact.ProfileName,
		Blocked:     contact.Blocked,
		Accepted:    contact.Accepted,
	}
}

type contactsDoc struct {
	Version  int           `json:"version"`
	Contacts []ContactJSON `json:"contacts"`
}

type contactDoc struct {
	Version int         `json:"version"`
	Contact ContactJSON `json:"contact"`
}

// blockChangeJSON is one entry of "results" in the "block" object of docs/json.md.
type blockChangeJSON struct {
	ContactJSON

	Changed bool `json:"changed"`
}

type blockJSON struct {
	Blocked bool              `json:"blocked"`
	Results []blockChangeJSON `json:"results"`
}

type blockDoc struct {
	Version int       `json:"version"`
	Block   blockJSON `json:"block"`
}

// Contacts prints a list of contacts (`contacts list`), one line (or JSON entry) each.
func (p *Printer) Contacts(contacts []signal.Contact) error {
	if p.format == JSON {
		doc := contactsDoc{Version: SchemaVersion, Contacts: make([]ContactJSON, 0, len(contacts))}
		for _, contact := range contacts {
			doc.Contacts = append(doc.Contacts, NewContactJSON(contact))
		}

		return p.writeJSON(doc)
	}

	table := tabwriter.NewWriter(p.w, 0, 0, columnGap, ' ', 0)
	fmt.Fprintln(table, "NAME\tNUMBER\tACI\tBLOCKED")

	for _, contact := range contacts {
		fmt.Fprintf(table, "%s\t%s\t%s\t%s\n", orDash(oneLine(contact.Name())), orDash(contact.Number),
			orDash(contact.ACI), choose(contact.Blocked, "yes", "no"))
	}

	return flush(table)
}

// Contact prints one contact (`contacts show`).
func (p *Printer) Contact(contact signal.Contact) error {
	if p.format == JSON {
		return p.writeJSON(contactDoc{Version: SchemaVersion, Contact: NewContactJSON(contact)})
	}

	table := tabwriter.NewWriter(p.w, 0, 0, 1, ' ', 0)
	fmt.Fprintf(table, "Name:\t%s\n", orDash(oneLine(contact.Name())))
	fmt.Fprintf(table, "Nickname:\t%s\n", orDash(oneLine(contact.Nickname)))
	fmt.Fprintf(table, "Contact name:\t%s\n", orDash(oneLine(contact.ContactName)))
	fmt.Fprintf(table, "Profile name:\t%s\n", orDash(oneLine(contact.ProfileName)))
	fmt.Fprintf(table, "Number:\t%s\n", orDash(contact.Number))
	fmt.Fprintf(table, "ACI:\t%s\n", orDash(contact.ACI))
	fmt.Fprintf(table, "PNI:\t%s\n", orDash(contact.PNI))
	fmt.Fprintf(table, "Blocked:\t%s\n", choose(contact.Blocked, "yes", "no"))
	fmt.Fprintf(table, "Message request:\t%s\n", messageRequest(contact.Accepted))

	return flush(table)
}

// Block prints the outcome of `contacts block` and `contacts unblock`, one line (or JSON entry)
// per user.
func (p *Printer) Block(res app.BlockResult) error {
	if p.format == JSON {
		doc := blockDoc{Version: SchemaVersion, Block: blockJSON{
			Blocked: res.Blocked, Results: make([]blockChangeJSON, 0, len(res.Results)),
		}}
		for _, change := range res.Results {
			doc.Block.Results = append(doc.Block.Results, blockChangeJSON{
				ContactJSON: NewContactJSON(change.Contact), Changed: change.Changed,
			})
		}

		return p.writeJSON(doc)
	}

	table := tabwriter.NewWriter(p.w, 0, 0, columnGap, ' ', 0)
	fmt.Fprintln(table, "NAME\tNUMBER\tACI\tSTATUS")

	for _, change := range res.Results {
		contact := change.Contact
		fmt.Fprintf(table, "%s\t%s\t%s\t%s\n", orDash(oneLine(contact.Name())), orDash(contact.Number),
			orDash(contact.ACI), blockStatus(res.Blocked, change.Changed))
	}

	return flush(table)
}

func blockStatus(blocked, changed bool) string {
	switch {
	case blocked && changed:
		return "blocked"
	case blocked:
		return "already blocked"
	case changed:
		return "unblocked"
	default:
		return "was not blocked"
	}
}

func messageRequest(accepted *bool) string {
	switch {
	case accepted == nil:
		return "-"
	case *accepted:
		return "accepted"
	default:
		return "not accepted"
	}
}
