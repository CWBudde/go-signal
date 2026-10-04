package cmd_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/go-signal/internal/signal/signaltest"
)

func receivedStories() []signal.Event {
	env := signal.Envelope{
		Sender:    signal.Recipient{ACI: aliceACI},
		Chat:      signal.Chat{Recipient: signal.Recipient{ACI: aliceACI}},
		Timestamp: at(1),
	}
	image := func(key string) *signal.Attachment {
		return &signal.Attachment{
			ContentType: "image/png",
			Filename:    "story.png",
			Size:        3,
			Remote:      signal.RemoteAttachment{CDNKey: key, Key: []byte("secret-story-key"), Digest: []byte("secret-digest")},
		}
	}

	return []signal.Event{
		&signal.Story{
			Envelope:      env,
			AllowsReplies: true,
			Text: &signal.StoryText{
				Text:            "Hello\nStory",
				Style:           "bold",
				BackgroundColor: new(uint32(0)),
				Preview:         &signal.StoryPreview{URL: "https://example.org", Title: "Story link", Image: image("preview")},
			},
		},
		&signal.Story{Envelope: env, File: image("image")},
		&signal.Story{
			Envelope: signal.Envelope{
				Sender:    signal.Recipient{ACI: testAccount().ACI},
				Chat:      signal.Chat{GroupID: groupID},
				Timestamp: at(2),
				Sync:      true,
			},
			Text: &signal.StoryText{Text: "Shared from phone", Style: "regular"},
		},
		&signal.Message{Envelope: env, Body: "still receiving"},
	}
}

func TestReceiveStories(t *testing.T) {
	t.Parallel()

	for _, format := range []string{formatPlain, formatJSON} {
		t.Run(format, func(t *testing.T) {
			t.Parallel()

			fake := &signaltest.Fake{Linked: []signal.Account{*testAccount()}}

			out := receiveAllFrom(t, fake, receivedStories(), "-o", format, "--send-read-receipts")
			if !strings.Contains(out, "Hello") || !strings.Contains(out, "Shared from phone") ||
				strings.Contains(out, "secret-story-key") || strings.Contains(out, "secret-digest") {
				t.Fatalf("story output: %q", out)
			}

			if strings.Contains(out, "unsupported *signal.Story") || strings.Contains(out, "invalidStory") {
				t.Fatalf("story not rendered: %q", out)
			}

			receipts := fake.Receipts()
			if len(receipts) != 1 || len(receipts[0].Timestamps) != 1 {
				t.Fatalf("story sent read receipts: %#v", receipts)
			}

			golden(t, "receive_stories_"+format, out)
		})
	}
}

func TestReceiveStoryDownloads(t *testing.T) {
	t.Parallel()

	for _, format := range []string{formatPlain, formatJSON} {
		t.Run(format, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			fake := &signaltest.Fake{
				Linked:       []signal.Account{*testAccount()},
				Attachments:  map[string][]byte{"preview": []byte("IMG")},
				DownloadErrs: map[string]error{"image": errCDN},
			}
			out := receiveAllFrom(t, fake, receivedStories(), "-o", format, "--download-attachments", dir)

			data, err := os.ReadFile(filepath.Join(dir, "1789907401000-1-story.png"))
			if err != nil || string(data) != "IMG" {
				t.Fatalf("story preview download = %q, %v", data, err)
			}

			if !strings.Contains(out, "download") || !strings.Contains(out, "still receiving") ||
				strings.Contains(out, "secret-story-key") {
				t.Fatalf("download failure/output: %q", out)
			}

			golden(t, "receive_stories_saved_"+format, strings.ReplaceAll(out, dir, "$DIR"))
		})
	}
}
