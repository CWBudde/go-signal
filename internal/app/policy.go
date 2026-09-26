package app

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/cwbudde/go-signal/internal/signal"
)

// ErrRecipientNotAllowed means that a send, reaction or delete goes to a chat that the App's
// Allowlist doesn't allow.
var ErrRecipientNotAllowed = errors.New("recipient not allowed")

// AllowAll is the Allowlist entry that allows every recipient.
const AllowAll = "*"

// Allowlist restricts the chats that Send, React and Delete go to (see WithAllowlist). Mentioned
// users and quote authors don't receive anything and aren't checked.
type Allowlist struct {
	all     bool
	entries []Target

	mu       sync.Mutex
	resolved []Target
	done     bool
}

// ParseAllowlist parses allowlist entries without contacting the server: recipient arguments as
// ParseRecipient takes them (users, group:<id>, self) or AllowAll. No entries allow nothing.
func ParseAllowlist(args []string) (*Allowlist, error) {
	list := &Allowlist{}

	var errs []error

	for _, arg := range args {
		if strings.TrimSpace(arg) == AllowAll {
			list.all = true

			continue
		}

		target, err := ParseRecipient(arg)
		if err != nil {
			errs = append(errs, err)

			continue
		}

		list.entries = append(list.entries, target)
	}

	err := errors.Join(errs...)
	if err != nil {
		return nil, fmt.Errorf("allowlist: %w", err)
	}

	return list, nil
}

// All reports whether the allowlist allows every recipient.
func (l *Allowlist) All() bool {
	return l.all
}

// Empty reports whether the allowlist allows no recipient at all.
func (l *Allowlist) Empty() bool {
	return !l.all && len(l.entries) == 0
}

// Len returns the number of entries besides AllowAll.
func (l *Allowlist) Len() int {
	return len(l.entries)
}

// Missing returns the entries of other that l doesn't allow, compared as written: a number in one
// list and the same user's ACI in the other don't match. Nothing is missing if l allows everyone.
func (l *Allowlist) Missing(other *Allowlist) []string {
	if l.all {
		return nil
	}

	var missing []string

	for _, entry := range other.entries {
		if !slices.ContainsFunc(l.entries, func(t Target) bool { return t.String() == entry.String() }) {
			missing = append(missing, entry.String())
		}
	}

	if other.all {
		missing = append(missing, AllowAll)
	}

	return missing
}

// Allowlist returns the list set with WithAllowlist; nil means that sending isn't restricted.
func (a *App) Allowlist() *Allowlist {
	return a.allow
}

// WithAllowlist restricts Send, React and Delete to the chats that list allows; they fail with
// ErrRecipientNotAllowed before anything is uploaded or sent. Without it, they aren't restricted.
func WithAllowlist(list *Allowlist) Option {
	return func(a *App) {
		a.allow = list
	}
}

// CheckRecipients resolves recipient arguments like ResolveRecipients and checks them against
// the allowlist (see WithAllowlist), as Send, React and Delete do before sending.
func (a *App) CheckRecipients(ctx context.Context, args []string) error {
	targets, err := a.ResolveRecipients(ctx, args)
	if err != nil {
		return err
	}

	return a.checkAllowed(ctx, targets)
}

// checkAllowed fails with ErrRecipientNotAllowed, naming every target that the App's allowlist
// doesn't allow.
func (a *App) checkAllowed(ctx context.Context, targets []Target) error {
	if a.allow == nil || a.allow.all {
		return nil
	}

	allowed, err := a.resolveList(ctx, a.allow)
	if err != nil {
		return err
	}

	var denied []string

	for _, target := range targets {
		if !allows(allowed, target) {
			denied = append(denied, target.String())
		}
	}

	if len(denied) > 0 {
		return fmt.Errorf("%w: %s (mcp serve --allow-recipient)", ErrRecipientNotAllowed, strings.Join(denied, ", "))
	}

	return nil
}

// ChatAllowed reports whether list allows chat: its group, or for a 1:1 chat the other user.
// `mcp serve --hook-from` uses it to pick the chats whose messages run the hook.
func (a *App) ChatAllowed(ctx context.Context, list *Allowlist, chat signal.Chat) (bool, error) {
	if list == nil || list.Empty() {
		return false, nil
	}

	if list.all {
		return true, nil
	}

	target := Target{Recipient: chat.Recipient, GroupID: chat.GroupID}
	if !target.IsGroup() && target.Recipient.ACI == "" {
		return false, nil
	}

	entries, err := a.resolveList(ctx, list)
	if err != nil {
		return false, err
	}

	return allows(entries, target), nil
}

// resolveList returns list's entries with their users resolved like ResolveRecipients does.
// They are resolved once; entries without a Signal account are left out, other failures are
// retried next time.
func (a *App) resolveList(ctx context.Context, list *Allowlist) ([]Target, error) {
	list.mu.Lock()
	defer list.mu.Unlock()

	if list.done {
		return list.resolved, nil
	}

	resolved := make([]Target, 0, len(list.entries))

	for _, entry := range list.entries {
		targets := []Target{entry}

		err := a.resolveUsers(ctx, targets)
		switch {
		case errors.Is(err, signal.ErrNotOnSignal):
			continue
		case err != nil:
			return nil, fmt.Errorf("resolve allowed recipient %s: %w", entry, err)
		}

		resolved = append(resolved, targets[0])
	}

	list.resolved, list.done = resolved, true

	return resolved, nil
}

// allows reports whether target is among the resolved entries.
func allows(entries []Target, target Target) bool {
	for _, entry := range entries {
		if sameTarget(entry, target) {
			return true
		}
	}

	return false
}

// sameTarget reports whether two resolved targets are the same group, note-to-self or user.
func sameTarget(a, b Target) bool {
	switch {
	case a.IsGroup() || b.IsGroup():
		return a.GroupID == b.GroupID
	case a.Self || b.Self:
		return a.Self && b.Self
	default:
		return a.Recipient.ACI != "" && a.Recipient.ACI == b.Recipient.ACI
	}
}
