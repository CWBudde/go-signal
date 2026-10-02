package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/cwbudde/go-signal/internal/signal"
)

// GroupMemberRoleRequest is the input of GroupsSetMemberRole.
type GroupMemberRoleRequest struct {
	// Group accepts an ID, master key or previously fetched title.
	Group string
	// Members accepts numbers, ACIs, usernames or self, naming full members.
	Members []string
	// Role is GroupRoleAdmin for promotion or GroupRoleMember for demotion.
	Role signal.GroupRole
}

// Check validates syntax before opening an account or resolving recipients.
func (r GroupMemberRoleRequest) Check() error {
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

	if r.Role != signal.GroupRoleAdmin && r.Role != signal.GroupRoleMember {
		return fmt.Errorf("%w: role must be admin or member", signal.ErrInvalidGroupMember)
	}

	if len(r.Members) == 0 {
		return fmt.Errorf("%w: specify at least one full member", ErrInvalidRecipient)
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

// GroupsSetMemberRole changes all requested full members in one patch, without retries.
// Accepted ID and revision are retained when fetching the resulting group fails.
func (a *App) GroupsSetMemberRole(ctx context.Context, req GroupMemberRoleRequest) (signal.Group, error) {
	operation := "groups promote"
	if req.Role == signal.GroupRoleMember {
		operation = "groups demote"
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
		members = append(members, target.Recipient)
	}

	group, err := a.client.SetGroupMemberRole(ctx, ref, members, req.Role)
	if err != nil {
		if group.ID != "" {
			return group, fmt.Errorf("%s: change accepted for group %s at revision %d; "+
				"inspect groups show %s before retrying: %w", operation, group.ID, group.Revision, group.ID, err)
		}

		return group, fmt.Errorf("%s: %w", operation, err)
	}

	return group, nil
}
