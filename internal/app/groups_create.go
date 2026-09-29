package app

import (
	"context"
	"fmt"

	"github.com/cwbudde/go-signal/internal/signal"
)

// CreateGroupRequest is the input of GroupsCreate.
type CreateGroupRequest struct {
	Title string
	// Members accepts numbers, ACIs or @usernames; the creator is included automatically.
	Members []string
}

// Check validates syntax without connecting or looking up recipients.
func (r CreateGroupRequest) Check() error {
	err := signal.ValidateGroupTitle(r.Title)
	if err != nil {
		return err //nolint:wrapcheck // self-contained validation error
	}

	for _, arg := range r.Members {
		target, err := ParseRecipient(arg)
		if err != nil {
			return err
		}

		if target.IsGroup() {
			return fmt.Errorf("%w %q: a group cannot be a group member", ErrInvalidRecipient, arg)
		}
	}

	return nil
}

// GroupsCreate creates and announces a group in send-only mode. Self references are harmless;
// other members are resolved and deduplicated before creation. An error can include a group ID
// to inspect before retrying, because notification can fail after creation succeeds.
func (a *App) GroupsCreate(ctx context.Context, req CreateGroupRequest) (signal.Group, error) {
	err := req.Check()
	if err != nil {
		return signal.Group{}, fmt.Errorf("groups create: %w", err)
	}

	err = a.connectSendOnly(ctx)
	if err != nil {
		return signal.Group{}, fmt.Errorf("groups create: connect: %w", err)
	}

	targets, err := a.ResolveRecipients(ctx, req.Members)
	if err != nil {
		return signal.Group{}, fmt.Errorf("groups create: members: %w", err)
	}

	opts := signal.CreateGroupOptions{Title: req.Title}

	for _, target := range targets {
		if !target.Self {
			opts.Members = append(opts.Members, target.Recipient)
		}
	}

	group, err := a.client.CreateGroup(ctx, opts)
	if err != nil {
		return group, fmt.Errorf("groups create: %w", err)
	}

	return group, nil
}
