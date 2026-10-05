package app

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/google/uuid"
)

// StorySendRequest names one group or private audience and either a plain text card or one media file.
type StorySendRequest struct {
	GroupID            string
	DistributionListID string
	MyStory            bool
	Text               string
	Attachment         string
	NoReplies          bool
	AttachDir          string
}

// StorySendResult separates story delivery from ordinary message output.
type StorySendResult struct {
	SendResult

	AllowsReplies      bool
	DistributionListID string
	StorageVersion     uint64
	SyncErr            error
}

// Check validates the audience and exclusive content before opening a client.
func (req StorySendRequest) Check() error {
	if (req.Attachment == "" && strings.TrimSpace(req.Text) == "") || (req.Attachment != "" && req.Text != "") {
		return signal.ErrInvalidStory
	}

	return req.checkAudience()
}

func (req StorySendRequest) checkAudience() error {
	audiences := 0

	if req.GroupID != "" {
		id, ok := decodeGroupKey(req.GroupID)
		if !ok || id != req.GroupID {
			return signal.ErrInvalidStory
		}

		audiences++
	}

	if req.DistributionListID != "" {
		id, err := uuid.Parse(req.DistributionListID)
		if err != nil || id.String() != req.DistributionListID {
			return signal.ErrInvalidStory
		}

		audiences++
	}

	if req.MyStory {
		audiences++
	}

	if audiences != 1 {
		return signal.ErrInvalidStory
	}

	return nil
}

// StorySend validates and reads media before connecting, then checks full membership and
// the allowlist before uploading. Incoming events remain unacknowledged in send-only mode.
func (a *App) StorySend(ctx context.Context, req StorySendRequest) (StorySendResult, error) {
	err := req.Check()
	if err != nil {
		return StorySendResult{}, fmt.Errorf("story send: %w", err)
	}

	files, err := storyFiles(req)
	if err != nil {
		return StorySendResult{}, fmt.Errorf("story send: %w", err)
	}

	err = a.connectSendOnly(ctx)
	if err != nil {
		return StorySendResult{}, fmt.Errorf("story send: connect: %w", err)
	}

	if req.GroupID == "" {
		return a.privateStorySend(ctx, req, files)
	}

	target := Target{GroupID: req.GroupID}

	err = a.checkAllowed(ctx, []Target{target})
	if err != nil {
		return StorySendResult{}, fmt.Errorf("story send: %w", err)
	}

	err = a.checkStoryMember(ctx, req.GroupID)
	if err != nil {
		return StorySendResult{}, fmt.Errorf("story send: %w", err)
	}

	story := &signal.OutgoingStory{Text: req.Text, AllowsReplies: !req.NoReplies}

	err = a.uploadStory(ctx, story, files)
	if err != nil {
		return StorySendResult{}, err
	}

	res := StorySendResult{
		SendResult:    SendResult{Timestamp: a.nextTimestamp()},
		AllowsReplies: story.AllowsReplies,
	}
	sent, sendErr := a.client.Send(ctx, signal.SendRequest{GroupID: req.GroupID, Timestamp: res.Timestamp, Story: story})

	result := TargetResult{Target: target, Members: sent.Results, Err: sendErr}
	if sendErr == nil {
		result.Err = sent.SyncErr
	}

	res.Results = []TargetResult{result}

	return res, res.err("story send")
}

func (a *App) checkStoryMember(ctx context.Context, groupID string) error {
	account, err := a.client.Account(ctx)
	if err != nil {
		return fmt.Errorf("account: %w", err)
	}

	group, err := a.client.Group(ctx, groupID)
	if err != nil {
		return fmt.Errorf("group: %w", err)
	}

	membership, _ := group.MembershipOf(account.ACI)
	if membership != signal.MembershipMember {
		return signal.ErrNotAMember
	}

	return nil
}

func storyFiles(req StorySendRequest) ([]signal.OutgoingAttachment, error) {
	if req.Attachment == "" {
		return nil, nil
	}

	files, err := loadAttachments(req.AttachDir, []string{req.Attachment})
	if err != nil {
		return nil, err
	}

	// Sniff the bytes instead of trusting an image/video filename extension.
	files[0].ContentType = http.DetectContentType(files[0].Data)
	if !signal.StoryMediaType(files[0].ContentType) {
		return nil, fmt.Errorf("%w: stories need image or video bytes", signal.ErrInvalidStory)
	}

	return files, nil
}
