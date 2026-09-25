// Package app holds the use cases behind the CLI commands (and later the MCP tools): each takes
// a typed request and returns a typed result, without printing or Cobra. cmd/ only parses flags,
// calls App and renders the result with internal/output.
package app

import (
	"context"
	"fmt"

	"github.com/cwbudde/go-signal/internal/signal"
)

// App runs the use cases against one open signal.Client, which stays owned by the caller.
type App struct {
	client signal.Client
}

// New returns an App on client.
func New(client signal.Client) *App {
	return &App{client: client}
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
