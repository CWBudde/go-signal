//go:build cgo || purego

package signal

import (
	"context"

	"go.mau.fi/mautrix-signal/pkg/signalmeow"
	"go.mau.fi/mautrix-signal/pkg/signalmeow/types"
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
