package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/cwbudde/go-signal/internal/signal"
)

// DefaultSyncTimeout bounds the sync after `link` and `account sync` unless set otherwise.
const DefaultSyncTimeout = 60 * time.Second

// SyncRequest is the input of Sync.
type SyncRequest struct {
	// Timeout bounds the sync; zero or less waits until it is complete (or ctx ends).
	Timeout time.Duration
	// Progress, if set, is called at the start of every stage (see signal.SyncStage).
	Progress func(signal.SyncStage)
}

// SyncResult is the output of Sync.
type SyncResult struct {
	signal.SyncResult

	// Incomplete says why the sync didn't finish (it wraps signal.ErrSyncIncomplete, e.g. with
	// context.DeadlineExceeded); nil when it is complete. What did arrive is stored, and the
	// rest comes with later syncs and received messages.
	Incomplete error
}

// Sync fetches the contacts and groups of the selected account from the phone and the storage
// service into the store (signal.Client.Sync). It connects in send-only mode, so incoming
// messages stay on the server for the next receive; the client must not be connected yet. An
// incomplete sync (timeout, storage service failure) is not an error: it is reported in
// SyncResult.Incomplete. Errors mean that nothing was synced, e.g. the device was unlinked.
func (a *App) Sync(ctx context.Context, req SyncRequest) (SyncResult, error) {
	// Connect only bounds the setup with ctx; the connection lives until Close.
	err := a.client.Connect(ctx, signal.SendOnly())
	if err != nil {
		return SyncResult{}, fmt.Errorf("sync: connect: %w", err)
	}

	if req.Timeout > 0 {
		var cancel context.CancelFunc

		ctx, cancel = context.WithTimeout(ctx, req.Timeout)
		defer cancel()
	}

	res, err := a.client.Sync(ctx, signal.SyncOptions{Progress: req.Progress})
	if errors.Is(err, signal.ErrSyncIncomplete) {
		return SyncResult{SyncResult: res, Incomplete: err}, nil
	}

	if err != nil {
		return SyncResult{}, fmt.Errorf("sync: %w", err)
	}

	return SyncResult{SyncResult: res}, nil
}
