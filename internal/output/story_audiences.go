package output

import (
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/cwbudde/go-signal/internal/signal"
)

type storyAudienceJSON struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	IsBlockList   bool     `json:"isBlockList"`
	AllowsReplies bool     `json:"allowsReplies"`
	Recipients    []string `json:"recipients"`
}

// StoryAudiences displays the expanded private audience observed at one storage version.
func (p *Printer) StoryAudiences(snapshot signal.StoryAudiences) error {
	audiences := make([]storyAudienceJSON, 0, len(snapshot.Audiences))
	for _, audience := range snapshot.Audiences {
		recipients := make([]string, 0, len(audience.Recipients))
		for _, recipient := range audience.Recipients {
			recipients = append(recipients, recipient.ACI)
		}

		audiences = append(audiences, storyAudienceJSON{
			ID:            audience.ID,
			Name:          audience.Name,
			IsBlockList:   audience.IsBlockList,
			AllowsReplies: audience.AllowsReplies,
			Recipients:    recipients,
		})
	}

	if p.format == JSON {
		return p.writeJSON(struct {
			Version        int                 `json:"version"`
			StorageVersion uint64              `json:"storageVersion"`
			Audiences      []storyAudienceJSON `json:"storyAudiences"`
		}{SchemaVersion, snapshot.StorageVersion, audiences})
	}

	table := tabwriter.NewWriter(p.w, 0, 0, columnGap, ' ', 0)

	_, err := fmt.Fprintf(table, "Storage version: %d\nID\tNAME\tPOLICY\tREPLIES\tRECIPIENTS\n", snapshot.StorageVersion)
	if err != nil {
		return fmt.Errorf("write story audiences: %w", err)
	}

	for _, audience := range audiences {
		policy := "inclusions"
		if audience.IsBlockList {
			policy = "exclusions"
		}

		_, err = fmt.Fprintf(table, "%s\t%s\t%s\t%t\t%s\n",
			audience.ID, audience.Name, policy, audience.AllowsReplies, strings.Join(audience.Recipients, ", "))
		if err != nil {
			return fmt.Errorf("write story audience: %w", err)
		}
	}

	err = table.Flush()
	if err != nil {
		return fmt.Errorf("flush story audiences: %w", err)
	}

	return nil
}
