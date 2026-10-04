package app

import (
	"context"

	"github.com/cwbudde/go-signal/internal/signal"
)

// StoryMediaResult records independent file and link-preview image downloads.
type StoryMediaResult struct {
	File, Preview *SavedAttachment
}

// SaveStoryMedia uses the verified download and collision-safe file writer for story media.
func (a *App) SaveStoryMedia(ctx context.Context, dir string, story *signal.Story) StoryMediaResult {
	var out StoryMediaResult

	save := func(att *signal.Attachment) *SavedAttachment {
		results := a.SaveAttachments(ctx, SaveAttachmentsRequest{
			Dir:     dir,
			Message: &signal.Message{Envelope: story.Envelope, Attachments: []signal.Attachment{*att}},
		})

		return &results[0]
	}
	if story.File != nil {
		out.File = save(story.File)
	}

	if story.Text != nil && story.Text.Preview != nil && story.Text.Preview.Image != nil {
		out.Preview = save(story.Text.Preview.Image)
	}

	return out
}
