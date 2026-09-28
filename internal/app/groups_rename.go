package app

import (
	"context"
	"fmt"

	"github.com/cwbudde/go-signal/internal/signal"
)

// RenameGroupRequest is the input of GroupsRename.
type RenameGroupRequest struct {
	// Group is an ID, master key or previously fetched title (see ResolveGroup).
	Group string
	Title string
}

// GroupsRename updates a group's title and cached title, connecting in send-only mode.
// The account must be a full member permitted to edit the group's attributes.
func (a *App) GroupsRename(ctx context.Context, req RenameGroupRequest) (signal.Group, error) {
	err := signal.ValidateGroupTitle(req.Title)
	if err != nil {
		return signal.Group{}, fmt.Errorf("groups rename: %w", err)
	}

	ref, err := a.ResolveGroup(ctx, req.Group)
	if err != nil {
		return signal.Group{}, fmt.Errorf("groups rename: %w", err)
	}

	err = a.connectSendOnly(ctx)
	if err != nil {
		return signal.Group{}, fmt.Errorf("groups rename: connect: %w", err)
	}

	group, err := a.client.RenameGroup(ctx, ref, req.Title)
	if err != nil {
		return signal.Group{}, fmt.Errorf("groups rename: %w", err)
	}

	return group, nil
}
