package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/cwbudde/go-signal/internal/signal"
)

// IdentitiesListRequest is the input of IdentitiesList.
type IdentitiesListRequest struct {
	// Recipient is an optional user argument (see ParseRecipient) to list only their key.
	Recipient string
}

// IdentitiesList lists the identity keys stored for other users, with their trust level (see
// signal.Client.Identities). With req.Recipient only that user's key is listed, or none. It
// doesn't connect; a phone number must be in the store already (it is after sending to it).
func (a *App) IdentitiesList(ctx context.Context, req IdentitiesListRequest) ([]signal.Identity, error) {
	var rcpt *signal.Recipient

	if req.Recipient != "" {
		user, err := a.identityUser(ctx, req.Recipient)
		if err != nil {
			return nil, fmt.Errorf("identities list: %w", err)
		}

		rcpt = &user
	}

	ids, err := a.client.Identities(ctx, rcpt)
	if err != nil {
		return nil, fmt.Errorf("identities list: %w", err)
	}

	return ids, nil
}

// IdentitiesShow returns the safety number with the user recipient (see ParseRecipient) and their
// identity key. It fails with signal.ErrUnknownIdentity when no key is stored for them yet.
func (a *App) IdentitiesShow(ctx context.Context, recipient string) (signal.SafetyNumber, error) {
	user, err := a.identityUser(ctx, recipient)
	if err != nil {
		return signal.SafetyNumber{}, fmt.Errorf("identities show: %w", err)
	}

	number, err := a.client.SafetyNumber(ctx, user)
	if err != nil {
		return signal.SafetyNumber{}, fmt.Errorf("identities show: %w", err)
	}

	return number, nil
}

// IdentitiesTrustRequest is the input of IdentitiesTrust.
type IdentitiesTrustRequest struct {
	// Recipient is the user argument (see ParseRecipient).
	Recipient string
	// SafetyNumber, if set, verifies the key: it must be the current safety number (60 digits,
	// white space ignored).
	SafetyNumber string
}

// IdentitiesTrust trusts the current identity key of req.Recipient, so that sending to them works
// again after their key changed: verified when req.SafetyNumber matches, otherwise unverified (see
// signal.Client.TrustIdentity). It returns the updated identity.
func (a *App) IdentitiesTrust(ctx context.Context, req IdentitiesTrustRequest) (signal.Identity, error) {
	if req.SafetyNumber != "" {
		_, err := signal.NormalizeSafetyNumber(req.SafetyNumber)
		if err != nil {
			return signal.Identity{}, fmt.Errorf("identities trust: %w", err)
		}
	}

	user, err := a.identityUser(ctx, req.Recipient)
	if err != nil {
		return signal.Identity{}, fmt.Errorf("identities trust: %w", err)
	}

	id, err := a.client.TrustIdentity(ctx, user, req.SafetyNumber)
	if err != nil {
		return signal.Identity{}, fmt.Errorf("identities trust: %w", err)
	}

	return id, nil
}

// identityUser resolves arg to another user with ACI; groups and our own account have no
// identity key to trust.
func (a *App) identityUser(ctx context.Context, arg string) (signal.Recipient, error) {
	targets, err := a.ResolveRecipients(ctx, []string{arg})
	if errors.Is(err, signal.ErrNotConnected) {
		return signal.Recipient{}, fmt.Errorf("%w (numbers are looked up only when sending; use the ACI)", err)
	}

	if err != nil {
		return signal.Recipient{}, err
	}

	switch target := targets[0]; {
	case target.IsGroup():
		return signal.Recipient{}, fmt.Errorf("%w %q: identity keys belong to users, not groups", ErrInvalidRecipient, arg)
	case target.Self:
		return signal.Recipient{}, fmt.Errorf("%w %q: that is this account", ErrInvalidRecipient, arg)
	default:
		return target.Recipient, nil
	}
}
