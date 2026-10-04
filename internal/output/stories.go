package output

import (
	"strings"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/signal"
)

type storyDoc struct {
	eventHead
	envelopeJSON

	AllowsReplies bool            `json:"allowsReplies"`
	File          *attachmentJSON `json:"file,omitempty"`
	Text          *storyTextJSON  `json:"text,omitempty"`
	Mentions      []mentionJSON   `json:"mentions,omitempty"`
}

type storyTextJSON struct {
	Body                string             `json:"body,omitempty"`
	Style               string             `json:"style,omitempty"`
	ForegroundColor     *uint32            `json:"foregroundColor,omitempty"`
	TextBackgroundColor *uint32            `json:"textBackgroundColor,omitempty"`
	BackgroundColor     *uint32            `json:"backgroundColor,omitempty"`
	Gradient            *storyGradientJSON `json:"gradient,omitempty"`
	Preview             *storyPreviewJSON  `json:"preview,omitempty"`
}
type storyGradientJSON struct {
	StartColor *uint32   `json:"startColor,omitempty"`
	EndColor   *uint32   `json:"endColor,omitempty"`
	Angle      *uint32   `json:"angle,omitempty"`
	Colors     []uint32  `json:"colors,omitempty"`
	Positions  []float32 `json:"positions,omitempty"`
}
type storyPreviewJSON struct {
	URL         string          `json:"url"`
	Title       string          `json:"title,omitempty"`
	Description string          `json:"description,omitempty"`
	Date        uint64          `json:"date,omitempty"`
	Image       *attachmentJSON `json:"image,omitempty"`
}

func storyAttachmentJSON(att *signal.Attachment, saved *app.SavedAttachment) *attachmentJSON {
	if att == nil {
		return nil
	}

	out := &attachmentJSON{
		ContentType: att.ContentType,
		Filename:    att.Filename,
		Size:        att.Size,
		Caption:     att.Caption,
	}
	if saved != nil {
		out.Path = saved.Path
		out.DownloadError = errorText(saved.Err)
	}

	return out
}

func (p *Printer) storyDocOf(story *signal.Story, saved app.StoryMediaResult) storyDoc {
	out := storyDoc{
		eventHead:     head("story"),
		envelopeJSON:  p.envelope(story.Envelope),
		AllowsReplies: story.AllowsReplies,
		File:          storyAttachmentJSON(story.File, saved.File),
		Mentions:      p.mentionsJSON(story.Mentions),
	}
	if text := story.Text; text != nil {
		out.Text = &storyTextJSON{
			Body:                text.Text,
			Style:               text.Style,
			ForegroundColor:     text.ForegroundColor,
			TextBackgroundColor: text.TextBackgroundColor,
			BackgroundColor:     text.BackgroundColor,
		}
		if gradient := text.Gradient; gradient != nil {
			out.Text.Gradient = &storyGradientJSON{
				StartColor: gradient.StartColor,
				EndColor:   gradient.EndColor,
				Angle:      gradient.Angle,
				Colors:     gradient.Colors,
				Positions:  gradient.Positions,
			}
		}

		if preview := text.Preview; preview != nil {
			out.Text.Preview = &storyPreviewJSON{
				URL:         preview.URL,
				Title:       preview.Title,
				Description: preview.Description,
				Date:        preview.Date,
				Image:       storyAttachmentJSON(preview.Image, saved.Preview),
			}
		}
	}

	return out
}

func (p *Printer) storyText(story *signal.Story, saved app.StoryMediaResult) string {
	parts := []string{"[story; replies disabled]"}
	if story.AllowsReplies {
		parts[0] = "[story; replies allowed]"
	}

	if story.File != nil {
		parts = append(parts, attachmentText(*story.File, false, saved.File))
		if story.File.Caption != "" {
			parts = append(parts, oneLine(p.mentionText(story.File.Caption, story.Mentions)))
		}
	}

	if text := story.Text; text != nil {
		parts = append(parts, p.storyCardText(text, story.Mentions, saved.Preview)...)
	}

	return strings.Join(parts, " ")
}

// SavedStory prints a story with its media download outcomes.
func (p *Printer) SavedStory(story *signal.Story, saved app.StoryMediaResult) error {
	if p.format == JSON {
		return p.writeJSON(p.storyDocOf(story, saved))
	}

	return p.writeLine(p.envelopeLine(story.Envelope, p.storyText(story, saved)))
}

func (p *Printer) storyCardText(
	text *signal.StoryText, mentions []signal.Mention, saved *app.SavedAttachment,
) []string {
	var parts []string
	if text.Text != "" {
		parts = append(parts, oneLine(p.mentionText(text.Text, mentions)))
	}

	preview := text.Preview
	if preview == nil {
		return parts
	}

	parts = append(parts, "[link "+oneLine(preview.URL)+"]")
	if preview.Title != "" {
		parts = append(parts, oneLine(preview.Title))
	}

	if preview.Image != nil {
		parts = append(parts, attachmentText(*preview.Image, false, saved))
	}

	return parts
}
