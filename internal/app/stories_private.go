package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/cwbudde/go-signal/internal/signal"
)

// StoryAudiences fetches the selected account's fresh phone-defined audience snapshot.
func (a *App) StoryAudiences(ctx context.Context) (signal.StoryAudiences, error) {
	err := a.connectSendOnly(ctx)
	if err != nil {
		return signal.StoryAudiences{}, fmt.Errorf("story audiences: connect: %w", err)
	}

	snapshot, err := a.client.StoryAudiences(ctx)
	if err != nil {
		return signal.StoryAudiences{}, fmt.Errorf("story audiences: %w", err)
	}

	return snapshot, nil
}

func (a *App) privateStorySend(
	ctx context.Context, req StorySendRequest, files []signal.OutgoingAttachment,
) (StorySendResult, error) {
	snapshot, err := a.client.StoryAudiences(ctx)
	if err != nil {
		return StorySendResult{}, fmt.Errorf("story send: audience: %w", err)
	}

	audienceID := req.DistributionListID
	if req.MyStory {
		audienceID = signal.MyStoryID
	}

	audience, err := findStoryAudience(snapshot, audienceID)
	if err != nil {
		return StorySendResult{}, err
	}

	targets := make([]Target, len(audience.Recipients))
	for i, recipient := range audience.Recipients {
		targets[i] = Target{Recipient: recipient}
	}

	err = a.checkAllowed(ctx, targets)
	if err != nil {
		return StorySendResult{}, fmt.Errorf("story send: %w", err)
	}

	story := &signal.OutgoingStory{
		DistributionListID: audienceID,
		Text:               req.Text,
		AllowsReplies:      audience.AllowsReplies && !req.NoReplies,
	}

	err = a.uploadStory(ctx, story, files)
	if err != nil {
		return StorySendResult{}, err
	}

	res := StorySendResult{
		SendResult:         SendResult{Timestamp: a.nextTimestamp()},
		AllowsReplies:      story.AllowsReplies,
		DistributionListID: audienceID,
		StorageVersion:     snapshot.StorageVersion,
	}

	sent, err := a.client.Send(ctx, signal.SendRequest{
		Recipients: audience.Recipients, Timestamp: res.Timestamp, Story: story,
	})
	if err != nil {
		return StorySendResult{}, fmt.Errorf("story send: %w", err)
	}

	return privateStoryResult(res, targets, sent)
}

func privateStoryResult(res StorySendResult, targets []Target, sent signal.SendResult) (StorySendResult, error) {
	peers := make(map[string]signal.RecipientResult, len(sent.Results))
	for _, peer := range sent.Results {
		peers[peer.Recipient.ACI] = peer
	}

	for _, target := range targets {
		peer, ok := peers[target.Recipient.ACI]

		result := TargetResult{Target: target, Unidentified: peer.Unidentified, Err: peer.Err}
		if !ok {
			result.Err = errNoResult
		}

		res.Results = append(res.Results, result)
	}

	res.SyncErr = sent.SyncErr

	peerErr := res.err("story send")
	if res.SyncErr != nil {
		return res, errors.Join(peerErr, fmt.Errorf("%w: %w", ErrSendFailed, res.SyncErr))
	}

	return res, peerErr
}

func findStoryAudience(snapshot signal.StoryAudiences, audienceID string) (signal.StoryAudience, error) {
	for _, audience := range snapshot.Audiences {
		if audience.ID == audienceID {
			if len(audience.Recipients) == 0 {
				return signal.StoryAudience{}, fmt.Errorf("story send: empty audience: %w", signal.ErrStoryAudienceUnavailable)
			}

			return audience, nil
		}
	}

	return signal.StoryAudience{}, fmt.Errorf(
		"story send: unknown distribution list: %w", signal.ErrStoryAudienceUnavailable,
	)
}

func (a *App) uploadStory(ctx context.Context, story *signal.OutgoingStory, files []signal.OutgoingAttachment) error {
	if len(files) == 0 {
		return nil
	}

	uploaded, err := a.client.Upload(ctx, files)
	if err != nil {
		return fmt.Errorf("story send: upload: %w", err)
	}

	if len(uploaded) != 1 {
		return fmt.Errorf("story send: %w", signal.ErrUnknownAttachment)
	}

	story.File = &uploaded[0]

	return nil
}
