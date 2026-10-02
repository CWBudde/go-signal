package output

import (
	"fmt"
	"strconv"
	"text/tabwriter"

	"github.com/cwbudde/go-signal/internal/signal"
)

// GroupLinkJSON contains the explicit link command's result. The URL is omitted
// for inactive links; ordinary group documents never contain this object.
type GroupLinkJSON struct {
	ID       string `json:"id"`
	Revision uint32 `json:"revision"`
	State    string `json:"state"`
	URL      string `json:"url,omitempty"`
}

type groupLinkDoc struct {
	Version   int           `json:"version"`
	GroupLink GroupLinkJSON `json:"groupLink"`
}

// GroupLink prints the explicitly requested link state and active URL.
func (p *Printer) GroupLink(link signal.GroupLink) error {
	url := ""
	if link.State == signal.GroupLinkEnabled || link.State == signal.GroupLinkApproval {
		url = link.URL
	}

	if p.format == JSON {
		return p.writeJSON(groupLinkDoc{Version: SchemaVersion, GroupLink: GroupLinkJSON{
			ID: link.ID, Revision: link.Revision, State: string(link.State), URL: url,
		}})
	}

	table := tabwriter.NewWriter(p.w, 0, 0, 1, ' ', 0)
	fmt.Fprintf(table, "Group:\t%s\n", strconv.Quote(link.ID))
	fmt.Fprintf(table, "Revision:\t%d\n", link.Revision)
	fmt.Fprintf(table, "Link state:\t%s\n", strconv.Quote(string(link.State)))

	if url != "" {
		fmt.Fprintf(table, "URL:\t%s\n", strconv.Quote(url))
	}

	return flush(table)
}
