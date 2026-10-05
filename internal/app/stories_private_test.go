package app_test

import (
	"errors"
	"testing"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/go-signal/internal/signal/signaltest"
)

func privateStoryDirectory() *signaltest.Fake {
	fake := directory()
	fake.StoryAudienceSnapshots = map[string]signal.StoryAudiences{
		testAccount().ACI: {
			StorageVersion: 12,

			Audiences: []signal.StoryAudience{
				{
					ID: signal.MyStoryID,

					AllowsReplies: true,

					Recipients: []signal.Recipient{{ACI: aliceACI}, {ACI: bobACI}},
				},
			},
		},
	}

	return fake
}

//nolint:cyclop // Independent assertions cover send and persistence boundaries.
func TestPrivateStoryMediaAndAllowlist(t *testing.T) {
	t.Parallel()

	for _, allowed := range []bool{false, true} {
		t.Run(map[bool]string{false: "denied", true: "allowed"}[allowed], func(t *testing.T) {
			t.Parallel()

			fake := privateStoryDirectory()

			entries := []string{aliceACI}
			if allowed {
				entries = append(entries, bobACI)
			}

			res, err := restricted(t, fake, entries...).StorySend(t.Context(), app.StorySendRequest{
				MyStory: true, Attachment: storyImage(t), NoReplies: true,
			})
			if !allowed {
				if !errors.Is(err, app.ErrRecipientNotAllowed) || len(fake.Uploaded()) != 0 || len(fake.Sent()) != 0 {
					t.Fatalf("allowed denied recipient: %v", err)
				}

				return
			}

			if err != nil ||
				len(fake.Uploaded()) != 1 ||
				len(fake.Sent()) != 1 ||
				len(res.Results) != 2 ||
				res.AllowsReplies ||
				fake.Sent()[0].Story.File == nil {
				t.Fatalf("media %+v %v", res, err)
			}
		})
	}
}

func TestPrivateStoryQueuedEvents(t *testing.T) {
	t.Parallel()

	fake := privateStoryDirectory()
	fake.Incoming = []signal.Event{&signal.Message{Body: "queued chat"}, &signal.Story{}}

	_, err := sender(t, fake).StorySend(t.Context(), app.StorySendRequest{MyStory: true, Text: "Private"})
	if err != nil || fake.Delivered() != 0 || len(fake.Receipts()) != 0 {
		t.Fatalf("acked queued events: %v", err)
	}
}
