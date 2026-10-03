package output

import (
	"fmt"
	"strconv"

	"github.com/cwbudde/go-signal/internal/signal"
)

type groupCancelRequestJSON struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Revision uint32 `json:"revision"`
	Changed  bool   `json:"changed"`
	Accepted bool   `json:"accepted"`
	Verified bool   `json:"verified"`
}

type groupCancelRequestDoc struct {
	Version            int                    `json:"version"`
	GroupCancelRequest groupCancelRequestJSON `json:"groupCancelRequest"`
}

// GroupCancelRequest prints signed request deletion or fresh preview no-op evidence.
func (p *Printer) GroupCancelRequest(result signal.GroupCancelRequestResult) error {
	if p.format == JSON {
		return p.writeJSON(groupCancelRequestDoc{Version: SchemaVersion, GroupCancelRequest: groupCancelRequestJSON{
			ID: result.ID, Title: result.Title, Revision: result.Revision,
			Changed: result.Changed, Accepted: result.Accepted, Verified: result.Verified,
		}})
	}

	heading := "No pending join request"
	if result.Changed {
		heading = "Join request cancelled"
	}

	return p.writeLine(fmt.Sprintf("%s\nID: %s\nTitle: %s\nRevision: %d",
		heading, result.ID, strconv.Quote(result.Title), result.Revision))
}
