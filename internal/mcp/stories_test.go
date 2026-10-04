package mcp_test

import (
	"strings"
	"testing"

	"github.com/cwbudde/go-signal/internal/mcp"
	"github.com/cwbudde/go-signal/internal/signal"
)

func TestStoryInboxTool(t *testing.T) {
	t.Parallel()

	fake := toolsFake()
	session := connectWith(t, fake, mcp.Options{}, testClient{})
	peer := signal.Recipient{ACI: aliceACI, Number: aliceNumber}
	story := &signal.Story{
		Envelope:      signal.Envelope{Sender: peer, Chat: signal.Chat{Recipient: peer}, Timestamp: 1000},
		AllowsReplies: true,
		Text: &signal.StoryText{
			Text:            "received story",
			Style:           "serif",
			BackgroundColor: new(uint32(0)),
			Preview: &signal.StoryPreview{
				URL: "https://story.example.org",
				Image: &signal.Attachment{
					ContentType: "image/jpeg",
					Remote:      signal.RemoteAttachment{Key: []byte("secret-story-tool-key"), CDNKey: "secret-story-tool-location"},
				},
			},
		},
	}

	got := waitForPush(t, session, fake, story)
	if len(got.Messages) != 1 || got.Messages[0].Unread || got.Messages[0].Event["type"] != "story" {
		t.Fatalf("story tool result: %#v", got)
	}

	var listed messages

	text := call(t, session, messagesList, nil, &listed)
	if !strings.Contains(text, "received story") || strings.Contains(text, "secret-story-tool") {
		t.Fatalf("unsafe or missing story tool text: %q", text)
	}

	if len(fake.Receipts()) != 0 {
		t.Fatal("story tool sent read receipts")
	}
}
