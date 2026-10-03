package output

import (
	"fmt"
	"strconv"

	"github.com/cwbudde/go-signal/internal/signal"
)

type groupJoinJSON struct {
	ID       string                 `json:"id"`
	Title    string                 `json:"title"`
	Revision uint32                 `json:"revision"`
	Status   signal.GroupJoinStatus `json:"status"`
	Changed  bool                   `json:"changed"`
	Accepted bool                   `json:"accepted"`
	Verified bool                   `json:"verified"`
}

type groupJoinDoc struct {
	Version   int           `json:"version"`
	GroupJoin groupJoinJSON `json:"groupJoin"`
}

// GroupJoin prints fresh membership or request evidence without any invite secrets.
func (p *Printer) GroupJoin(result signal.GroupJoinResult) error {
	if p.format == JSON {
		return p.writeJSON(groupJoinDoc{Version: SchemaVersion, GroupJoin: groupJoinJSON{
			ID: result.ID, Title: result.Title, Revision: result.Revision, Status: result.Status,
			Changed: result.Changed, Accepted: result.Accepted, Verified: result.Verified,
		}})
	}

	heading := "Already a member"
	if result.Status == signal.GroupJoinRequesting {
		heading = "Already requested"
		if result.Changed {
			heading = "Join requested"
		}
	} else if result.Changed {
		heading = "Joined group"
	}

	return p.writeLine(fmt.Sprintf("%s\nID: %s\nTitle: %s\nRevision: %d",
		heading, result.ID, strconv.Quote(result.Title), result.Revision))
}
