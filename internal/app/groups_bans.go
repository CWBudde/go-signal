package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/cwbudde/go-signal/internal/signal"
)

// GroupBanRequest is the input of GroupsSetBanned.
type GroupBanRequest struct {
	// Group accepts an ID, master key or previously fetched title.
	Group string
	// Members accepts numbers, ACIs or usernames; self cannot be targeted.
	Members []string
	// Banned is true to ban users or false to lift existing bans.
	Banned bool
}

// Check validates syntax before opening an account or resolving recipients.
func (r GroupBanRequest) Check() error {
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
		return fmt.Errorf("%w: specify at least one user", ErrInvalidRecipient)
	}

	for _, arg := range r.Members {
		target, err := ParseRecipient(arg)
		if err != nil {
			return err
		}

		if target.IsGroup() {
			return fmt.Errorf("%w %q: a group cannot be banned", ErrInvalidRecipient, arg)
		}

		if target.Self {
			return fmt.Errorf("%w: cannot ban or unban yourself", ErrInvalidRecipient)
		}
	}

	return nil
}

// GroupsSetBanned bans or unbans all resolved users in one patch, without retries.
// Accepted ID and revision are retained when fetching the resulting group fails.
func (a *App) GroupsSetBanned(ctx context.Context, req GroupBanRequest) (signal.Group, error) {
	operation := "groups ban"
	if !req.Banned {
		operation = "groups unban"
	}

	err := req.Check()
	if err != nil {
		return signal.Group{}, fmt.Errorf("%s: %w", operation, err)
	}

	ref, err := a.ResolveGroup(ctx, req.Group)
	if err != nil {
		return signal.Group{}, fmt.Errorf("%s: %w", operation, err)
	}

	err = a.connectSendOnly(ctx)
	if err != nil {
		return signal.Group{}, fmt.Errorf("%s: connect: %w", operation, err)
	}

	targets, err := a.ResolveRecipients(ctx, req.Members)
	if err != nil {
		return signal.Group{}, fmt.Errorf("%s: members: %w", operation, err)
	}

	members := make([]signal.Recipient, 0, len(targets))
	for _, target := range targets {
		if target.Self {
			return signal.Group{}, fmt.Errorf("%s: %w: cannot ban or unban yourself", operation, ErrInvalidRecipient)
		}

		members = append(members, target.Recipient)
	}

	group, err := a.client.SetGroupBanned(ctx, ref, members, req.Banned)
	if err != nil {
		if group.ID != "" {
			return group, fmt.Errorf("%s: change accepted for group %s at revision %d; "+
				"inspect groups show %s before retrying: %w", operation, group.ID, group.Revision, group.ID, err)
		}

		return group, fmt.Errorf("%s: %w", operation, err)
	}

	return group, nil
}
