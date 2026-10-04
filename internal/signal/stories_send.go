package signal

import (
	"encoding/base64"
	"errors"
	"strings"
)

// ErrInvalidStory means that a story has an unsupported audience or content.
var ErrInvalidStory = errors.New("invalid story")

// OutgoingStory sends a plain text card or one uploaded image/video to a named group.
type OutgoingStory struct {
	Text          string
	File          *UploadedAttachment
	AllowsReplies bool
}

func (req SendRequest) checkStory() error {
	if !req.storyAudience() || !req.storyOnly() {
		return ErrInvalidStory
	}

	return req.Story.check()
}

func (req SendRequest) storyAudience() bool {
	id, err := base64.StdEncoding.DecodeString(req.GroupID)
	return err == nil && len(id) == 32 && base64.StdEncoding.EncodeToString(id) == req.GroupID && len(req.Recipients) == 0
}

func (req SendRequest) storyOnly() bool {
	return !req.hasContent() && req.Sticker == nil && !req.hasPoll() && !req.hasPin() &&
		req.Reaction == nil && req.DeleteTarget == 0 && req.EditTarget == 0
}

func (story *OutgoingStory) check() error {
	if story.File == nil {
		if strings.TrimSpace(story.Text) == "" {
			return ErrInvalidStory
		}

		return nil
	}

	file := story.File
	if story.Text != "" || file.ID == "" || !StoryMediaType(file.ContentType) {
		return ErrInvalidStory
	}

	return nil
}

// StoryMediaType reports whether the MIME type is suitable for a story attachment.
func StoryMediaType(contentType string) bool {
	return strings.HasPrefix(contentType, "image/") || strings.HasPrefix(contentType, "video/")
}
