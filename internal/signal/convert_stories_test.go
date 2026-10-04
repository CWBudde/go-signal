//go:build cgo || libsignal_go

package signal_test

import (
	"math"
	"reflect"
	"testing"

	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow/events"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow/protobuf/signalpb"
	"github.com/google/uuid"
)

//nolint:funlen // rich input and expected wire fixtures
func TestConvertStory(t *testing.T) {
	t.Parallel()

	sender := uuid.MustParse("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
	env := signal.Envelope{
		Sender:          signal.Recipient{ACI: sender.String()},
		Chat:            signal.Chat{Recipient: signal.Recipient{ACI: sender.String()}},
		Timestamp:       123,
		ServerTimestamp: 456,
	}
	raw := &events.Story{
		Info: events.MessageInfo{Sender: sender, ChatID: sender.String(), ServerTimestamp: 456}, Timestamp: 123,
		Content: &signalpb.StoryMessage{AllowsReplies: new(true), Attachment: &signalpb.StoryMessage_TextAttachment{
			TextAttachment: &signalpb.TextAttachment{
				Text: new("Hello"), TextStyle: new(signalpb.TextAttachment_BOLD),
				TextForegroundColor: new(uint32(0)), TextBackgroundColor: new(uint32(42)),
				Background: &signalpb.TextAttachment_Gradient_{
					Gradient: &signalpb.TextAttachment_Gradient{
						StartColor: new(uint32(1)),
						EndColor:   new(uint32(2)),
						Angle:      new(uint32(90)),
						Colors:     []uint32{1, 2},
						Positions:  []float32{0, 1},
					},
				},
				Preview: &signalpb.Preview{
					Url:         new("https://example.org"),
					Title:       new("Link"),
					Description: new("Description"),
					Date:        new(uint64(99)),
					Image: &signalpb.AttachmentPointer{
						ContentType:          new(pngType),
						Key:                  []byte{1, 2},
						AttachmentIdentifier: &signalpb.AttachmentPointer_CdnKey{CdnKey: "story-link-preview"},
					},
				},
			},
		}},
	}
	want := &signal.Story{
		Envelope:      env,
		AllowsReplies: true,
		Text: &signal.StoryText{
			Text:                "Hello",
			Style:               "bold",
			ForegroundColor:     new(uint32(0)),
			TextBackgroundColor: new(uint32(42)),
			Gradient: &signal.StoryGradient{
				StartColor: new(uint32(1)),
				EndColor:   new(uint32(2)),
				Angle:      new(uint32(90)),
				Colors:     []uint32{1, 2},
				Positions:  []float32{0, 1},
			},
			Preview: &signal.StoryPreview{
				URL:         "https://example.org",
				Title:       "Link",
				Description: "Description",
				Date:        99,
				Image: &signal.Attachment{
					ContentType: pngType,
					Remote:      signal.RemoteAttachment{CDNKey: "story-link-preview", Key: []byte{1, 2}},
				},
			},
		},
	}

	got := signal.ConvertEvent(raw, "other")
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("story = %#v; want %#v", got, want)
	}

	story, ok := got.(*signal.Story)
	if !ok {
		t.Fatal("expected story")
	}
	// Presentation data belongs to the facade, including optional zero colors.
	card := raw.Content.GetTextAttachment()
	*card.TextForegroundColor = 9

	card.GetGradient().Colors[0] = 9
	if *story.Text.ForegroundColor != 0 || story.Text.Gradient.Colors[0] != 1 {
		t.Fatal("facade retained mutable wire presentation")
	}
}

//nolint:cyclop,funlen // table exercises every malformed and supported story variant
func TestConvertStoryVariants(t *testing.T) {
	t.Parallel()

	sender := uuid.MustParse("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")

	for _, testCase := range []struct {
		name    string
		content *signalpb.StoryMessage
		invalid bool
	}{
		{
			"media",
			&signalpb.StoryMessage{
				Attachment: &signalpb.StoryMessage_FileAttachment{
					FileAttachment: &signalpb.AttachmentPointer{
						ContentType: new("video/mp4"), Caption: new("caption"), Size: new(uint32(12)),
					},
				},
			},
			false,
		},
		{
			"solid background",
			&signalpb.StoryMessage{
				Attachment: &signalpb.StoryMessage_TextAttachment{
					TextAttachment: &signalpb.TextAttachment{Background: &signalpb.TextAttachment_Color{Color: 0}},
				},
			},
			false,
		},
		{"missing attachment", &signalpb.StoryMessage{}, true},
		{"nil content", nil, true},
		{
			"nonfinite gradient",
			&signalpb.StoryMessage{
				Attachment: &signalpb.StoryMessage_TextAttachment{
					TextAttachment: &signalpb.TextAttachment{
						Background: &signalpb.TextAttachment_Gradient_{
							Gradient: &signalpb.TextAttachment_Gradient{Positions: []float32{float32(math.Inf(1))}},
						},
					},
				},
			},
			true,
		},
		{
			"nil file",
			&signalpb.StoryMessage{Attachment: &signalpb.StoryMessage_FileAttachment{}},
			true,
		},
		{
			"nil text",
			&signalpb.StoryMessage{Attachment: &signalpb.StoryMessage_TextAttachment{}},
			true,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			raw := &events.Story{
				Info:      events.MessageInfo{Sender: sender, ChatID: "Zm9v"},
				Timestamp: 42,
				Content:   testCase.content,
			}

			got := signal.ConvertEvent(raw, sender.String())
			if testCase.invalid {
				u, ok := got.(*signal.Unsupported)
				if !ok || u.Type != "invalidStory" || !u.Sync || u.Timestamp != 42 {
					t.Fatalf("invalid story: %#v", got)
				}

				return
			}

			story, ok := got.(*signal.Story)
			if !ok || !story.Sync || story.Chat.GroupID != "Zm9v" || story.Timestamp != 42 {
				t.Fatalf("story metadata: %#v", got)
			}

			if testCase.name == "media" && (story.File == nil || story.File.Caption != "caption" || story.File.Size != 12) {
				t.Fatal("media lost")
			}

			if testCase.name == "solid background" &&
				(story.Text == nil || story.Text.BackgroundColor == nil || *story.Text.BackgroundColor != 0) {
				t.Fatal("explicit zero background lost")
			}
		})
	}
}
