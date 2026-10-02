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
}

// Check validates syntax before opening an account or looking up a group title.
func (r UpdateGroupRequest) Check() error {
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

	return r.Update.Check() //nolint:wrapcheck // facade validation is self-contained
}

// GroupsUpdate submits a combined settings change once, connecting in send-only mode.
// An accepted result is preserved when response validation or fetching the new state fails.
func (a *App) GroupsUpdate(ctx context.Context, req UpdateGroupRequest) (signal.Group, error) {
	err := req.Check()
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
