//go:build cgo || libsignal_go

package signal

import (
	"math"
	"slices"
	"strings"

	"github.com/cwbudde/mautrix-signal/pkg/signalmeow/events"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow/protobuf/signalpb"
)

func convertStory(raw *events.Story, ownACI string) Event {
	env := envelope(raw.Info, ownACI)
	env.Timestamp = raw.Timestamp
	msg := raw.Content

	out := &Story{
		Envelope:      env,
		AllowsReplies: msg.GetAllowsReplies(),
		Mentions:      convertMentions(msg.GetBodyRanges()),
	}
	switch {
	case msg.GetFileAttachment() != nil:
		att := convertAttachment(msg.GetFileAttachment())
		out.File = &att
	case msg.GetTextAttachment() != nil:
		out.Text = convertStoryText(msg.GetTextAttachment())
		if out.Text == nil {
			return &Unsupported{Envelope: env, Type: "invalidStory"}
		}
	default:
		return &Unsupported{Envelope: env, Type: "invalidStory"}
	}

	return out
}

func storyOptional[T any](value *T) *T {
	if value == nil {
		return nil
	}

	return new(*value)
}

func convertStoryText(raw *signalpb.TextAttachment) *StoryText {
	out := &StoryText{
		Text:                raw.GetText(),
		Style:               strings.ToLower(raw.GetTextStyle().String()),
		ForegroundColor:     storyOptional(raw.TextForegroundColor),
		TextBackgroundColor: storyOptional(raw.TextBackgroundColor),
	}
	if color, ok := raw.GetBackground().(*signalpb.TextAttachment_Color); ok {
		out.BackgroundColor = new(color.Color)
	}

	if gradient := raw.GetGradient(); gradient != nil {
		// Non-finite floats cannot be stored or printed as JSON. Report malformed content.
		for _, position := range gradient.GetPositions() {
			if math.IsNaN(float64(position)) || math.IsInf(float64(position), 0) {
				return nil
			}
		}

		out.Gradient = &StoryGradient{
			StartColor: storyOptional(gradient.StartColor),
			EndColor:   storyOptional(gradient.EndColor),
			Angle:      storyOptional(gradient.Angle),
			Colors:     slices.Clone(gradient.GetColors()),
			Positions:  slices.Clone(gradient.GetPositions()),
		}
	}

	if preview := raw.GetPreview(); preview != nil {
		out.Preview = &StoryPreview{
			URL:         preview.GetUrl(),
			Title:       preview.GetTitle(),
			Description: preview.GetDescription(),
			Date:        preview.GetDate(),
		}
		if preview.GetImage() != nil {
			image := convertAttachment(preview.GetImage())
			out.Preview.Image = &image
		}
	}

	return out
}
