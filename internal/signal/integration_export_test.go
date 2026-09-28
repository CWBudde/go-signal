//go:build integration && (cgo || libsignal_go)

package signal

import (
	"context"
	"errors"
	"fmt"

	"github.com/cwbudde/mautrix-signal/pkg/signalmeow"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow/types"
	"github.com/google/uuid"
)

var errNotMeow = errors.New("not a signalmeow client")

// FreshIntegrationGroup bypasses the in-memory group cache to verify server state.
func FreshIntegrationGroup(ctx context.Context, client Client, groupID string) (Group, error) {
	cli, err := connectedCli(client)
	if err != nil {
		return Group{}, err
	}

	cli.GroupCache.Delete(types.GroupIdentifier(groupID))

	group, err := client.Group(ctx, groupID)
	if err != nil {
		return Group{}, fmt.Errorf("fetch fresh integration group: %w", err)
	}

	return group, nil
}

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

// CreateIntegrationGroup creates a two-member test group. Once prepared, its ID is returned
// even on failure so callers can inspect a partially completed creation before retrying.
func CreateIntegrationGroup(ctx context.Context, c Client, peerACI string) (string, error) {
	cli, err := connectedCli(c)
	if err != nil {
		return "", err
	}

	peer, err := uuid.Parse(peerACI)
	if err != nil {
		return "", fmt.Errorf("parse peer ACI: %w", err)
	}

	if peer == uuid.Nil || peer == cli.Store.ACI {
		return "", ErrUnresolvable
	}

	for _, id := range []uuid.UUID{cli.Store.ACI, peer} {
		_, err = cli.FetchExpiringProfileKeyCredentialById(ctx, id)
		if err != nil {
			return "", fmt.Errorf("fetch group member credential %s: %w", id, err)
		}
	}

	group := &signalmeow.Group{
		Title: "go-signal integration test",
		Members: []*signalmeow.GroupMember{
			{ACI: cli.Store.ACI, Role: signalmeow.GroupMember_ADMINISTRATOR},
			{ACI: peer, Role: signalmeow.GroupMember_DEFAULT},
		},
		AccessControl: &signalmeow.GroupAccessControl{
			Members:           signalmeow.AccessControl_MEMBER,
			Attributes:        signalmeow.AccessControl_MEMBER,
			AddFromInviteLink: signalmeow.AccessControl_UNSATISFIABLE,
		},
	}

	_, err = signalmeow.PrepareGroupCreation(group)
	if err != nil {
		return "", fmt.Errorf("prepare test group: %w", err)
	}

	groupID := string(group.GroupIdentifier)

	_, err = cli.CreateGroup(ctx, group)
	if err != nil {
		return groupID, fmt.Errorf("create test group %s (inspect before retrying): %w", groupID, err)
	}

	return groupID, nil
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
