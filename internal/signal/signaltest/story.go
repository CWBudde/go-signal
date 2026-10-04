package signaltest

import (
	"slices"

	"github.com/cwbudde/go-signal/internal/signal"
)

func cloneStoryRequest(req signal.SendRequest) signal.SendRequest {
	if req.Story == nil {
		return req
	}

	story := *req.Story
	if story.File != nil {
		file := *story.File
		story.File = &file
	}

	req.Story = &story

	return req
}

func (c *client) checkStory(req signal.SendRequest) error {
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
