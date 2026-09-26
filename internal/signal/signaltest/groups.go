package signaltest

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/cwbudde/go-signal/internal/signal"
)

// LeaveCall records a successful LeaveGroup.
type LeaveCall struct {
	// ACI is the account that left.
	ACI     string
	GroupID string
	Promote []signal.Recipient
}

// Leaves returns every successful LeaveGroup, in order.
func (f *Fake) Leaves() []LeaveCall {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([]LeaveCall(nil), f.leaves...)
}

func (c *client) Groups(context.Context) ([]signal.Group, error) {
	c.fake.mu.Lock()
	defer c.fake.mu.Unlock()

	err := c.checkGroupOp("groups")
	if err != nil {
		return nil, err
	}

	groupIDs := slices.Sorted(maps.Keys(c.fake.GroupInfo))
	for failing := range c.fake.GroupErrs {
		if _, ok := c.fake.GroupInfo[failing]; !ok {
			groupIDs = append(groupIDs, failing)
		}
	}

	out := make([]signal.Group, 0, len(groupIDs))

	for _, groupID := range groupIDs {
		group, err := c.fetch(groupID)
		if errors.Is(err, signal.ErrNotAMember) || errors.Is(err, signal.ErrUnknownGroup) {
			// Like the real client: what the title cache knows.
			cached := c.fake.GroupTitleCache[groupID]
			out = append(out, signal.Group{ID: groupID, Title: cached.Title, LeftAt: cached.LeftAt, Err: err})

			continue
		}

		if err != nil {
			return nil, fmt.Errorf("group %s: %w", groupID, err)
		}

		out = append(out, group)
	}

	signal.SortGroups(out)

	return out, nil
}

func (c *client) Group(_ context.Context, ref string) (signal.Group, error) {
	c.fake.mu.Lock()
	defer c.fake.mu.Unlock()

	err := c.checkGroupOp("group")
	if err != nil {
		return signal.Group{}, err
	}

	groupID, err := c.groupID(ref)
	if err != nil {
		return signal.Group{}, err
	}

	return c.fetch(groupID)
}

func (c *client) LeaveGroup(_ context.Context, ref string, opts signal.LeaveOptions) (signal.LeaveResult, error) {
	c.fake.mu.Lock()
	defer c.fake.mu.Unlock()

	group, err := c.leavable(ref, opts)
	if err != nil {
		return signal.LeaveResult{}, err
	}

	group.LeftAt = c.fake.LeaveTime
	if group.LeftAt.IsZero() {
		group.LeftAt = time.Now().UTC().Truncate(time.Second)
	}

	if c.fake.left == nil {
		c.fake.left = make(map[string]time.Time)
	}

	c.fake.left[group.ID] = group.LeftAt
	c.fake.cacheGroup(group)
	c.fake.leaves = append(c.fake.leaves, LeaveCall{
		ACI: c.connected, GroupID: group.ID, Promote: slices.Clone(opts.Promote),
	})

	return signal.LeaveResult{
		Group: group, Revision: group.Revision + 1, Promoted: newAdmins(group, opts.Promote),
	}, nil
}

func (c *client) GroupTitles(context.Context) (map[string]signal.CachedGroup, error) {
	c.fake.mu.Lock()
	defer c.fake.mu.Unlock()

	if c.closed {
		return nil, signal.ErrClosed
	}

	_, err := c.fake.account(c.opts)
	if err != nil {
		return nil, err
	}

	if c.fake.GroupTitlesErr != nil {
		return nil, c.fake.GroupTitlesErr
	}

	return maps.Clone(c.fake.GroupTitleCache), nil
}

// cacheGroup records a fetched group in the title cache like the real client: an empty title
// keeps the cached one, and LeftAt is replaced. The caller holds f.mu.
func (f *Fake) cacheGroup(group signal.Group) {
	if f.GroupTitleCache == nil {
		f.GroupTitleCache = make(map[string]signal.CachedGroup)
	}

	cached := signal.CachedGroup{Title: group.Title, LeftAt: group.LeftAt}
	if cached.Title == "" {
		cached.Title = f.GroupTitleCache[group.ID].Title
	}

	f.GroupTitleCache[group.ID] = cached
}

// leavable returns the group ref if the connected account can leave it with opts, like the
// real client checks; the caller holds c.fake.mu.
func (c *client) leavable(ref string, opts signal.LeaveOptions) (signal.Group, error) {
	err := c.checkGroupOp("leave group")
	if err != nil {
		return signal.Group{}, err
	}

	groupID, err := c.groupID(ref)
	if err != nil {
		return signal.Group{}, err
	}

	group, err := c.fetch(groupID)
	if err != nil {
		return signal.Group{}, err
	}

	err = group.CheckLeave(c.connected, opts.Promote)
	if err != nil {
		return signal.Group{}, fmt.Errorf("leave group %s: %w (fake)", groupID, err)
	}

	if c.fake.LeaveErr != nil {
		return signal.Group{}, c.fake.LeaveErr
	}

	return group, nil
}

// newAdmins returns the members of group in promote that aren't admins yet.
func newAdmins(group signal.Group, promote []signal.Recipient) []signal.Recipient {
	var out []signal.Recipient

	for _, member := range group.Members {
		named := slices.ContainsFunc(promote, func(r signal.Recipient) bool { return r.ACI == member.Recipient.ACI })
		if named && member.Role != signal.GroupRoleAdmin {
			out = append(out, member.Recipient)
		}
	}

	return out
}

// checkGroupOp fails like the real client for a group operation it can't run; the caller holds
// c.fake.mu.
func (c *client) checkGroupOp(action string) error {
	switch {
	case c.closed:
		return signal.ErrClosed
	case c.connected == "":
		return signal.ErrNotConnected
	case c.lost != nil:
		return fmt.Errorf("%s: %w", action, c.lost)
	}

	return nil
}

// groupID resolves ref, a group ID or master key, like the real client; the caller holds
// c.fake.mu.
func (c *client) groupID(ref string) (string, error) {
	if c.fake.knows(ref) {
		return ref, nil
	}

	groupID, ok := c.fake.GroupKeys[ref]
	if ok && c.fake.knows(groupID) {
		return groupID, nil
	}

	return "", fmt.Errorf("%w %s: no group with this ID or master key is known (fake)", signal.ErrUnknownGroup, ref)
}

// fetch returns the group groupID as the server shows it to the connected account; the caller
// holds c.fake.mu.
func (c *client) fetch(groupID string) (signal.Group, error) {
	err := c.fake.GroupErrs[groupID]
	if err != nil {
		return signal.Group{}, fmt.Errorf("fetch group %s: %w", groupID, err)
	}

	if _, left := c.fake.left[groupID]; left {
		return signal.Group{}, fmt.Errorf("%w %s: we left it (fake)", signal.ErrNotAMember, groupID)
	}

	group, ok := c.fake.GroupInfo[groupID]
	if !ok {
		return signal.Group{}, fmt.Errorf("%w %s (fake)", signal.ErrUnknownGroup, groupID)
	}

	group.ID = groupID
	group.Membership, group.Role = group.MembershipOf(c.connected)
	group.Members = slices.Clone(group.Members)
	group.Pending = slices.Clone(group.Pending)
	group.Requesting = slices.Clone(group.Requesting)
	c.fake.cacheGroup(group)

	return group, nil
}

// knows reports whether the group groupID is on the "server"; the caller holds f.mu.
func (f *Fake) knows(groupID string) bool {
	_, info := f.GroupInfo[groupID]
	_, failing := f.GroupErrs[groupID]

	return info || failing
}

// groupMembers returns the members of the group groupID in GroupInfo, for Send; the caller
// holds f.mu.
func (f *Fake) groupMembers(groupID string) ([]signal.Recipient, bool) {
	group, ok := f.GroupInfo[groupID]
	if !ok {
		return nil, false
	}

	members := make([]signal.Recipient, 0, len(group.Members))
	for _, member := range group.Members {
		members = append(members, member.Recipient)
	}

	return members, true
}

// CachedTitles returns the title cache the real client would have after listing groups, for
// Fake.GroupTitleCache.
func CachedTitles(groups map[string]signal.Group) map[string]signal.CachedGroup {
	out := make(map[string]signal.CachedGroup, len(groups))

	for groupID, group := range groups {
		out[groupID] = signal.CachedGroup{Title: group.Title, LeftAt: group.LeftAt}
	}

	return out
}
