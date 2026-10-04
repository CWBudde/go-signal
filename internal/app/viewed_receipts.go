package app

import (
	"context"
	"fmt"
	"slices"

	"github.com/cwbudde/go-signal/internal/signal"
)

// ViewedReceiptRequest names the original sender and the sent timestamps (milliseconds) of
// messages whose media the user has viewed. Group messages also use the individual sender.
type ViewedReceiptRequest struct {
	Sender     string
	Timestamps []uint64
}

// ViewedReceiptResult describes a submitted viewed receipt. It does not confirm peer rendering.
type ViewedReceiptResult struct {
	Sender     signal.Recipient
	Timestamps []uint64
}

// SendViewedReceipt explicitly submits one viewed receipt for another user's messages.
// It validates before connecting in send-only mode, resolves the sender, honors WithAllowlist,
// and removes duplicate timestamps without changing the request. Incoming events stay queued.
func (a *App) SendViewedReceipt(ctx context.Context, req ViewedReceiptRequest) (ViewedReceiptResult, error) {
	target, timestamps, err := validateViewedReceipt(req)
	if err != nil {
		return ViewedReceiptResult{}, fmt.Errorf("send viewed receipt: %w", err)
	}

	err = a.rejectOwnViewedSender(ctx, target)
	if err != nil {
		return ViewedReceiptResult{}, fmt.Errorf("send viewed receipt: %w", err)
	}

	err = a.connectSendOnly(ctx)
	if err != nil {
		return ViewedReceiptResult{}, fmt.Errorf("send viewed receipt: connect: %w", err)
	}

	targets, err := a.ResolveRecipients(ctx, []string{req.Sender})
	if err != nil {
		return ViewedReceiptResult{}, fmt.Errorf("send viewed receipt: %w", err)
	}

	// A username may resolve to our own ACI after ResolveRecipients' initial self check.
	err = a.rejectOwnViewedSender(ctx, targets[0])
	if err != nil {
		return ViewedReceiptResult{}, fmt.Errorf("send viewed receipt: %w", err)
	}

	err = a.checkAllowed(ctx, targets)
	if err != nil {
		return ViewedReceiptResult{}, fmt.Errorf("send viewed receipt: %w", err)
	}

	err = a.client.SendReceipt(ctx, targets[0].Recipient, signal.ReceiptViewed, timestamps)
	if err != nil {
		return ViewedReceiptResult{}, fmt.Errorf("send viewed receipt: %w", err)
	}

	return ViewedReceiptResult{Sender: targets[0].Recipient, Timestamps: timestamps}, nil
}

func validateViewedReceipt(req ViewedReceiptRequest) (Target, []uint64, error) {
	target, err := ParseRecipient(req.Sender)
	if err != nil {
		return Target{}, nil, err
	}

	if target.IsGroup() || target.Self {
		return Target{}, nil, fmt.Errorf("%w: sender must be another user", ErrInvalidRecipient)
	}

	if len(req.Timestamps) == 0 || slices.Contains(req.Timestamps, 0) {
		return Target{}, nil, fmt.Errorf("%w: want nonzero message timestamps", signal.ErrInvalidReceipt)
	}

	timestamps := make([]uint64, 0, len(req.Timestamps))
	for _, timestamp := range req.Timestamps {
		if !slices.Contains(timestamps, timestamp) {
			timestamps = append(timestamps, timestamp)
		}
	}

	return target, timestamps, nil
}

func (a *App) rejectOwnViewedSender(ctx context.Context, target Target) error {
	own, err := a.client.Account(ctx)
	if err != nil {
		return fmt.Errorf("account: %w", err)
	}

	if isOwn(target, own) {
		return fmt.Errorf("%w: sender must be another user", ErrInvalidRecipient)
	}

	return nil
}
