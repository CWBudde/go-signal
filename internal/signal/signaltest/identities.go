package signaltest

import (
	"context"
	"crypto/sha256"
	"fmt"
	"math/big"
	"slices"
	"strings"

	"github.com/cwbudde/go-signal/internal/signal"
)

// SafetyNumberOf is the safety number the fake reports for the account ownACI and id: 60 digits
// derived from both ACIs and id's fingerprint, the same for both sides.
func SafetyNumberOf(ownACI string, id signal.Identity) string {
	sides := []string{ownACI, id.Recipient.ACI}
	slices.Sort(sides)

	sum := sha256.Sum256([]byte(strings.Join(sides, "/") + "/" + id.Fingerprint))
	digits := new(big.Int).SetBytes(sum[:]).String()

	for len(digits) < signal.SafetyNumberDigits {
		digits += digits
	}

	return digits[:signal.SafetyNumberDigits]
}

// ScannableOf is the QR code content the fake reports for SafetyNumberOf.
func ScannableOf(ownACI string, id signal.Identity) []byte {
	return []byte("fake-scannable:" + SafetyNumberOf(ownACI, id))
}

func (c *client) Identities(_ context.Context, rcpt *signal.Recipient) ([]signal.Identity, error) {
	c.fake.mu.Lock()
	defer c.fake.mu.Unlock()

	_, err := c.checkIdentities()
	if err != nil {
		return nil, err
	}

	if rcpt != nil && rcpt.ACI == "" {
		return nil, fmt.Errorf("%s: %w (fake)", rcpt, signal.ErrUnresolvable)
	}

	out := make([]signal.Identity, 0, len(c.fake.Identities))

	for _, id := range c.fake.Identities {
		if rcpt == nil || id.Recipient.ACI == rcpt.ACI {
			out = append(out, id)
		}
	}

	slices.SortFunc(out, func(a, b signal.Identity) int { return strings.Compare(a.Recipient.ACI, b.Recipient.ACI) })

	return out, nil
}

func (c *client) SafetyNumber(_ context.Context, rcpt signal.Recipient) (signal.SafetyNumber, error) {
	c.fake.mu.Lock()
	defer c.fake.mu.Unlock()

	acc, err := c.checkIdentities()
	if err != nil {
		return signal.SafetyNumber{}, err
	}

	i, err := c.fake.identity(rcpt)
	if err != nil {
		return signal.SafetyNumber{}, err
	}

	id := c.fake.Identities[i]

	return signal.SafetyNumber{
		Identity: id, Number: SafetyNumberOf(acc.ACI, id), Scannable: ScannableOf(acc.ACI, id),
	}, nil
}

func (c *client) TrustIdentity(_ context.Context, rcpt signal.Recipient, number string) (signal.Identity, error) {
	c.fake.mu.Lock()
	defer c.fake.mu.Unlock()

	acc, err := c.checkIdentities()
	if err != nil {
		return signal.Identity{}, err
	}

	i, err := c.fake.identity(rcpt)
	if err != nil {
		return signal.Identity{}, err
	}

	identity := &c.fake.Identities[i]
	level := max(signal.TrustUnverified, identity.Trust)

	if number != "" {
		want, err := signal.NormalizeSafetyNumber(number)
		if err != nil {
			return signal.Identity{}, fmt.Errorf("%w (fake)", err)
		}

		if want != SafetyNumberOf(acc.ACI, *identity) {
			return signal.Identity{}, fmt.Errorf("%w with %s (fake)", signal.ErrSafetyNumberMismatch, rcpt)
		}

		level = signal.TrustVerified
	}

	identity.Trust = level

	return *identity, nil
}

// checkIdentities fails like the real client for identity calls, which only need the account's
// data; the caller holds c.fake.mu.
func (c *client) checkIdentities() (signal.Account, error) {
	if c.closed {
		return signal.Account{}, signal.ErrClosed
	}

	acc, err := c.fake.account(c.opts)
	if err != nil {
		return signal.Account{}, err
	}

	if c.fake.IdentitiesErr != nil {
		return signal.Account{}, c.fake.IdentitiesErr
	}

	return acc, nil
}

// identity returns the index of rcpt's identity in f.Identities; the caller holds f.mu.
func (f *Fake) identity(rcpt signal.Recipient) (int, error) {
	if rcpt.ACI == "" {
		return 0, fmt.Errorf("%s: %w (fake)", rcpt, signal.ErrUnresolvable)
	}

	i := slices.IndexFunc(f.Identities, func(id signal.Identity) bool { return id.Recipient.ACI == rcpt.ACI })
	if i < 0 {
		return 0, fmt.Errorf("%w for %s (fake)", signal.ErrUnknownIdentity, rcpt)
	}

	return i, nil
}

// changeIdentity applies an identity change like the real client does when it decrypts a
// message with a new key; the caller holds f.mu.
func (f *Fake) changeIdentity(evt *signal.IdentityChanged) {
	changed := signal.Identity{
		Recipient: evt.Recipient, Fingerprint: evt.NewFingerprint, Trust: signal.TrustUntrusted, ChangedAt: evt.Time,
	}

	i := slices.IndexFunc(f.Identities, func(id signal.Identity) bool { return id.Recipient.ACI == evt.Recipient.ACI })
	if i < 0 {
		f.Identities = append(f.Identities, changed)

		return
	}

	changed.FirstSeen = f.Identities[i].FirstSeen
	f.Identities[i] = changed
}

// untrusted reports whether sending to aci is blocked; the caller holds f.mu.
func (f *Fake) untrusted(aci string) bool {
	return slices.ContainsFunc(f.Identities, func(id signal.Identity) bool {
		return id.Recipient.ACI == aci && id.Trust == signal.TrustUntrusted
	})
}
