package signal

import (
	"encoding/base64"
	"errors"
	"strings"

	"github.com/google/uuid"
)

// ErrInvalidStory means that a story has an unsupported audience or content.
var ErrInvalidStory = errors.New("invalid story")

// OutgoingStory sends a plain text card or one uploaded image/video to a named group or private distribution list.
type OutgoingStory struct {
	DistributionListID string
	Text               string
	File               *UploadedAttachment
	AllowsReplies      bool
}

func (req SendRequest) checkStory() error {
	if !req.storyAudience() || !req.storyOnly() {
		return ErrInvalidStory
	}

	return req.Story.check()
}

func (req SendRequest) storyAudience() bool {
	if req.Story.DistributionListID != "" {
		return req.privateStoryAudience()
	}

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

func (req SendRequest) privateStoryAudience() bool {
	id, err := uuid.Parse(req.Story.DistributionListID)
	if err != nil || id.String() != req.Story.DistributionListID || req.GroupID != "" || len(req.Recipients) == 0 {
		return false
	}

	for _, recipient := range req.Recipients {
		if !storyRecipient(recipient) {
			return false
		}
	}

	return true
}

func storyRecipient(recipient Recipient) bool {
	aci, err := uuid.Parse(recipient.ACI)
	return err == nil && aci != uuid.Nil && aci.String() == recipient.ACI && recipient.PNI == ""
}
