package output

import (
	"fmt"
	"text/tabwriter"

	"github.com/cwbudde/go-signal/internal/app"
)

// SyncJSON is the "sync" object of docs/json.md.
type SyncJSON struct {
	Contacts    int      `json:"contacts"`
	Groups      int      `json:"groups"`
	MasterKey   bool     `json:"masterKey"`
	Storage     bool     `json:"storage"`
	ContactList bool     `json:"contactList"`
	Complete    bool     `json:"complete"`
	Missing     []string `json:"missing,omitempty"`
	Error       string   `json:"error,omitempty"`
}

type syncDoc struct {
	Version int      `json:"version"`
	Sync    SyncJSON `json:"sync"`
}

// NewSyncJSON converts res to its JSON form.
func NewSyncJSON(res app.SyncResult) SyncJSON {
	out := SyncJSON{
		Contacts:    res.Contacts,
		Groups:      res.Groups,
		MasterKey:   res.MasterKey,
		Storage:     res.Storage,
		ContactList: res.ContactList,
		Complete:    res.Incomplete == nil,
	}

	if res.Incomplete != nil {
		out.Missing = res.Missing()
		out.Error = res.Incomplete.Error()
	}

	return out
}

// Sync prints the outcome of `account sync`.
func (p *Printer) Sync(res app.SyncResult) error {
	if p.format == JSON {
		return p.writeJSON(syncDoc{Version: SchemaVersion, Sync: NewSyncJSON(res)})
	}

	table := tabwriter.NewWriter(p.w, 0, 0, 1, ' ', 0)
	fmt.Fprintf(table, "Contacts:\t%d\n", res.Contacts)
	fmt.Fprintf(table, "Groups:\t%d\n", res.Groups)
	fmt.Fprintf(table, "Storage key:\t%s\n", choose(res.MasterKey, "known", "unknown"))
	fmt.Fprintf(table, "Storage:\t%s\n", choose(res.Storage, "synced", "not synced"))
	fmt.Fprintf(table, "Contact list:\t%s\n", choose(res.ContactList, "received", "not received"))

	return flush(table)
}

// choose returns yes if cond holds and no otherwise.
func choose(cond bool, yes, no string) string {
	if cond {
		return yes
	}

	return no
}
