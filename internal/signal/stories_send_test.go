//nolint:goconst // Literal expectations are independent story fixtures.
package signal_test

import (
	"errors"
	"testing"

	"github.com/cwbudde/go-signal/internal/signal"
)

const storyGroupID = "Z3JvdXAtaWQtZ3JvdXAtaWQtZ3JvdXAtaWQtZ3JvdXA="

func TestOutgoingStoryCheck(t *testing.T) {
	t.Parallel()

	for _, req := range []signal.SendRequest{
		{GroupID: storyGroupID, Story: &signal.OutgoingStory{Text: "Today"}},
		{
			GroupID: storyGroupID,
			Story:   &signal.OutgoingStory{File: &signal.UploadedAttachment{ID: "uploaded-card-media", ContentType: pngType}},
		},
	} {
		err := req.Check()
		if err != nil {
			t.Fatalf("valid story: %v", err)
		}
	}

	for _, req := range []signal.SendRequest{
		{
			Recipients: []signal.Recipient{{ACI: aliceACI}},
			Story:      &signal.OutgoingStory{Text: "Today"},
		},
		{GroupID: storyGroupID, Story: &signal.OutgoingStory{}},
		{GroupID: storyGroupID, Story: &signal.OutgoingStory{Text: " "}},
		{
			GroupID: storyGroupID,
			Story: &signal.OutgoingStory{
				Text: "Today",
				File: &signal.UploadedAttachment{ID: "uploaded-card-media", ContentType: pngType},
			},
		},
		{
			GroupID: storyGroupID,
			Story:   &signal.OutgoingStory{File: &signal.UploadedAttachment{ContentType: pngType}},
		},
		{
			GroupID: storyGroupID,
			Story: &signal.OutgoingStory{File: &signal.UploadedAttachment{
				ID: "uploaded-card-media", ContentType: "application/pdf",
			}},
		},
		{GroupID: storyGroupID, Body: "message", Story: &signal.OutgoingStory{Text: "Today"}},
		{GroupID: storyGroupID, EditTarget: 1, Story: &signal.OutgoingStory{Text: "Today"}},
		{
			GroupID:   storyGroupID,
			PollClose: &signal.OutgoingPollClose{},
			Story:     &signal.OutgoingStory{Text: "Today"},
		},
	} {
		err := req.Check()
		if !errors.Is(err, signal.ErrInvalidStory) {
			t.Fatalf("invalid story: %+v: %v", req, err)
		}
	}
}
