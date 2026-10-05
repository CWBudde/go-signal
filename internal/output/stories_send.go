package output

import (
	"fmt"
	"slices"

	"github.com/cwbudde/go-signal/internal/app"
)

type storySendJSON struct {
	SendJSON

	AllowsReplies      bool   `json:"allowsReplies"`
	DistributionListID string `json:"distributionListId,omitempty"`
	StorageVersion     uint64 `json:"storageVersion,omitempty"`
	SyncError          string `json:"syncError,omitempty"`
}

// StorySend prints submission outcomes for a group or private story, including partial peer delivery.
func (p *Printer) StorySend(res app.StorySendResult) error {
	if p.format != JSON {
		return p.privateStorySendTable(res)
	}

	syncError := ""
	if res.SyncErr != nil {
		syncError = res.SyncErr.Error()
	}

	return p.writeJSON(struct {
		Version   int           `json:"version"`
		StorySend storySendJSON `json:"storySend"`
	}{
		Version: SchemaVersion,
		StorySend: storySendJSON{
			SendJSON:           p.sendToJSON(res.SendResult),
			AllowsReplies:      res.AllowsReplies,
			DistributionListID: res.DistributionListID,
			StorageVersion:     res.StorageVersion,
			SyncError:          syncError,
		},
	})
}

func (p *Printer) storySendTable(res app.SendResult) error {
	res.Results = slices.Clone(res.Results)
	for i := range res.Results {
		result := &res.Results[i]
		if result.Err == nil || len(result.Members) == 0 {
			continue
		}

		_, peers := p.groupStatus(*result)
		result.Err = fmt.Errorf("peer submissions: %s; story transcript: %w", peers, result.Err)
	}

	return p.sendTable(res)
}

func (p *Printer) privateStorySendTable(res app.StorySendResult) error {
	if res.DistributionListID == "" {
		return p.storySendTable(res.SendResult)
	}

	_, err := fmt.Fprintf(p.w, "Audience: %s (storage version %d)\n", res.DistributionListID, res.StorageVersion)
	if err != nil {
		return fmt.Errorf("write story audience: %w", err)
	}

	err = p.sendTable(res.SendResult)
	if err != nil {
		return err
	}

	syncStatus := "sent"
	if res.SyncErr != nil {
		syncStatus = "failed: " + res.SyncErr.Error()
	}

	_, err = fmt.Fprintf(p.w, "Story transcript: %s\n", syncStatus)
	if err != nil {
		return fmt.Errorf("write story transcript: %w", err)
	}

	return nil
}
