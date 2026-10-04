package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/google/uuid"
)

// RemoveGroupMembersRequest is the input of GroupsRemoveMembers.
type RemoveGroupMembersRequest struct {
	// Group is an ID, master key or previously fetched title (see ResolveGroup).
	Group string
	// Members accepts numbers, ACIs, @usernames or explicit PNI:<uuid> invitations.
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
		target, err := parseRemovalMember(arg)
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

	targets, err := a.resolveRemovalMembers(ctx, req.Members)
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

// Explicit PNI input is limited to invitation revocation, not ordinary recipient sending.
func parseRemovalMember(arg string) (Target, error) {
	arg = strings.TrimSpace(arg)
	if !strings.HasPrefix(strings.ToUpper(arg), "PNI:") {
		return ParseRecipient(arg)
	}

	id, err := uuid.Parse(arg[len("PNI:"):])
	if err != nil || id == uuid.Nil || len(arg) != len("PNI:")+len(uuid.Nil.String()) {
		return Target{}, fmt.Errorf("%w %q: want PNI:<nonzero uuid>", ErrInvalidRecipient, arg)
	}

	return Target{Recipient: signal.Recipient{PNI: id.String()}}, nil
}

func (a *App) resolveRemovalMembers(ctx context.Context, args []string) ([]Target, error) {
	targets := make([]Target, len(args))

	var (
		users   []Target
		indices []int
	)

	for i, arg := range args {
		target, err := parseRemovalMember(arg)
		if err != nil {
			return nil, err
		}

		targets[i] = target
		if target.Recipient.PNI == "" {
			users = append(users, target)
			indices = append(indices, i)
		}
	}

	err := a.resolveUsers(ctx, users)
	if err != nil {
		return nil, err
	}

	for i, index := range indices {
		// Only number lookup supplies a PNI for revocation. Usernames and bare ACIs
		// keep their ACI-only contract even if a resolver returns additional identity data.
		if targets[index].Recipient.Number == "" {
			users[i].Recipient.PNI = ""
		}

		targets[index] = users[i]
	}

	return targets, nil
}
