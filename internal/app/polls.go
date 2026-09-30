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

// Check validates creation before opening a client.
func (req PollCreateRequest) Check() error {
	err := checkPollGroup(req.GroupID)
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
	err := checkPollGroup(req.GroupID)
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
	// Author resolution is intentionally deferred until after group allowlist checking.
	vote := signal.OutgoingPollVote{
		TargetAuthor:    signal.Recipient{ACI: "00000000-0000-0000-0000-000000000001"},
		TargetTimestamp: timestamp, OptionIndexes: req.OptionIndexes, VoteCount: req.VoteCount,
	}

	err = vote.Check()
	if err != nil {
		return fmt.Errorf("poll vote: %w", err)
	}

	return nil
}

// Check validates closure before opening a client.
func (req PollCloseRequest) Check() error {
	err := checkPollGroup(req.GroupID)
	if err != nil {
		return err
	}

	err = (signal.OutgoingPollClose{TargetTimestamp: req.Target}).Check()
	if err != nil {
		return fmt.Errorf("poll close: %w", err)
	}

	return nil
}

// Check validates offline canonical references and the bounded snapshot size.
func (req PollShowRequest) Check() error {
	err := checkPollGroup(req.GroupID)
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

// PollCreate sends one group poll through the usual send policy.
func (a *App) PollCreate(ctx context.Context, req PollCreateRequest) (PollSendResult, error) {
	err := req.Check()
	if err != nil {
		return PollSendResult{}, fmt.Errorf("poll create: %w", err)
	}

	poll := &signal.Poll{Question: req.Question, Options: slices.Clone(req.Options), AllowMultiple: !req.SingleChoice}
	out := PollSendResult{Operation: "create", Poll: poll}

	out.SendResult,
		err = a.sendContent(ctx,
		"poll create",
		[]string{GroupPrefix + req.GroupID},
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

// PollVote sends the caller's explicit vote counter and selection.
func (a *App) PollVote(ctx context.Context, req PollVoteRequest) (PollSendResult, error) {
	err := req.Check()
	if err != nil {
		return PollSendResult{}, fmt.Errorf("poll vote: %w", err)
	}

	author, timestamp, _ := ParseTarget(req.Target)
	out := PollSendResult{
		Operation:       "vote",
		TargetTimestamp: timestamp,
		OptionIndexes:   slices.Clone(req.OptionIndexes),
		VoteCount:       req.VoteCount,
	}

	out.SendResult,
		err = a.sendContent(ctx,
		"poll vote",
		[]string{GroupPrefix + req.GroupID},
		func(ctx context.Context) (content,
			error,
		) {
			authors := []Target{author}

			err := a.resolveUsers(ctx, authors)
			if err != nil {
				return content{}, fmt.Errorf("resolve target author: %w", err)
			}

			out.TargetAuthor = authors[0].Recipient

			return content{pollVote: &signal.OutgoingPollVote{
					TargetAuthor:    out.TargetAuthor,
					TargetTimestamp: timestamp,
					OptionIndexes:   slices.Clone(req.OptionIndexes),
					VoteCount:       req.VoteCount,
				}},
				nil
		})

	return out, err
}

// PollClose closes this account's poll in one group.
func (a *App) PollClose(ctx context.Context, req PollCloseRequest) (PollSendResult, error) {
	err := req.Check()
	if err != nil {
		return PollSendResult{}, fmt.Errorf("poll close: %w", err)
	}

	out := PollSendResult{Operation: "close", TargetTimestamp: req.Target}

	out.SendResult,
		err = a.sendContent(ctx,
		"poll close",
		[]string{GroupPrefix + req.GroupID},
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
