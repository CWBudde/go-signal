package output

import (
	"fmt"
	"slices"

	"github.com/cwbudde/go-signal/internal/app"
)

type storySendJSON struct {
	SendJSON

	AllowsReplies bool `json:"allowsReplies"`
}

// StorySend prints submission outcomes for a group story, including partial peer delivery.
func (p *Printer) StorySend(res app.StorySendResult) error {
	if p.format != JSON {
		return p.storySendTable(res.SendResult)
	}

	return p.writeJSON(struct {
		Version   int           `json:"version"`
		StorySend storySendJSON `json:"storySend"`
	}{
		Version:   SchemaVersion,
		StorySend: storySendJSON{SendJSON: p.sendToJSON(res.SendResult), AllowsReplies: res.AllowsReplies},
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
