//go:build integration && (cgo || libsignal_go)

package signal

import (
	"context"
	"errors"
	"fmt"

	"github.com/cwbudde/mautrix-signal/pkg/signalmeow"
	"github.com/google/uuid"
)

var errNotMeow = errors.New("not a signalmeow client")

// LookupPhone runs contact discovery (CDSI) for number on the connected client c, bypassing the
// store's cache that Resolve checks first, and returns the ACI and PNI found (empty if none).
func LookupPhone(ctx context.Context, c Client, number string) (string, string, error) {
	cli, err := connectedCli(c)
	if err != nil {
		return "", "", err
	}

	e164, err := parseE164(number)
	if err != nil {
		return "", "", err
	}

	found, err := cli.LookupPhone(ctx, e164)
	if err != nil {
		return "", "", fmt.Errorf("look up %s: %w", number, err)
	}

	entry, ok := found[e164]
	if !ok {
		return "", "", nil
	}

	return nilOr(entry.ACI), nilOr(entry.PNI), nil
}

// FetchProfile fetches the profile of aci from the server on the connected client c, bypassing
// signalmeow's profile cache, and returns its decrypted name. It also fetches an expiring profile
// key credential for aci and verifies it against the server's zkgroup parameters, the path group
// changes take.
func FetchProfile(ctx context.Context, c Client, aci string) (string, error) {
	cli, err := connectedCli(c)
	if err != nil {
		return "", err
	}

	signalID, err := uuid.Parse(aci)
	if err != nil {
		return "", fmt.Errorf("parse ACI %q: %w", aci, err)
	}

	profile, err := cli.RetrieveProfileByID(ctx, signalID, 0)
	if err != nil {
		return "", fmt.Errorf("fetch profile of %s: %w", aci, err)
	}

	_, err = cli.FetchExpiringProfileKeyCredentialById(ctx, signalID)
	if err != nil {
		return "", fmt.Errorf("fetch profile key credential of %s: %w", aci, err)
	}

	return profile.Name, nil
}

func connectedCli(c Client) (*signalmeow.Client, error) {
	meow, ok := c.(*meowClient)
	if !ok {
		return nil, fmt.Errorf("%w: %T", errNotMeow, c)
	}

	meow.cliMu.Lock()
	cli := meow.cli
	meow.cliMu.Unlock()

	if cli == nil {
		return nil, ErrNotConnected
	}

	return cli, nil
}

func nilOr(id uuid.UUID) string {
	if id == uuid.Nil {
		return ""
	}

	return id.String()
}
