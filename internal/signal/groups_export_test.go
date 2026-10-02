//go:build cgo || libsignal_go

package signal

import (
	"context"
	"time"

	"github.com/cwbudde/mautrix-signal/pkg/signalmeow"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow/types"
	"github.com/google/uuid"
)

// ConvertGroup exposes convertGroup to the signal_test package.
func ConvertGroup(raw *signalmeow.Group, ownACI string) Group {
	return convertGroup(raw, ownACI)
}

// GroupIDFromMasterKey exposes groupIDFromMasterKey to the signal_test package.
func GroupIDFromMasterKey(masterKey []byte) (string, error) {
	id, err := groupIDFromMasterKey(masterKey)

	return string(id), err
}

// GroupFetchError exposes groupFetchError to the signal_test package.
func GroupFetchError(gid string, err error) error {
	return groupFetchError(types.GroupIdentifier(gid), err)
}

// UpdateGroupError exposes updateGroupError to the signal_test package.
func UpdateGroupError(err error) error {
	return updateGroupError(err)
}

// LeaveChange exposes leaveChange to the signal_test package.
func LeaveChange(group Group, self string, promote []Recipient) (*signalmeow.GroupChange, []Recipient, error) {
	return leaveChange(group, self, promote)
}

// ResolveGroupRef resolves ref against the group store of the selected account of client, which
// must come from Open, without connecting (see Client.Group).
func ResolveGroupRef(ctx context.Context, client Client, ref string) (string, error) {
	meow, ok := client.(*meowClient)
	if !ok {
		panic("ResolveGroupRef: not a signalmeow-backed client")
	}

	device, err := meow.device(ctx)
	if err != nil {
		return "", err
	}

	id, err := resolveGroupRef(ctx, device.GroupStore, ref)

	return string(id), err
}

// RenameChange exposes renameChange to the signal_test package.
func RenameChange(group Group, self, title string) (*signalmeow.GroupChange, error) {
	return renameChange(group, self, title)
}

// NewGroup exposes the group creation defaults to signal_test.
func NewGroup(opts CreateGroupOptions, self uuid.UUID) *signalmeow.Group {
	return newGroup(opts, self)
}

// RemoveMembersChange exposes the removal change builder.
func RemoveMembersChange(group Group, self string, members []Recipient) (*signalmeow.GroupChange, Group, error) {
	return removeMembersChange(group, self, members)
}

// GroupRemovalSender is the transport used by RemoveGroupMembersOnce.
type GroupRemovalSender = groupRemovalSender

// RemoveGroupMembersOnce exposes the single-attempt group update transport.
func RemoveGroupMembersOnce(ctx context.Context, cli GroupRemovalSender,
	raw *signalmeow.Group, change *signalmeow.GroupChange, invalidate func(),
) error {
	return removeGroupMembersOnce(ctx, cli, raw, change, invalidate)
}

// AddMembersChange exposes the addition change builder.
func AddMembersChange(raw *signalmeow.Group, self string, members []Recipient) (*signalmeow.GroupChange, error) {
	return addMembersChange(raw, self, members)
}

// GroupAdditionSender is the transport used by AddGroupMembersOnce.
type GroupAdditionSender = groupAdditionSender

// GroupAvatarSender is the transport used by UpdateGroupWithAvatarOnce.
type GroupAvatarSender = groupAvatarSender

// UpdateGroupWithAvatarOnce exposes avatar preflight and single-attempt transport.
func UpdateGroupWithAvatarOnce(ctx context.Context, cli GroupAvatarSender, raw *signalmeow.Group, self string,
	update GroupUpdate, invalidate func(),
) (*signalmeow.Group, error) {
	return updateGroupWithAvatarOnce(ctx, cli, raw, self, update, invalidate)
}

// AddGroupMembersOnce exposes the single-attempt addition and authoritative refetch.
func AddGroupMembersOnce(ctx context.Context, cli GroupAdditionSender,
	raw *signalmeow.Group, change *signalmeow.GroupChange, invalidate func(),
) (*signalmeow.Group, error) {
	return addGroupMembersOnce(ctx, cli, raw, change, invalidate)
}

// InstallGroupClient gives an offline client a real signalmeow client for group retrieval.
// Restore it before closing the offline client, which has no receive loops to stop.
func InstallGroupClient(client Client, cli *signalmeow.Client) func() {
	meow, ok := client.(*meowClient)
	if !ok {
		panic("InstallGroupClient: not a signalmeow-backed client")
	}

	meow.cliMu.Lock()
	previous := meow.cli
	meow.cli = cli
	meow.cliMu.Unlock()

	return func() {
		meow.cliMu.Lock()
		meow.cli = previous
		meow.cliMu.Unlock()
	}
}

// SettingsChange exposes the settings change builder.
func SettingsChange(raw *signalmeow.Group, self string, update GroupUpdate) (*signalmeow.GroupChange, error) {
	return settingsChange(raw, self, update)
}

// UpdateGroupSettingsOnce exposes the single-attempt settings transport.
func UpdateGroupSettingsOnce(ctx context.Context, cli GroupAdditionSender,
	raw *signalmeow.Group, change *signalmeow.GroupChange, invalidate func(),
) (*signalmeow.Group, error) {
	return updateGroupSettingsOnce(ctx, cli, raw, change, invalidate)
}

// MemberRoleChange exposes the role change builder.
func MemberRoleChange(raw *signalmeow.Group, self string, members []Recipient,
	role GroupRole,
) (*signalmeow.GroupChange, error) {
	return memberRoleChange(raw, self, members, role)
}

// SetGroupMemberRoleOnce exposes the single-attempt role transport and authoritative refetch.
func SetGroupMemberRoleOnce(ctx context.Context, cli GroupAdditionSender,
	raw *signalmeow.Group, change *signalmeow.GroupChange, invalidate func(),
) (*signalmeow.Group, error) {
	return setGroupMemberRoleOnce(ctx, cli, raw, change, invalidate)
}

// BannedMembersChange exposes the ban change builder.
func BannedMembersChange(raw *signalmeow.Group, self string, members []Recipient, banned bool, at time.Time,
) (*signalmeow.GroupChange, error) {
	return bannedMembersChange(raw, self, members, banned, at)
}

// SetGroupBannedOnce exposes the single-attempt ban transport.
func SetGroupBannedOnce(ctx context.Context, cli GroupAdditionSender, raw *signalmeow.Group,
	change *signalmeow.GroupChange, invalidate func(),
) (*signalmeow.Group, error) {
	return setGroupBannedOnce(ctx, cli, raw, change, invalidate)
}

// GroupLinkChange exposes the invite-link patch builder.
func GroupLinkChange(raw *signalmeow.Group, self string, update GroupLinkUpdate) (*signalmeow.GroupChange, error) {
	return groupLinkChange(raw, self, update)
}

// ConvertGroupLink exposes dedicated link conversion.
func ConvertGroupLink(raw *signalmeow.Group, self string) (GroupLink, error) {
	return convertGroupLink(raw, self)
}

// UpdateGroupLinkOnce exposes single-attempt link transport.
func UpdateGroupLinkOnce(ctx context.Context, cli GroupAdditionSender, raw *signalmeow.Group,
	change *signalmeow.GroupChange, invalidate func(),
) (*signalmeow.Group, error) {
	return updateGroupLinkOnce(ctx, cli, raw, change, invalidate)
}

// VerifiedGroupLink exposes accepted link verification and partial-result handling.
func VerifiedGroupLink(raw *signalmeow.Group, self string, committedRevision uint32) (GroupLink, error) {
	return verifiedGroupLink(raw, self, committedRevision)
}
