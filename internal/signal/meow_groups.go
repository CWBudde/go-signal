//go:build cgo

package signal

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cwbudde/go-signal/internal/store"
	"github.com/google/uuid"
	"go.mau.fi/mautrix-signal/pkg/libsignalgo"
	"go.mau.fi/mautrix-signal/pkg/signalmeow"
	mstore "go.mau.fi/mautrix-signal/pkg/signalmeow/store"
	"go.mau.fi/mautrix-signal/pkg/signalmeow/types"
)

// groupKeyLen is the length of group IDs and master keys.
const groupKeyLen = 32

func (c *meowClient) Groups(ctx context.Context) ([]Group, error) {
	cli, done, err := c.groupClient("groups")
	if err != nil {
		return nil, err
	}
	defer done()

	ctx = c.zlog.WithContext(ctx)

	ids, err := c.data.GroupIdentifiers(ctx, c.ownACI)
	if err != nil {
		return nil, err //nolint:wrapcheck // store wraps it
	}

	out := make([]Group, 0, len(ids))

	for _, groupID := range ids {
		group, err := c.fetchGroup(ctx, cli, types.GroupIdentifier(groupID))
		if errors.Is(err, ErrNotAMember) || errors.Is(err, ErrUnknownGroup) {
			out = append(out, c.unavailableGroup(ctx, groupID, err))

			continue
		}

		if err != nil {
			return nil, c.lostOr(fmt.Errorf("group %s: %w", groupID, err))
		}

		out = append(out, group)
	}

	SortGroups(out)

	return out, nil
}

func (c *meowClient) Group(ctx context.Context, ref string) (Group, error) {
	cli, done, err := c.groupClient("group")
	if err != nil {
		return Group{}, err
	}
	defer done()

	ctx = c.zlog.WithContext(ctx)

	gid, err := resolveGroupRef(ctx, c.connDevice.GroupStore, ref)
	if err != nil {
		return Group{}, err
	}

	group, err := c.fetchGroup(ctx, cli, gid)
	if err != nil {
		return Group{}, c.lostOr(err)
	}

	return group, nil
}

func (c *meowClient) LeaveGroup(ctx context.Context, ref string, opts LeaveOptions) (LeaveResult, error) {
	cli, done, err := c.groupClient("leave group")
	if err != nil {
		return LeaveResult{}, err
	}
	defer done()

	ctx = c.zlog.WithContext(ctx)

	gid, err := resolveGroupRef(ctx, c.connDevice.GroupStore, ref)
	if err != nil {
		return LeaveResult{}, err
	}

	group, err := c.fetchGroup(ctx, cli, gid)
	if err != nil {
		return LeaveResult{}, c.lostOr(err)
	}

	change, promoted, err := leaveChange(group, c.ownACI, opts.Promote)
	if err != nil {
		return LeaveResult{}, fmt.Errorf("leave group %s: %w", group.ID, err)
	}

	// UpdateGroup patches the group and sends the change to the members; a failure to send it to
	// them is only logged by signalmeow. It doesn't retry a conflict (409): the server's reply
	// has a body, which signalmeow takes for ContactManifestMismatchError (its conflict
	// resolution for a ConflictError would dereference this change's nil ModifyTitle anyway).
	revision, err := cli.UpdateGroup(ctx, change, gid)
	if err != nil {
		return LeaveResult{}, c.lostOr(fmt.Errorf("leave group %s: %w", group.ID, updateGroupError(err)))
	}

	group.LeftAt = time.Now().UTC().Truncate(time.Second)

	cached := group
	cached.Revision = revision
	c.cacheGroup(ctx, cached)

	return LeaveResult{Group: group, Revision: revision, Promoted: promoted}, nil
}

// updateGroupError maps a failure of UpdateGroup: a conflict becomes ErrGroupChanged.
func updateGroupError(err error) error {
	if errors.Is(err, signalmeow.ContactManifestMismatchError) || errors.Is(err, signalmeow.ConflictError) {
		return fmt.Errorf("%w (%w)", ErrGroupChanged, err)
	}

	return err
}

func (c *meowClient) GroupTitles(ctx context.Context) (map[string]CachedGroup, error) {
	// Close waits for it like for a send, so that the store stays open.
	if !c.begin(&c.sending) {
		return nil, ErrClosed
	}
	defer c.sending.Done()

	_, err := c.storeDevice(ctx)
	if err != nil {
		return nil, err
	}

	recs, err := c.data.Groups(ctx)
	if err != nil {
		return nil, err //nolint:wrapcheck // store wraps it
	}

	titles := make(map[string]CachedGroup, len(recs))

	for _, rec := range recs {
		titles[rec.ID] = CachedGroup{Title: rec.Title, LeftAt: rec.LeftAt}
	}

	return titles, nil
}

// groupClient checks that a group operation can run (connected, not closed, connection not
// lost) and returns the signalmeow client with the function that ends the operation.
func (c *meowClient) groupClient(action string) (*signalmeow.Client, func(), error) {
	if c.cancelLoops == nil {
		return nil, nil, ErrNotConnected
	}

	if !c.begin(&c.sending) {
		return nil, nil, ErrClosed
	}

	err := c.connectionLost()
	if err != nil {
		c.sending.Done()

		return nil, nil, fmt.Errorf("%s: %w", action, err)
	}

	c.cliMu.Lock()
	cli := c.cli
	c.cliMu.Unlock()

	return cli, c.sending.Done, nil
}

// lostOr returns the error of a connection lost for good (e.g. ErrDeviceUnlinked), which is
// the likely cause of err, or else err.
func (c *meowClient) lostOr(err error) error {
	lost := c.connectionLost()
	if lost != nil {
		return fmt.Errorf("%w (%w)", lost, err)
	}

	return err
}

// resolveGroupRef resolves ref, a group ID or master key in standard base64, to the ID of a group
// whose master key groups holds.
func resolveGroupRef(ctx context.Context, groups mstore.GroupStore, ref string) (types.GroupIdentifier, error) {
	raw, err := base64.StdEncoding.DecodeString(ref)
	if err != nil || len(raw) != groupKeyLen {
		return "", fmt.Errorf("%w %q: want a base64 group ID or master key of %d bytes", ErrUnknownGroup, ref, groupKeyLen)
	}

	known, err := knownGroup(ctx, groups, types.GroupIdentifier(ref))
	if known || err != nil {
		return types.GroupIdentifier(ref), err
	}

	derived, err := groupIDFromMasterKey(raw)
	if err != nil {
		return "", err
	}

	known, err = knownGroup(ctx, groups, derived)
	if known || err != nil {
		return derived, err
	}

	return "", fmt.Errorf("%w %s: no group with this ID or master key is known; groups become known "+
		"through a sync or a message from the group", ErrUnknownGroup, ref)
}

// knownGroup reports whether the store holds the master key of the group gid.
func knownGroup(ctx context.Context, groups mstore.GroupStore, gid types.GroupIdentifier) (bool, error) {
	key, err := groups.MasterKeyFromGroupIdentifier(ctx, gid)
	if err != nil {
		return false, fmt.Errorf("load group %s: %w", gid, err)
	}

	return key != "", nil
}

// groupIDFromMasterKey derives the group ID from a raw master key.
func groupIDFromMasterKey(masterKey []byte) (types.GroupIdentifier, error) {
	if len(masterKey) != groupKeyLen {
		return "", fmt.Errorf("%w: a master key has %d bytes, not %d", ErrUnknownGroup, groupKeyLen, len(masterKey))
	}

	id, err := libsignalgo.GroupMasterKey(masterKey).GroupIdentifier()
	if err != nil {
		return "", fmt.Errorf("derive group ID: %w", err)
	}

	return types.BytesToGroupIdentifier(id), nil
}

// fetchGroup fetches the group gid from the server (or signalmeow's in-memory cache), converts
// it and updates the title cache.
func (c *meowClient) fetchGroup(ctx context.Context, cli *signalmeow.Client, gid types.GroupIdentifier) (Group, error) {
	raw, _, err := cli.RetrieveGroupByID(ctx, gid, 0)
	if err != nil {
		return Group{}, groupFetchError(gid, err)
	}

	group := convertGroup(raw, c.ownACI)
	c.cacheGroup(ctx, group)

	return group, nil
}

// groupFetchError maps a failure of RetrieveGroupByID for the group gid to our errors. signalmeow
// reports the server's status only in the error text.
func groupFetchError(gid types.GroupIdentifier, err error) error {
	switch {
	case errors.Is(err, signalmeow.ErrGroupMasterKeyNotFound):
		return fmt.Errorf("%w %s: its master key is not known", ErrUnknownGroup, gid)
	case strings.Contains(err.Error(), "unexpected response status: 403"):
		return fmt.Errorf("%w %s: the server refused to show it (we left, were removed or were never "+
			"approved)", ErrNotAMember, gid)
	case strings.Contains(err.Error(), "unexpected response status: 404"):
		return fmt.Errorf("%w %s: the server doesn't know it", ErrUnknownGroup, gid)
	default:
		return fmt.Errorf("fetch group %s: %w", gid, err)
	}
}

// cacheGroup records group's title for GroupTitles and whether we left it; an empty title keeps
// the one known before (see store.PutGroup). A failure is only logged: the cache is a
// convenience.
func (c *meowClient) cacheGroup(ctx context.Context, group Group) {
	err := c.data.PutGroup(ctx, store.GroupRecord{
		ID:        group.ID,
		Title:     group.Title,
		Revision:  group.Revision,
		LeftAt:    group.LeftAt,
		UpdatedAt: time.Now(),
	})
	if err != nil {
		c.log.Warn("cache group title", "group", group.ID, "error", err)
	}
}

// unavailableGroup is the entry Groups lists for the group id that couldn't be fetched because
// of err, with what the title cache knows about it.
func (c *meowClient) unavailableGroup(ctx context.Context, id string, err error) Group {
	group := Group{ID: id, Err: err}

	rec, ok, cacheErr := c.data.Group(ctx, id)
	if cacheErr != nil {
		c.log.Warn("read group title", "group", id, "error", cacheErr)
	}

	if ok {
		group.Title, group.LeftAt = rec.Title, rec.LeftAt
	}

	return group
}

// convertGroup converts a signalmeow group; ownACI decides Membership and Role.
func convertGroup(raw *signalmeow.Group, ownACI string) Group {
	group := Group{
		ID:                string(raw.GroupIdentifier),
		MasterKey:         string(raw.GroupMasterKey),
		Title:             raw.Title,
		Description:       raw.Description,
		Revision:          raw.Revision,
		Timer:             time.Duration(raw.DisappearingMessagesDuration) * time.Second,
		AnnouncementsOnly: raw.AnnouncementsOnly,
	}

	for _, member := range raw.Members {
		if member == nil {
			continue
		}

		group.Members = append(group.Members, GroupMember{
			Recipient:        Recipient{ACI: member.ACI.String()},
			Role:             convertRole(member.Role),
			JoinedAtRevision: member.JoinedAtRevision,
		})
	}

	for _, pending := range raw.PendingMembers {
		if pending == nil {
			continue
		}

		out := PendingMember{
			Recipient: serviceRecipient(pending.ServiceID),
			Role:      convertRole(pending.Role),
			InvitedAt: msTime(pending.Timestamp),
		}

		if pending.AddedByUserID != uuid.Nil {
			out.AddedBy = Recipient{ACI: pending.AddedByUserID.String()}
		}

		group.Pending = append(group.Pending, out)
	}

	for _, requesting := range raw.RequestingMembers {
		if requesting == nil {
			continue
		}

		group.Requesting = append(group.Requesting, RequestingMember{
			Recipient:   Recipient{ACI: requesting.ACI.String()},
			RequestedAt: msTime(requesting.Timestamp),
		})
	}

	group.Membership, group.Role = group.MembershipOf(ownACI)

	return group
}

func convertRole(role signalmeow.GroupMemberRole) GroupRole {
	switch role {
	case signalmeow.GroupMember_ADMINISTRATOR:
		return GroupRoleAdmin
	case signalmeow.GroupMember_DEFAULT:
		return GroupRoleMember
	case signalmeow.GroupMember_UNKNOWN:
	}

	return GroupRoleUnknown
}

// msTime converts ms since the epoch to a time; zero stays the zero time.
func msTime(ms uint64) time.Time {
	if ms == 0 {
		return time.Time{}
	}

	return time.UnixMilli(int64(ms)).UTC() //nolint:gosec // timestamps fit
}

// leaveChange builds the group change with which self leaves group, promoting the members
// promote to admin first (see Group.CheckLeave): a member deletes itself, an invited user
// deletes its invitation, a requesting user its request. It also returns the members that get
// promoted, without those that already are admins.
func leaveChange(group Group, self string, promote []Recipient) (*signalmeow.GroupChange, []Recipient, error) {
	err := group.CheckLeave(self, promote)
	if err != nil {
		return nil, nil, err
	}

	selfACI, err := uuid.Parse(self)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: invalid own ACI %q: %w", ErrUnresolvable, self, err)
	}

	membership, _ := group.MembershipOf(self)

	switch membership {
	case MembershipPending:
		serviceID := libsignalgo.NewACIServiceID(selfACI)

		return &signalmeow.GroupChange{DeletePendingMembers: []*libsignalgo.ServiceID{&serviceID}}, nil, nil
	case MembershipRequesting:
		return &signalmeow.GroupChange{DeleteRequestingMembers: []*uuid.UUID{&selfACI}}, nil, nil
	case MembershipMember, MembershipNone:
	}

	change := &signalmeow.GroupChange{DeleteMembers: []*uuid.UUID{&selfACI}}

	var promoted []Recipient

	for _, member := range group.Members {
		if member.Role == GroupRoleAdmin || !containsACI(promote, member.Recipient.ACI) {
			continue
		}

		aci, err := uuid.Parse(member.Recipient.ACI)
		if err != nil {
			return nil, nil, fmt.Errorf("%w: invalid ACI %q: %w", ErrInvalidPromotion, member.Recipient.ACI, err)
		}

		change.ModifyMemberRoles = append(change.ModifyMemberRoles,
			&signalmeow.RoleMember{ACI: aci, Role: signalmeow.GroupMember_ADMINISTRATOR})
		promoted = append(promoted, member.Recipient)
	}

	return change, promoted, nil
}

func containsACI(recipients []Recipient, aci string) bool {
	for _, rcpt := range recipients {
		if rcpt.ACI == aci {
			return true
		}
	}

	return false
}
