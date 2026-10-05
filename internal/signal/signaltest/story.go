package signaltest

import (
	"slices"

	"github.com/cwbudde/go-signal/internal/signal"
)

func cloneStoryRequest(req signal.SendRequest) signal.SendRequest {
	if req.Story == nil {
		return req
	}

	req.Recipients = slices.Clone(req.Recipients)

	story := *req.Story
	if story.File != nil {
		file := *story.File
		story.File = &file
	}

	req.Story = &story

	return req
}

func (c *client) checkStory(req signal.SendRequest) error {
	if req.GroupID == "" {
		for _, recipient := range req.Recipients {
			if recipient.ACI == c.connected {
				return signal.ErrInvalidStory
			}
		}

		if req.Story.File != nil && !slices.Contains(c.uploads, req.Story.File.ID) {
			return signal.ErrUnknownAttachment
		}

		return nil
	}

	group, err := c.fetch(req.GroupID)
	if err != nil {
		return err
	}

	membership, _ := group.MembershipOf(c.connected)
	if membership != signal.MembershipMember {
		return signal.ErrNotAMember
	}

	if req.Story.File != nil && !slices.Contains(c.uploads, req.Story.File.ID) {
		return signal.ErrUnknownAttachment
	}

	return nil
}
