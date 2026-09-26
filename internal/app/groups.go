package app

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/cwbudde/go-signal/internal/signal"
)

// ErrAmbiguousGroup means that a group title matches more than one group; the error lists them.
var ErrAmbiguousGroup = errors.New("several groups have this title; use the group ID")

// GroupsList fetches every known group with its state from the server (signal.Client.Groups).
// It connects in send-only mode, so the client must not be connected yet: incoming messages
// stay on the server for the next receive.
func (a *App) GroupsList(ctx context.Context) ([]signal.Group, error) {
	err := a.client.Connect(ctx, signal.SendOnly())
	if err != nil {
		return nil, fmt.Errorf("groups list: connect: %w", err)
	}

	groups, err := a.client.Groups(ctx)
	if err != nil {
		return nil, fmt.Errorf("groups list: %w", err)
	}

	return groups, nil
}

// GroupsShow fetches the group arg (see ResolveGroup) from the server. It connects like
// GroupsList, after resolving arg, so an unknown title fails without connecting.
func (a *App) GroupsShow(ctx context.Context, arg string) (signal.Group, error) {
	ref, err := a.ResolveGroup(ctx, arg)
	if err != nil {
		return signal.Group{}, fmt.Errorf("groups show: %w", err)
	}

	err = a.client.Connect(ctx, signal.SendOnly())
	if err != nil {
		return signal.Group{}, fmt.Errorf("groups show: connect: %w", err)
	}

	group, err := a.client.Group(ctx, ref)
	if err != nil {
		return signal.Group{}, fmt.Errorf("groups show: %w", err)
	}

	return group, nil
}

// LeaveRequest is the input of GroupsLeave.
type LeaveRequest struct {
	// Group names the group to leave (see ResolveGroup).
	Group string
	// Promote are user recipient arguments (see ParseRecipient) of members to make admins in
	// the same change; needed when we are the group's only admin and other members remain.
	Promote []string
}

// GroupsLeave leaves the group req.Group (signal.Client.LeaveGroup): as a member, it removes us
// and tells the other members; an invitation is declined and a join request cancelled. It
// connects like GroupsList. It fails with signal.ErrLastAdmin when we are the only admin and
// req.Promote names nobody, and with signal.ErrNotAMember when we are not in the group.
func (a *App) GroupsLeave(ctx context.Context, req LeaveRequest) (signal.LeaveResult, error) {
	ref, err := a.ResolveGroup(ctx, req.Group)
	if err != nil {
		return signal.LeaveResult{}, fmt.Errorf("groups leave: %w", err)
	}

	err = a.client.Connect(ctx, signal.SendOnly())
	if err != nil {
		return signal.LeaveResult{}, fmt.Errorf("groups leave: connect: %w", err)
	}

	promote, err := a.resolveMembers(ctx, req.Promote)
	if err != nil {
		return signal.LeaveResult{}, fmt.Errorf("groups leave: promote: %w", err)
	}

	res, err := a.client.LeaveGroup(ctx, ref, signal.LeaveOptions{Promote: promote})
	if err != nil {
		return signal.LeaveResult{}, fmt.Errorf("groups leave: %w", err)
	}

	return res, nil
}

// resolveMembers resolves user recipient arguments to recipients with ACI; groups and self are
// refused.
func (a *App) resolveMembers(ctx context.Context, args []string) ([]signal.Recipient, error) {
	if len(args) == 0 {
		return nil, nil
	}

	targets, err := a.ResolveRecipients(ctx, args)
	if err != nil {
		return nil, err
	}

	out := make([]signal.Recipient, 0, len(targets))

	for _, target := range targets {
		if target.IsGroup() || target.Self {
			return nil, fmt.Errorf("%w %q: want another member of the group", ErrInvalidRecipient, target)
		}

		out = append(out, target.Recipient)
	}

	return out, nil
}

// ResolveGroup turns a group argument into the group ID or master key that signal.Client.Group
// takes: group:<id>, a bare base64 group ID or master key (32 bytes, standard or URL-safe
// alphabet; the result is standard base64), or else a group title. A title must match exactly
// one group fetched before (ignoring case and surrounding white space; see
// signal.Client.GroupTitles): several matches fail with ErrAmbiguousGroup, none with
// signal.ErrUnknownGroup. It doesn't connect.
func (a *App) ResolveGroup(ctx context.Context, arg string) (string, error) {
	arg = strings.TrimSpace(arg)

	if strings.HasPrefix(arg, GroupPrefix) {
		target, err := parseGroup(arg)
		if err != nil {
			return "", err
		}

		return target.GroupID, nil
	}

	groupKey, ok := decodeGroupKey(arg)
	if ok {
		return groupKey, nil
	}

	if arg == "" {
		return "", fmt.Errorf("%w: empty group", signal.ErrUnknownGroup)
	}

	return a.groupByTitle(ctx, arg)
}

// groupByTitle returns the ID of the only group titled title (see ResolveGroup).
func (a *App) groupByTitle(ctx context.Context, title string) (string, error) {
	titles, err := a.client.GroupTitles(ctx)
	if err != nil {
		return "", fmt.Errorf("group titles: %w", err)
	}

	var matches []string

	for groupID, known := range titles {
		if strings.EqualFold(strings.TrimSpace(known), title) {
			matches = append(matches, groupID)
		}
	}

	switch len(matches) {
	case 0:
		return "", fmt.Errorf("%w: no group titled %q is known (group titles are known once the groups "+
			"were listed), and it is no group ID", signal.ErrUnknownGroup, title)
	case 1:
		return matches[0], nil
	default:
		slices.Sort(matches)

		return "", fmt.Errorf("%w %q: %s%s", ErrAmbiguousGroup, title, GroupPrefix,
			strings.Join(matches, ", "+GroupPrefix))
	}
}

// decodeGroupKey decodes a base64 group ID or master key and returns it in standard base64.
func decodeGroupKey(arg string) (string, bool) {
	raw, err := base64.StdEncoding.DecodeString(arg)
	if err != nil {
		// Also accept the URL-safe alphabet, as in group links.
		raw, err = base64.RawURLEncoding.DecodeString(strings.TrimRight(arg, "="))
	}

	if err != nil || len(raw) != groupIDLen {
		return "", false
	}

	return base64.StdEncoding.EncodeToString(raw), true
}
