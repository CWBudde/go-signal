package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/cwbudde/go-signal/internal/signal"
)

// GroupLinkRequest identifies a group by cached title, ID or master key.
// Invite URLs are not accepted as group references.
type GroupLinkRequest struct {
	Group string
}

// Check validates the reference before opening an account, without exposing it in errors.
func (r GroupLinkRequest) Check() error {
	group := strings.TrimSpace(r.Group)
	if group == "" || strings.HasPrefix(strings.ToLower(group), "https://signal.group/") {
		return signal.GroupLinkReferenceError(signal.ErrUnknownGroup) //nolint:wrapcheck // safe validation error
	}

	if strings.HasPrefix(group, GroupPrefix) {
		_, err := parseGroup(group)
		if err != nil {
			return signal.GroupLinkReferenceError(err) //nolint:wrapcheck // safe validation error
		}
	}

	return nil
}

// GroupLinkUpdateRequest changes invite-link access, rotates its password, or both.
type GroupLinkUpdateRequest struct {
	Group  string
	Update signal.GroupLinkUpdate
}

// Check validates the reference and update before opening an account.
func (r GroupLinkUpdateRequest) Check() error {
	err := (GroupLinkRequest{Group: r.Group}).Check()
	if err != nil {
		return err
	}

	return r.Update.Check() //nolint:wrapcheck // self-contained validation
}

// GroupsLinkShow returns a fresh dedicated invite-link view for a full member.
func (a *App) GroupsLinkShow(ctx context.Context, req GroupLinkRequest) (signal.GroupLink, error) {
	err := req.Check()
	if err != nil {
		return signal.GroupLink{}, fmt.Errorf("groups link show: %w", err)
	}

	ref, err := a.ResolveGroup(ctx, req.Group)
	if err != nil {
		return signal.GroupLink{}, fmt.Errorf("groups link show: %w", signal.GroupLinkReferenceError(err))
	}

	err = a.connectSendOnly(ctx)
	if err != nil {
		return signal.GroupLink{}, fmt.Errorf("groups link show: connect: %w", err)
	}

	link, err := a.client.GroupLink(ctx, ref)
	if err != nil {
		return link, fmt.Errorf("groups link show: %w", err)
	}

	return link, nil
}

// GroupsLinkUpdate changes a link once, without retries. Accepted metadata is retained
// when fetching fresh state fails; callers must inspect state before retrying.
func (a *App) GroupsLinkUpdate(ctx context.Context, req GroupLinkUpdateRequest) (signal.GroupLink, error) {
	err := req.Check()
	if err != nil {
		return signal.GroupLink{}, fmt.Errorf("groups link update: %w", err)
	}

	ref, err := a.ResolveGroup(ctx, req.Group)
	if err != nil {
		return signal.GroupLink{}, fmt.Errorf("groups link update: %w", signal.GroupLinkReferenceError(err))
	}

	err = a.connectSendOnly(ctx)
	if err != nil {
		return signal.GroupLink{}, fmt.Errorf("groups link update: connect: %w", err)
	}

	link, err := a.client.UpdateGroupLink(ctx, ref, req.Update)
	if err != nil {
		if link.ID != "" {
			return link, fmt.Errorf("groups link update: change accepted for group %s at revision %d; "+
				"inspect groups link show %s before retrying: %w", link.ID, link.Revision, link.ID, err)
		}

		if errors.Is(err, signal.ErrGroupUpdateUncertain) {
			return link, fmt.Errorf("groups link update: inspect groups link show before retrying: %w", err)
		}

		return link, fmt.Errorf("groups link update: %w", err)
	}

	return link, nil
}
