package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/cwbudde/go-signal/internal/signal"
)

// AddGroupMembersRequest is the input of GroupsAddMembers.
type AddGroupMembersRequest struct {
	// Group is an ID, master key or previously fetched title.
	Group string
	// Members accepts numbers, ACIs or @usernames.
	Members []string
}

// Check validates syntax before opening the account or resolving recipients.
func (r AddGroupMembersRequest) Check() error {
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

	if len(r.Members) == 0 {
		return fmt.Errorf("%w: specify at least one member to add", ErrInvalidRecipient)
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

// GroupsAddMembers resolves all recipients before making a single membership change.
// It returns the accepted group state and never retries a potentially applied change.
func (a *App) GroupsAddMembers(ctx context.Context, req AddGroupMembersRequest) (signal.Group, error) {
	err := req.Check()
	if err != nil {
		return signal.Group{}, fmt.Errorf("groups add-members: %w", err)
	}

	ref, err := a.ResolveGroup(ctx, req.Group)
	if err != nil {
		return signal.Group{}, fmt.Errorf("groups add-members: %w", err)
	}

	err = a.connectSendOnly(ctx)
	if err != nil {
		return signal.Group{}, fmt.Errorf("groups add-members: connect: %w", err)
	}

	targets, err := a.ResolveRecipients(ctx, req.Members)
	if err != nil {
		return signal.Group{}, fmt.Errorf("groups add-members: members: %w", err)
	}

	members := make([]signal.Recipient, 0, len(targets))
	for _, target := range targets {
		members = append(members, target.Recipient)
	}

	group, err := a.client.AddGroupMembers(ctx, ref, members)
	if err != nil {
		return group, fmt.Errorf("groups add-members: %w", err)
	}

	return group, nil
}
