package app

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/cwbudde/go-signal/internal/signal"
)

// ErrNotAUser means that a contacts command got a group or self where it needs another user.
var ErrNotAUser = errors.New("not a user")

// ContactsListRequest is the input of ContactsList.
type ContactsListRequest struct {
	// Blocked lists only blocked users.
	Blocked bool
	// Query keeps only users whose name (any of them), number or ACI contains it, ignoring case.
	Query string
}

// ContactsList returns the users the store knows (see signal.Client.Contacts), filtered by req
// and sorted by display name. It needs no connection.
func (a *App) ContactsList(ctx context.Context, req ContactsListRequest) ([]signal.Contact, error) {
	contacts, err := a.client.Contacts(ctx)
	if err != nil {
		return nil, fmt.Errorf("contacts list: %w", err)
	}

	query := strings.ToLower(strings.TrimSpace(req.Query))

	contacts = slices.DeleteFunc(contacts, func(contact signal.Contact) bool {
		return (req.Blocked && !contact.Blocked) || !matches(contact, query)
	})

	SortContacts(contacts)

	return contacts, nil
}

// SortContacts sorts contacts by display name (ignoring case), then by ACI.
func SortContacts(contacts []signal.Contact) {
	slices.SortStableFunc(contacts, func(a, b signal.Contact) int {
		return cmp.Or(
			cmp.Compare(strings.ToLower(a.DisplayName()), strings.ToLower(b.DisplayName())),
			cmp.Compare(a.ACI, b.ACI),
		)
	})
}

// matches reports whether one of contact's names or identifiers contains query (lower case).
func matches(contact signal.Contact, query string) bool {
	if query == "" {
		return true
	}

	for _, field := range []string{
		contact.Nickname, contact.ContactName, contact.ProfileName, contact.Number, contact.ACI, contact.PNI,
	} {
		if strings.Contains(strings.ToLower(field), query) {
			return true
		}
	}

	return false
}

// ContactsShow returns what the store knows about one user: a number, ACI, @username (looked up
// on the server, no connection needed) or self. It fails with signal.ErrUnknownContact for users
// the store doesn't know, and with ErrNotAUser for a group.
func (a *App) ContactsShow(ctx context.Context, arg string) (signal.Contact, error) {
	target, err := ParseRecipient(arg)
	if err != nil {
		return signal.Contact{}, fmt.Errorf("contacts show: %w", err)
	}

	if target.IsGroup() {
		return signal.Contact{}, fmt.Errorf("contacts show: %s: %w", arg, ErrNotAUser)
	}

	rcpt := target.Recipient

	switch {
	case target.Self:
		acc, err := a.client.Account(ctx)
		if err != nil {
			return signal.Contact{}, fmt.Errorf("contacts show: %w", err)
		}

		rcpt = signal.Recipient{ACI: acc.ACI}
	case rcpt.Username != "":
		resolved, err := a.client.Resolve(ctx, []signal.Recipient{rcpt})
		if err != nil {
			return signal.Contact{}, fmt.Errorf("contacts show: %w", err)
		}

		rcpt = signal.Recipient{ACI: resolved[0].ACI}
	}

	contact, err := a.client.Contact(ctx, rcpt)
	if err != nil {
		return signal.Contact{}, fmt.Errorf("contacts show: %w", err)
	}

	return contact, nil
}

// BlockResult is the output of ContactsBlock and ContactsUnblock.
type BlockResult struct {
	// Blocked is true for a block, false for an unblock.
	Blocked bool
	// Results has one entry per user, in the order of the arguments (without duplicates).
	Results []BlockChange
}

// BlockChange is the outcome for one user.
type BlockChange struct {
	// Contact is the user afterwards, as far as the store knows them.
	Contact signal.Contact
	// Changed is false if the user already was blocked (or unblocked).
	Changed bool
}

// ContactsBlock blocks users (see setBlocked).
func (a *App) ContactsBlock(ctx context.Context, args []string) (BlockResult, error) {
	return a.setBlocked(ctx, "contacts block", args, true)
}

// ContactsUnblock unblocks users (see setBlocked).
func (a *App) ContactsUnblock(ctx context.Context, args []string) (BlockResult, error) {
	return a.setBlocked(ctx, "contacts unblock", args, false)
}

// setBlocked blocks or unblocks the users args (numbers, ACIs, @usernames; no groups, not self)
// with signal.Client.SetBlocked: our other devices get the new blocked list, and the phone
// updates the storage service. It connects in send-only mode, so the client must not be
// connected yet. Invalid arguments fail before connecting; users that can't be resolved fail
// before anything changes.
func (a *App) setBlocked(ctx context.Context, action string, args []string, blocked bool) (BlockResult, error) {
	err := checkUsers(args)
	if err != nil {
		return BlockResult{}, fmt.Errorf("%s: %w", action, err)
	}

	err = a.client.Connect(ctx, signal.SendOnly())
	if err != nil {
		return BlockResult{}, fmt.Errorf("%s: connect: %w", action, err)
	}

	targets, err := a.ResolveRecipients(ctx, args)
	if err != nil {
		return BlockResult{}, fmt.Errorf("%s: %w", action, err)
	}

	recipients, res, err := a.blockTargets(ctx, targets, blocked)
	if err != nil {
		return BlockResult{}, fmt.Errorf("%s: %w", action, err)
	}

	err = a.client.SetBlocked(ctx, recipients, blocked)
	if err != nil {
		return BlockResult{}, fmt.Errorf("%s: %w", action, err)
	}

	for i, rcpt := range recipients {
		contact, err := a.client.Contact(ctx, rcpt)
		if err != nil {
			contact = signal.Contact{Recipient: rcpt, Blocked: blocked}
		}

		// Keep the number the user gave, if the store has none.
		if contact.Number == "" {
			contact.Number = rcpt.Number
		}

		res.Results[i].Contact = contact
	}

	return res, nil
}

// blockTargets returns the users of targets and a result that says which of them change.
func (a *App) blockTargets(ctx context.Context, targets []Target, blocked bool,
) ([]signal.Recipient, BlockResult, error) {
	res := BlockResult{Blocked: blocked, Results: make([]BlockChange, 0, len(targets))}
	recipients := make([]signal.Recipient, 0, len(targets))

	for _, target := range targets {
		// The own number or ACI resolves to self.
		if target.Self {
			return nil, BlockResult{}, fmt.Errorf("%s: %w: that is this account",
				cmp.Or(target.Recipient.Number, target.Recipient.ACI), ErrNotAUser)
		}

		before, err := a.client.Contact(ctx, target.Recipient)
		changed := err != nil || before.Blocked != blocked

		recipients = append(recipients, target.Recipient)
		res.Results = append(res.Results, BlockChange{Changed: changed})
	}

	return recipients, res, nil
}

// checkUsers checks the arguments of block and unblock without resolving them: at least one,
// each valid, and none a group or self.
func checkUsers(args []string) error {
	if len(args) == 0 {
		return ErrNoRecipients
	}

	var errs []error

	for _, arg := range args {
		target, err := ParseRecipient(arg)

		switch {
		case err != nil:
			errs = append(errs, err)
		case target.IsGroup():
			errs = append(errs, fmt.Errorf("%s: %w (blocking groups isn't supported)", arg, ErrNotAUser))
		case target.Self:
			errs = append(errs, fmt.Errorf("%s: %w: that is this account", arg, ErrNotAUser))
		}
	}

	return errors.Join(errs...)
}
