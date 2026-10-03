package output

import (
	"fmt"
	"strconv"

	"github.com/cwbudde/go-signal/internal/signal"
)

type groupAcceptJSON struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Revision uint32 `json:"revision"`
	Changed  bool   `json:"changed"`
	Accepted bool   `json:"accepted"`
	Verified bool   `json:"verified"`
}

type groupAcceptDoc struct {
	Version     int             `json:"version"`
	GroupAccept groupAcceptJSON `json:"groupAccept"`
}

// GroupAccept prints fresh invitation acceptance or full membership evidence.
func (p *Printer) GroupAccept(result signal.GroupAcceptResult) error {
	if p.format == JSON {
		return p.writeJSON(groupAcceptDoc{Version: SchemaVersion, GroupAccept: groupAcceptJSON{
			ID: result.ID, Title: result.Title, Revision: result.Revision,
			Changed: result.Changed, Accepted: result.Accepted, Verified: result.Verified,
		}})
	}

	heading := "Already a member"
	if result.Changed {
		heading = "Invitation accepted"
	}

	return p.writeLine(fmt.Sprintf("%s\nID: %s\nTitle: %s\nRevision: %d",
		heading, result.ID, strconv.Quote(result.Title), result.Revision))
}
