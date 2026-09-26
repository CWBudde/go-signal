//go:build cgo || purego

package signal

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/google/uuid"
	mstore "go.mau.fi/mautrix-signal/pkg/signalmeow/store"
)

var errInvalidNumber = errors.New("invalid phone number")

func (c *meowClient) Resolve(ctx context.Context, recipients []Recipient) ([]Recipient, error) {
	// Close waits for a running Resolve like for a send: it is part of one.
	if !c.begin(&c.sending) {
		return nil, ErrClosed
	}
	defer c.sending.Done()

	ctx = c.zlog.WithContext(ctx)

	device := c.connDevice
	if device == nil {
		var err error

		device, err = c.device(ctx)
		if err != nil {
			return nil, err
		}
	}

	out := slices.Clone(recipients)

	var (
		errs     []error
		discover []int // indices of numbers that aren't cached
	)

	for i := range out {
		lookup, err := resolveOffline(ctx, device.RecipientStore, &out[i])
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", recipients[i], err))
		} else if lookup {
			discover = append(discover, i)
		}
	}

	if len(discover) > 0 {
		errs = append(errs, c.discover(ctx, device.RecipientStore, out, discover))
	}

	err := errors.Join(errs...)
	if err != nil {
		return nil, err
	}

	return out, nil
}

// resolveOffline fills in rcpt's ACI without contact discovery, and reports whether rcpt is a
// number that needs it.
func resolveOffline(ctx context.Context, recipients mstore.RecipientStore, rcpt *Recipient) (bool, error) {
	switch {
	case rcpt.ACI != "":
		return false, nil
	case rcpt.Number != "":
		cached, err := cachedNumber(ctx, recipients, rcpt)

		return err == nil && !cached, err
	case rcpt.Username != "":
		aci, err := lookupUsername(ctx, rcpt.Username)
		if err != nil {
			return false, err
		}

		rcpt.ACI = aci.String()

		return false, nil
	default:
		return false, ErrUnresolvable
	}
}

// cachedNumber fills in rcpt's ACI (and PNI) from the store and reports whether it was known.
func cachedNumber(ctx context.Context, recipients mstore.RecipientStore, rcpt *Recipient) (bool, error) {
	known, err := recipients.LoadRecipientByE164(ctx, rcpt.Number)
	if err != nil {
		return false, fmt.Errorf("load recipient: %w", err)
	}

	if known == nil || known.ACI == uuid.Nil {
		return false, nil
	}

	rcpt.ACI = known.ACI.String()
	if known.PNI != uuid.Nil {
		rcpt.PNI = known.PNI.String()
	}

	return true, nil
}

// discover looks up the numbers of out[indices] through contact discovery (CDSI), fills in
// their ACI and PNI and caches them in the store.
func (c *meowClient) discover(
	ctx context.Context, recipients mstore.RecipientStore, out []Recipient, indices []int,
) error {
	c.cliMu.Lock()
	cli := c.cli
	c.cliMu.Unlock()

	if cli == nil {
		return fmt.Errorf("look up %s: %w", numbersOf(out, indices), ErrNotConnected)
	}

	e164s := make([]uint64, 0, len(indices))

	for _, i := range indices {
		e164, err := parseE164(out[i].Number)
		if err != nil {
			return err
		}

		e164s = append(e164s, e164)
	}

	found, err := cli.LookupPhone(ctx, e164s...)
	if err != nil {
		return fmt.Errorf("look up %s: %w", numbersOf(out, indices), err)
	}

	var errs []error

	for n, i := range indices {
		entry, ok := found[e164s[n]]
		if !ok || entry.ACI == uuid.Nil {
			// Without an ACI (not registered, or not discoverable by number) we can't send.
			errs = append(errs, fmt.Errorf("%s: %w", out[i].Number, ErrNotOnSignal))

			continue
		}

		out[i].ACI = entry.ACI.String()
		if entry.PNI != uuid.Nil {
			out[i].PNI = entry.PNI.String()
		}

		_, err := recipients.UpdateRecipientE164(ctx, entry.ACI, entry.PNI, out[i].Number)
		if err != nil {
			c.log.Warn("cache recipient", "number", out[i].Number, "error", err)
		}
	}

	return errors.Join(errs...)
}

// parseE164 converts +<digits> into the number CDSI takes.
func parseE164(number string) (uint64, error) {
	digits, ok := strings.CutPrefix(number, "+")
	if !ok {
		return 0, fmt.Errorf("%w %q: missing +", errInvalidNumber, number)
	}

	e164, err := strconv.ParseUint(digits, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%w %q: %w", errInvalidNumber, number, err)
	}

	return e164, nil
}

func numbersOf(out []Recipient, indices []int) string {
	numbers := make([]string, 0, len(indices))
	for _, i := range indices {
		numbers = append(numbers, out[i].Number)
	}

	return strings.Join(numbers, ", ")
}
