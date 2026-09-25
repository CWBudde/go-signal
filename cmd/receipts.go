package cmd

import (
	"context"
	"log/slog"
	"time"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/signal"
)

const (
	// receiptDelay is how long receive collects read receipts before sending them, so that a
	// burst of messages (such as the queue drained after connecting) gets one receipt per sender.
	receiptDelay = time.Second
	// receiptFlushTimeout bounds sending the last receipts when receive ends, also when it was
	// interrupted.
	receiptFlushTimeout = 10 * time.Second
)

// receiptSender sends the read receipts of receive --send-read-receipts: it queues one for each
// message after it was printed and sends the queue receiptDelay after the first queued one, and
// when receive ends. A nil *receiptSender does nothing.
type receiptSender struct {
	receipts *app.ReadReceipts
	timer    *time.Timer
	armed    bool
}

// newReceiptSender returns a receiptSender for receipts, or nil when receipts is nil.
func newReceiptSender(receipts *app.ReadReceipts) *receiptSender {
	if receipts == nil {
		return nil
	}

	timer := time.NewTimer(receiptDelay)
	timer.Stop()

	return &receiptSender{receipts: receipts, timer: timer}
}

// due fires when the queued receipts are to be sent; nil (never fires) while none are queued.
func (s *receiptSender) due() <-chan time.Time {
	if s == nil || !s.armed {
		return nil
	}

	return s.timer.C
}

// handled queues a read receipt for evt if it is a message from another user and handling it
// (printing) didn't fail with err.
func (s *receiptSender) handled(evt signal.Event, err error) {
	if s == nil || err != nil || !s.receipts.Add(evt) || s.armed {
		return
	}

	s.timer.Reset(receiptDelay)
	s.armed = true
}

// flush sends the queued receipts. Failures are only logged: receive goes on.
func (s *receiptSender) flush(ctx context.Context) {
	if s == nil {
		return
	}

	s.timer.Stop()
	s.armed = false

	if !s.receipts.Pending() {
		return
	}

	err := s.receipts.Flush(ctx)
	if err != nil {
		slog.Warn("sending read receipts failed", "error", err)
	}
}

// close sends the receipts still queued when receive ends, even if ctx is done.
func (s *receiptSender) close(ctx context.Context) {
	if s == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), receiptFlushTimeout)
	defer cancel()

	s.flush(ctx)
}
