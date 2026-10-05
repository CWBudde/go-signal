package app

import (
	"context"
	"fmt"
	"slices"

	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/google/uuid"
)

// DefaultPollScanLimit and MaxPollScanLimit bound local poll history inspection.
const (
	DefaultPollScanLimit = 1000
	MaxPollScanLimit     = 10000
)

func checkPollGroup(id string) error {
	canonical, ok := decodeGroupKey(id)
	if !ok || canonical != id {
		return fmt.Errorf("%w: need a canonical group ID", ErrInvalidRecipient)
	}

	return nil
}

// pollRecipient validates exactly one destination without resolving it.
func pollRecipient(groupID, recipient string) (string, error) {
	if (groupID == "") == (recipient == "") {
		return "", fmt.Errorf("%w: supply one group or direct recipient", signal.ErrInvalidPoll)
	}

	if groupID != "" {
		err := checkPollGroup(groupID)
		if err != nil {
			return "", err
		}

		return GroupPrefix + groupID, nil
	}

	target, err := ParseRecipient(recipient)
	if err != nil {
		return "", err
	}

	if target.IsGroup() || target.Recipient.ACI == uuid.Nil.String() {
		return "", fmt.Errorf("%w: need a direct recipient", ErrInvalidRecipient)
	}

	return recipient, nil
}

// Check validates creation before opening a client.
func (req PollCreateRequest) Check() error {
	_, err := pollRecipient(req.GroupID, req.Recipient)
	if err != nil {
		return err
	}

	err = (signal.Poll{Question: req.Question, Options: req.Options, AllowMultiple: !req.SingleChoice}).Check()
	if err != nil {
		return fmt.Errorf("poll: %w", err)
	}

	return nil
}

// Check validates a vote before opening a client.
func (req PollVoteRequest) Check() error {
	_, err := pollRecipient(req.GroupID, req.Recipient)
	if err != nil {
		return err
	}

	author, timestamp, err := ParseTarget(req.Target)
	if err != nil {
		return err
	}

	if author.Recipient.ACI == uuid.Nil.String() {
		return fmt.Errorf("%w: target author must be a non-nil ACI", signal.ErrInvalidPoll)
	}

	if req.Clear == (len(req.OptionIndexes) > 0) {
		return fmt.Errorf("%w: select options or explicitly clear", signal.ErrInvalidPoll)
	}
	// Author resolution is intentionally deferred until after destination allowlist checking.
	vote := signal.OutgoingPollVote{
		TargetAuthor:    signal.Recipient{ACI: "00000000-0000-0000-0000-000000000001"},
		TargetTimestamp: timestamp, OptionIndexes: req.OptionIndexes, VoteCount: max(req.VoteCount, 1),
	}

	err = vote.Check()
	if err != nil {
		return fmt.Errorf("poll vote: %w", err)
	}

	return nil
}

// Check validates closure before opening a client.
func (req PollCloseRequest) Check() error {
	_, err := pollRecipient(req.GroupID, req.Recipient)
	if err != nil {
		return err
	}

	err = (signal.OutgoingPollClose{TargetTimestamp: req.Target}).Check()
	if err != nil {
		return fmt.Errorf("poll close: %w", err)
	}

	return nil
}

func (req PollShowRequest) checkChat() error {
	_, err := pollRecipient(req.GroupID, req.Recipient)
	if err != nil {
		return err
	}

	if req.Recipient != "" {
		chat, err := ParseRecipient(req.Recipient)
		if err != nil || chat.Recipient.ACI == "" || chat.Recipient.ACI != req.Recipient {
			return fmt.Errorf("%w: show requires a canonical chat ACI", ErrInvalidRecipient)
		}
	}

	return nil
}

// Check validates offline canonical references and the bounded snapshot size.
func (req PollShowRequest) Check() error {
	err := req.checkChat()
	if err != nil {
		return err
	}

	author, timestamp, err := ParseTarget(req.Target)
	if err != nil {
		return err
	}

	if author.Self || author.Recipient.ACI == "" || author.Recipient.ACI == uuid.Nil.String() ||
		req.Target != fmt.Sprintf("%s:%d", author.Recipient.ACI, timestamp) {
		return fmt.Errorf("%w: show requires canonical ACI:timestamp", ErrInvalidTarget)
	}

	if req.ScanLimit < 0 || req.ScanLimit > MaxPollScanLimit {
		return fmt.Errorf("%w: scan limit must be 0 through %d", signal.ErrInvalidPoll, MaxPollScanLimit)
	}

	return nil
}

// PollCreate sends one poll through the usual send policy.
func (a *App) PollCreate(ctx context.Context, req PollCreateRequest) (PollSendResult, error) {
	err := req.Check()
	if err != nil {
		return PollSendResult{}, fmt.Errorf("poll create: %w", err)
	}

	destination, _ := pollRecipient(req.GroupID, req.Recipient)

	poll := &signal.Poll{Question: req.Question, Options: slices.Clone(req.Options), AllowMultiple: !req.SingleChoice}
	out := PollSendResult{Operation: "create", Poll: poll}

	out.SendResult,
		err = a.sendContent(ctx,
		"poll create",
		[]string{destination},
		func(ctx context.Context) (content,
			error,
		) {
			acc, err := a.client.Account(ctx)
			if err != nil {
				return content{}, fmt.Errorf("own account: %w", err)
			}

			out.TargetAuthor = signal.Recipient{ACI: acc.ACI}

			return content{pollCreate: poll}, nil
		})
	out.TargetTimestamp = out.Timestamp

	return out, err
}

// PollVote reserves a durable counter for selections or withdrawal, with an optional explicit override.
func (a *App) PollVote(ctx context.Context, req PollVoteRequest) (PollSendResult, error) {
	err := req.Check()
	if err != nil {
		return PollSendResult{}, fmt.Errorf("poll vote: %w", err)
	}

	destination, _ := pollRecipient(req.GroupID, req.Recipient)

	author, timestamp, _ := ParseTarget(req.Target)
	out := PollSendResult{
		Operation:       "vote",
		TargetTimestamp: timestamp,
		OptionIndexes:   slices.Clone(req.OptionIndexes),
		VoteCount:       req.VoteCount,
	}

	out.SendResult,
		err = a.sendPreparedContent(ctx,
		"poll vote",
		[]string{destination},
		func(ctx context.Context, targets []Target) (content,
			error,
		) {
			authors := []Target{author}

			err := a.resolveUsers(ctx, authors)
			if err != nil {
				return content{}, fmt.Errorf("resolve target author: %w", err)
			}

			out.TargetAuthor = authors[0].Recipient
			chat := signal.Chat{GroupID: targets[0].GroupID, Recipient: targets[0].Recipient}

			out.VoteCount, err = a.client.ReservePollVote(ctx, signal.PollVoteCounterRequest{
				Chat: chat, Author: out.TargetAuthor, Timestamp: timestamp, Explicit: req.VoteCount,
			})
			if err != nil {
				return content{}, fmt.Errorf("reserve counter: %w", err)
			}

			return content{pollVote: &signal.OutgoingPollVote{
					TargetAuthor:    out.TargetAuthor,
					TargetTimestamp: timestamp,
					OptionIndexes:   slices.Clone(req.OptionIndexes),
					VoteCount:       out.VoteCount,
				}},
				nil
		})

	return out, err
}

// PollClose closes this account's poll in one chat.
func (a *App) PollClose(ctx context.Context, req PollCloseRequest) (PollSendResult, error) {
	err := req.Check()
	if err != nil {
		return PollSendResult{}, fmt.Errorf("poll close: %w", err)
	}

	destination, _ := pollRecipient(req.GroupID, req.Recipient)

	out := PollSendResult{Operation: "close", TargetTimestamp: req.Target}

	out.SendResult,
		err = a.sendContent(ctx,
		"poll close",
		[]string{destination},
		func(ctx context.Context) (content,
			error,
		) {
			acc, err := a.client.Account(ctx)
			if err != nil {
				return content{}, fmt.Errorf("own account: %w", err)
			}

			out.TargetAuthor = signal.Recipient{ACI: acc.ACI}

			return content{pollClose: &signal.OutgoingPollClose{TargetTimestamp: req.Target}}, nil
		})

	return out, err
}
