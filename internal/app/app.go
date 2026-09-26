// Package app holds the use cases behind the CLI commands (and later the MCP tools): each takes
// a typed request and returns a typed result, without printing or Cobra. cmd/ only parses flags,
// calls App and renders the result with internal/output.
package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/cwbudde/go-signal/internal/signal"
)

// App runs the use cases against one open signal.Client, which stays owned by the caller.
type App struct {
	client signal.Client
	now    func() time.Time
}

// Option customises an App.
type Option func(*App)

// WithClock replaces time.Now, e.g. for fixed message timestamps in tests.
func WithClock(now func() time.Time) Option {
	return func(a *App) {
		a.now = now
	}
}

// New returns an App on client.
func New(client signal.Client, opts ...Option) *App {
	a := &App{client: client, now: time.Now}
	for _, opt := range opts {
		opt(a)
	}

	return a
}

// AccountShow returns the selected account from the data dir, without contacting the server.
func (a *App) AccountShow(ctx context.Context) (signal.Account, error) {
	acc, err := a.client.Account(ctx)
	if err != nil {
		return signal.Account{}, fmt.Errorf("account show: %w", err)
	}

	return acc, nil
}

// DevicesList returns all devices of the selected account as the server knows them.
func (a *App) DevicesList(ctx context.Context) ([]signal.Device, error) {
	devices, err := a.client.Devices(ctx)
	if err != nil {
		return nil, fmt.Errorf("devices list: %w", err)
	}

	return devices, nil
}

// UnlinkRequest is the input of AccountUnlink.
type UnlinkRequest struct {
	// LocalOnly skips the server and only deletes the local data.
	LocalOnly bool
}

// UnlinkResult is the output of AccountUnlink.
type UnlinkResult struct {
	// Account is the removed account.
	Account signal.Account
	// LocalOnly reports whether the server was skipped: requested, or because the account was
	// already marked as unlinked.
	LocalOnly bool
}

// AccountUnlink removes this device from the account (unless req.LocalOnly) and deletes the
// account's local data.
func (a *App) AccountUnlink(ctx context.Context, req UnlinkRequest) (UnlinkResult, error) {
	acc, err := a.client.Unlink(ctx, signal.UnlinkOptions{LocalOnly: req.LocalOnly})
	if err != nil {
		return UnlinkResult{}, fmt.Errorf("account unlink: %w", err)
	}

	return UnlinkResult{Account: acc, LocalOnly: req.LocalOnly || acc.Unlinked()}, nil
}

// connectSendOnly connects the client in send-only mode (see signal.SendOnly). A client that is
// connected already, like the MCP server's, is used as it is.
func (a *App) connectSendOnly(ctx context.Context) error {
	err := a.client.Connect(ctx, signal.SendOnly())
	if errors.Is(err, signal.ErrAlreadyConnected) {
		return nil
	}

	return err //nolint:wrapcheck // the callers wrap it
}
