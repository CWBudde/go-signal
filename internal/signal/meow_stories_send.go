//go:build cgo || libsignal_go

package signal

import (
	"context"
	"errors"
	"fmt"

	"github.com/cwbudde/mautrix-signal/pkg/signalmeow"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow/protobuf/signalpb"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow/types"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
)

const (
	storyTextWhite       uint32 = 0xffffffff
	storyBackgroundBlack uint32 = 0xff000000
)

func (c *meowClient) sendStory(ctx context.Context, req SendRequest) (SendResult, error) {
	err := req.Check()
	if err != nil {
		return SendResult{}, err
	}

	var pointer *signalpb.AttachmentPointer

	if req.Story.File != nil {
		pointers, err := c.uploadedPointers([]UploadedAttachment{*req.Story.File})
		if err != nil {
			return SendResult{}, err
		}

		pointer = pointers[0]
	}

	story, err := outgoingStoryMessage(req.Story, pointer)
	if err != nil {
		return SendResult{}, err
	}

	err = c.connectionLost()
	if err != nil {
		return SendResult{}, fmt.Errorf("story send: %w", err)
	}

	c.cliMu.Lock()
	cli := c.cli
	c.cliMu.Unlock()

	sent, err := submitStory(c.zlog.WithContext(ctx), cli, req, story)
	if errors.Is(err, signalmeow.ErrStoryNotMember) {
		return SendResult{}, ErrNotAMember
	}

	if errors.Is(err, signalmeow.ErrGroupMasterKeyNotFound) {
		return SendResult{}, ErrUnknownGroup
	}

	if err != nil {
		return SendResult{}, fmt.Errorf("story send: %w", err)
	}

	res := SendResult{Timestamp: req.Timestamp, Results: groupResults(&sent.GroupMessageSendResult)}
	if sent.SyncError != nil {
		res.SyncErr = fmt.Errorf("story transcript failed after peer submission: %w", sent.SyncError)
	}

	return res, nil
}

func outgoingStoryMessage(story *OutgoingStory, pointer *signalpb.AttachmentPointer) (*signalpb.StoryMessage, error) {
	msg := &signalpb.StoryMessage{AllowsReplies: new(story.AllowsReplies)}
	if story.File != nil {
		if pointer == nil {
			return nil, ErrUnknownAttachment
		}

		owned, _ := proto.Clone(pointer).(*signalpb.AttachmentPointer)
		msg.Attachment = &signalpb.StoryMessage_FileAttachment{FileAttachment: owned}

		return msg, nil
	}

	msg.Attachment = &signalpb.StoryMessage_TextAttachment{TextAttachment: &signalpb.TextAttachment{
		Text: new(story.Text), TextStyle: signalpb.TextAttachment_DEFAULT.Enum(),
		TextForegroundColor: new(storyTextWhite),
		Background:          &signalpb.TextAttachment_Color{Color: storyBackgroundBlack},
	}}

	return msg, nil
}

func submitStory(
	ctx context.Context, cli *signalmeow.Client, req SendRequest, story *signalpb.StoryMessage,
) (*signalmeow.GroupStorySendResult, error) {
	if req.GroupID != "" {
		//nolint:wrapcheck // sendStory maps and wraps fork errors.
		return cli.SendGroupStory(ctx, types.GroupIdentifier(req.GroupID), story, req.Timestamp)
	}

	distribution, err := uuid.Parse(req.Story.DistributionListID)
	if err != nil {
		return nil, ErrInvalidStory
	}

	recipients := make([]uuid.UUID, len(req.Recipients))
	for i, recipient := range req.Recipients {
		aci, err := uuid.Parse(recipient.ACI)
		if err != nil || aci == cli.Store.ACI {
			return nil, ErrInvalidStory
		}

		recipients[i] = aci
	}

	//nolint:wrapcheck // sendStory maps and wraps fork errors.
	return cli.SendPrivateStory(ctx, distribution, recipients, story, req.Timestamp)
}
