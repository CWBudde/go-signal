package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/cwbudde/go-signal/internal/signal"
)

// UpdateGroupRequest is the input of GroupsUpdate.
type UpdateGroupRequest struct {
	// Group accepts an ID, master key or previously fetched title.
	Group  string
	Update signal.GroupUpdate
	// AvatarFile is an optional local PNG/JPEG path; a non-nil empty path is invalid.
	AvatarFile *string
	// RemoveAvatar clears the current avatar and is exclusive with AvatarFile or Update.Avatar.
	RemoveAvatar bool
}

// Check validates the request and any local avatar before opening an account.
// Use Prepare to retain validated bytes and avoid reopening the local file.
func (r UpdateGroupRequest) Check() error {
	_, err := r.Prepare()
	return err
}

func (r UpdateGroupRequest) checkGroup() error {
	group := strings.TrimSpace(r.Group)
	if group == "" {
		return fmt.Errorf("%w: empty group", signal.ErrUnknownGroup)
	}

	if strings.HasPrefix(group, GroupPrefix) {
		_, err := parseGroup(group)
		if err != nil {
			return err
		}
	}

	return nil
}

// GroupsUpdate submits a combined settings change once, connecting in send-only mode.
// An accepted result is preserved when response validation or fetching the new state fails.
func (a *App) GroupsUpdate(ctx context.Context, req UpdateGroupRequest) (signal.Group, error) {
	req, err := req.Prepare()
	if err != nil {
		return signal.Group{}, fmt.Errorf("groups update: %w", err)
	}

	ref, err := a.ResolveGroup(ctx, req.Group)
	if err != nil {
		return signal.Group{}, fmt.Errorf("groups update: %w", err)
	}

	err = a.connectSendOnly(ctx)
	if err != nil {
		return signal.Group{}, fmt.Errorf("groups update: connect: %w", err)
	}

	group, err := a.client.UpdateGroup(ctx, ref, req.Update)
	if err != nil {
		if group.ID != "" {
			return group, fmt.Errorf("groups update: change accepted for group %s at revision %d; "+
				"inspect groups show %s before retrying: %w", group.ID, group.Revision, group.ID, err)
		}

		return group, fmt.Errorf("groups update: %w", err)
	}

	return group, nil
}
