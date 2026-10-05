package signal_test

import (
	"errors"
	"testing"

	"github.com/cwbudde/go-signal/internal/signal"
)

const (
	privateStorySelf = "10000000-0000-4000-8000-000000000001"
	privateStoryPeer = "10000000-0000-4000-8000-000000000002"
)

func TestPrivateStoryRequest(t *testing.T) {
	t.Parallel()

	for _, id := range []string{signal.MyStoryID, "76543210-1234-4321-8234-123456789abc"} {
		req := signal.SendRequest{
			Recipients: []signal.Recipient{{ACI: privateStoryPeer}},
			Story:      &signal.OutgoingStory{DistributionListID: id, Text: "Private"},
		}

		err := req.Check()
		if err != nil {
			t.Fatal(err)
		}

		for _, invalid := range []signal.SendRequest{
			{Story: req.Story},
			{
				GroupID:    "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB=",
				Recipients: req.Recipients,
				Story:      req.Story,
			},
			{Recipients: []signal.Recipient{{PNI: privateStoryPeer}}, Story: req.Story},
			{
				Recipients: []signal.Recipient{{ACI: "malformed-story-recipient"}},
				Story:      req.Story,
			},
		} {
			if !errors.Is(invalid.Check(), signal.ErrInvalidStory) {
				t.Fatalf("accepted %+v", invalid)
			}
		}
	}
}
