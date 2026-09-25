package app

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/google/uuid"
)

// ErrInvalidRecipient means that a recipient argument has none of the accepted forms.
var ErrInvalidRecipient = errors.New("invalid recipient")

const (
	// SelfRecipient is the recipient argument for note-to-self.
	SelfRecipient = "self"
	// GroupPrefix starts a group recipient argument: group:<base64 group ID>.
	GroupPrefix = "group:"

	groupIDLen = 32
)

// e164 is + and up to 15 digits without a leading zero (ITU-T E.164).
var e164 = regexp.MustCompile(`^\+[1-9][0-9]{1,14}$`)

// Target is where a message goes: a user (Recipient) or a group (GroupID).
type Target struct {
	Recipient signal.Recipient
	// GroupID is the base64 group identifier (standard encoding).
	GroupID string
	// Self marks note-to-self; after ResolveRecipients, Recipient is the own account.
	Self bool
}

// IsGroup reports whether t is a group.
func (t Target) IsGroup() bool {
	return t.GroupID != ""
}

// String returns t in the form ParseRecipient accepts.
func (t Target) String() string {
	switch {
	case t.IsGroup():
		return GroupPrefix + t.GroupID
	case t.Self:
		return SelfRecipient
	case t.Recipient.Username != "" && t.Recipient.ACI == "":
		return "@" + t.Recipient.Username
	default:
		return t.Recipient.String()
	}
}

// ParseRecipient parses a recipient argument: an E.164 number (+4915112345678), an ACI (UUID),
// @username (nickname.discriminator), group:<id> or self. It doesn't contact the server.
func ParseRecipient(arg string) (Target, error) {
	arg = strings.TrimSpace(arg)

	switch {
	case strings.EqualFold(arg, SelfRecipient):
		return Target{Self: true}, nil
	case strings.HasPrefix(arg, GroupPrefix):
		return parseGroup(arg)
	case strings.HasPrefix(arg, "@"):
		name := arg[1:]
		if name == "" || strings.ContainsAny(name, " @") {
			return Target{}, fmt.Errorf("%w %q: want @nickname.discriminator", ErrInvalidRecipient, arg)
		}

		return Target{Recipient: signal.Recipient{Username: name}}, nil
	case strings.HasPrefix(arg, "+"):
		if !e164.MatchString(arg) {
			return Target{}, fmt.Errorf("%w %q: want + and the number with country code", ErrInvalidRecipient, arg)
		}

		return Target{Recipient: signal.Recipient{Number: arg}}, nil
	}

	aci, err := uuid.Parse(arg)
	if err != nil || len(arg) != len(uuid.Nil.String()) {
		return Target{}, fmt.Errorf("%w %q: want +<number>, @<username>, an ACI, %s<id> or %s",
			ErrInvalidRecipient, arg, GroupPrefix, SelfRecipient)
	}

	return Target{Recipient: signal.Recipient{ACI: aci.String()}}, nil
}

func parseGroup(arg string) (Target, error) {
	encoded := strings.TrimPrefix(arg, GroupPrefix)

	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		// Also accept the URL-safe alphabet, as in group links.
		raw, err = base64.RawURLEncoding.DecodeString(strings.TrimRight(encoded, "="))
	}

	if err != nil || len(raw) != groupIDLen {
		return Target{}, fmt.Errorf("%w %q: want %s and a base64 group ID of %d bytes",
			ErrInvalidRecipient, arg, GroupPrefix, groupIDLen)
	}

	return Target{GroupID: base64.StdEncoding.EncodeToString(raw)}, nil
}

// ResolveRecipients parses args (see ParseRecipient) and resolves every user to an ACI: self and
// the account's own number or ACI become note-to-self, numbers and usernames are looked up with
// Client.Resolve (numbers need Connect unless cached). Duplicates are dropped, keeping the order.
// All invalid arguments or unresolvable recipients are reported together; a recipient without a
// Signal account fails with signal.ErrNotOnSignal.
func (a *App) ResolveRecipients(ctx context.Context, args []string) ([]Target, error) {
	targets := make([]Target, 0, len(args))

	var errs []error

	for _, arg := range args {
		target, err := ParseRecipient(arg)
		if err != nil {
			errs = append(errs, err)

			continue
		}

		targets = append(targets, target)
	}

	err := errors.Join(errs...)
	if err != nil {
		return nil, err
	}

	err = a.resolveUsers(ctx, targets)
	if err != nil {
		return nil, fmt.Errorf("resolve recipients: %w", err)
	}

	return dedupe(targets), nil
}

// resolveUsers fills in the ACI of every user in targets.
func (a *App) resolveUsers(ctx context.Context, targets []Target) error {
	pending, err := a.markSelf(ctx, targets)
	if err != nil || len(pending) == 0 {
		return err
	}

	lookup := make([]signal.Recipient, 0, len(pending))
	for _, i := range pending {
		lookup = append(lookup, targets[i].Recipient)
	}

	resolved, err := a.client.Resolve(ctx, lookup)
	if err != nil {
		return err //nolint:wrapcheck // wrapped by ResolveRecipients
	}

	for n, i := range pending {
		targets[i].Recipient = resolved[n]
	}

	return nil
}

// markSelf turns self and the account's own number or ACI into note-to-self targets. It returns
// the indices of the users left to look up.
func (a *App) markSelf(ctx context.Context, targets []Target) ([]int, error) {
	var (
		own     *signal.Account
		pending []int
	)

	for i := range targets {
		if targets[i].IsGroup() {
			continue
		}

		if own == nil {
			acc, err := a.client.Account(ctx)
			if err != nil {
				return nil, err //nolint:wrapcheck // wrapped by ResolveRecipients
			}

			own = &acc
		}

		if isOwn(targets[i], *own) {
			targets[i] = Target{Self: true, Recipient: signal.Recipient{ACI: own.ACI, Number: own.Number}}
		} else if targets[i].Recipient.ACI == "" {
			pending = append(pending, i)
		}
	}

	return pending, nil
}

func isOwn(target Target, own signal.Account) bool {
	rcpt := target.Recipient

	return target.Self || rcpt.ACI == own.ACI || (rcpt.Number != "" && rcpt.Number == own.Number)
}

// dedupe drops targets that name the same group or ACI as an earlier one.
func dedupe(targets []Target) []Target {
	seen := make(map[string]bool, len(targets))
	out := targets[:0]

	for _, target := range targets {
		key := GroupPrefix + target.GroupID
		if !target.IsGroup() {
			key = target.Recipient.ACI
		}

		if seen[key] {
			continue
		}

		seen[key] = true

		out = append(out, target)
	}

	return out
}
