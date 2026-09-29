package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/cwbudde/go-signal/internal/signal"
)

// RemoveGroupMembersRequest is the input of GroupsRemoveMembers.
type RemoveGroupMembersRequest struct {
	// Group is an ID, master key or previously fetched title (see ResolveGroup).
	Group string
	// Members accepts numbers, ACIs or @usernames of members, invitees or join requesters.
	Members []string
}

// Check validates syntax without opening the account or looking up recipients.
func (r RemoveGroupMembersRequest) Check() error {
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
		return fmt.Errorf("%w: specify at least one member to remove", ErrInvalidRecipient)
	}

	for _, arg := range r.Members {
		target, err := ParseRecipient(arg)
		if err != nil {
			return err
		}

		if target.IsGroup() {
			return fmt.Errorf("%w %q: a group cannot be a group member", ErrInvalidRecipient, arg)
		}

		if target.Self {
			return fmt.Errorf("%w: use groups leave to remove yourself", ErrInvalidRecipient)
		}
	}

	return nil
}

// GroupsRemoveMembers removes members, revokes invitations or rejects join requests in a
// single group change. All recipients are resolved before changing the group. The account
// must be an administrator. Server errors are returned without retrying a potentially applied change.
func (a *App) GroupsRemoveMembers(ctx context.Context, req RemoveGroupMembersRequest) (signal.Group, error) {
	err := req.Check()
	if err != nil {
		return signal.Group{}, fmt.Errorf("groups remove-members: %w", err)
	}

	ref, err := a.ResolveGroup(ctx, req.Group)
	if err != nil {
		return signal.Group{}, fmt.Errorf("groups remove-members: %w", err)
	}

	err = a.connectSendOnly(ctx)
	if err != nil {
		return signal.Group{}, fmt.Errorf("groups remove-members: connect: %w", err)
	}

	targets, err := a.ResolveRecipients(ctx, req.Members)
	if err != nil {
		return signal.Group{}, fmt.Errorf("groups remove-members: members: %w", err)
	}

	members := make([]signal.Recipient, 0, len(targets))

	for _, target := range targets {
		if target.Self {
			return signal.Group{}, fmt.Errorf("groups remove-members: %w: use groups leave to remove yourself",
				ErrInvalidRecipient)
		}

		members = append(members, target.Recipient)
	}

	group, err := a.client.RemoveGroupMembers(ctx, ref, members)
	if err != nil {
		return group, fmt.Errorf("groups remove-members: %w", err)
	}

	return group, nil
}
