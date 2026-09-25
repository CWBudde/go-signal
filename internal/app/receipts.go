package app

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/cwbudde/go-signal/internal/signal"
)

// ReadReceipts collects read receipts for incoming messages and sends them in batches, one
// receipt per sender with all of its message timestamps (as signal-cli does). It is for a single
// receive loop and not safe for concurrent use.
type ReadReceipts struct {
	client  signal.Client
	pending []pendingReceipt // in the order of each sender's first message
}

type pendingReceipt struct {
	sender     signal.Recipient
	timestamps []uint64
}

// ReadReceipts returns an empty batch of read receipts to send on the App's client, which must
// be connected (not in send-only mode) when Flush is called.
func (a *App) ReadReceipts() *ReadReceipts {
	return &ReadReceipts{client: a.client}
}

// Add queues a read receipt for evt if it is a message from another user: a *signal.Message that
// is not a sync transcript of our own. It reports whether it queued one. Other events (edits,
// reactions, deletes, typing, …) get no read receipt, as in the official clients.
func (r *ReadReceipts) Add(evt signal.Event) bool {
	msg, ok := evt.(*signal.Message)
	if !ok || msg.Sync || msg.Sender.ACI == "" || msg.Timestamp == 0 {
		return false
	}

	i := slices.IndexFunc(r.pending, func(p pendingReceipt) bool { return p.sender.ACI == msg.Sender.ACI })
	if i < 0 {
		r.pending = append(r.pending, pendingReceipt{sender: msg.Sender})
		i = len(r.pending) - 1
	}

	if !slices.Contains(r.pending[i].timestamps, msg.Timestamp) {
		r.pending[i].timestamps = append(r.pending[i].timestamps, msg.Timestamp)
	}

	return true
}

// Pending reports whether receipts wait for Flush.
func (r *ReadReceipts) Pending() bool {
	return len(r.pending) > 0
}

// Flush sends the queued receipts, one per sender, and empties the queue. A failed receipt is
// not retried and doesn't stop the others; the errors are joined.
func (r *ReadReceipts) Flush(ctx context.Context) error {
	pending := r.pending
	r.pending = nil

	var errs []error

	for _, p := range pending {
		err := r.client.SendReceipt(ctx, p.sender, signal.ReceiptRead, p.timestamps)
		if err != nil {
			errs = append(errs, fmt.Errorf("read receipt to %s: %w", p.sender, err))
		}
	}

	return errors.Join(errs...)
}
