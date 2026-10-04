//go:build cgo || libsignal_go

package signal_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/internal/signal"
)

func TestStoryInboxSurvivesReopen(t *testing.T) {
	t.Parallel()
	dir := seedAccount(t)
	client := openInboxClient(t, dir)
	env := signal.Envelope{
		Sender:    signal.Recipient{ACI: "dddddddd-dddd-dddd-dddd-dddddddddddd"},
		Chat:      signal.Chat{GroupID: testGroupID},
		Timestamp: 1000,
	}
	story := &signal.Story{
		Envelope: env,
		Text: &signal.StoryText{
			Text:            "persisted story",
			BackgroundColor: new(uint32(0)),
			Preview: &signal.StoryPreview{
				URL: "https://stored-story.example.org",
				Image: &signal.Attachment{
					ContentType: pngType,
					Remote:      signal.RemoteAttachment{CDNKey: "stored-story-file", Key: []byte{1, 2}},
				},
			},
		},
	}

	stored, err := client.InboxAdd(t.Context(), signal.InboxEntry{
		ReceivedAt: time.UnixMilli(2000).UTC(),
		Time:       time.UnixMilli(1000).UTC(),
		Chat:       env.Chat,
		Event:      story,
	})
	if err != nil {
		t.Fatalf("store story: %v", err)
	}

	err = client.Close()
	if err != nil {
		t.Fatal(err)
	}

	client = openInboxClient(t, dir)

	entries, err := client.InboxList(t.Context(), signal.InboxQuery{Chat: env.Chat.Key()})
	if err != nil || len(entries) != 1 || !reflect.DeepEqual(entries[0], stored) {
		t.Fatalf("reopened story = %#v, %v", entries, err)
	}
}
