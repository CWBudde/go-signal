//go:build cgo || libsignal_go

package signal

import (
	"context"
	"encoding/base64"
	"fmt"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/cwbudde/mautrix-signal/pkg/signalmeow"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow/types"
)

type groupAvatarSender interface {
	groupAdditionSender
	UploadGroupAvatar(ctx context.Context, data []byte, gid types.GroupIdentifier,
		key types.SerializedGroupMasterKey) (string, error)
}

// updateGroupWithAvatarOnce validates the complete change before uploading any bytes.
// The uploaded ciphertext may remain unused if the single subsequent PATCH is rejected.
func updateGroupWithAvatarOnce(ctx context.Context, cli groupAvatarSender, raw *signalmeow.Group, self string,
	update GroupUpdate, invalidate func(),
) (*signalmeow.Group, error) {
	change, err := settingsChange(raw, self, update)
	if err != nil {
		return nil, err
	}

	if change == nil {
		return raw, nil
	}

	if update.Avatar != nil && !update.Avatar.Remove {
		err = uploadAvatarChange(ctx, cli, raw, change, update.Avatar.Data)
		if err != nil {
			return nil, err
		}
	}

	return updateGroupSettingsOnce(ctx, cli, raw, change, invalidate)
}

func uploadAvatarChange(ctx context.Context, cli groupAvatarSender, raw *signalmeow.Group,
	change *signalmeow.GroupChange, data []byte,
) error {
	key, err := base64.StdEncoding.DecodeString(string(raw.GroupMasterKey))
	if err != nil || len(key) != 32 {
		return fmt.Errorf("%w: invalid group master key", ErrUnknownGroup)
	}

	err = ctx.Err()
	if err != nil {
		return fmt.Errorf("upload group avatar: %w", err)
	}

	path, err := cli.UploadGroupAvatar(ctx, slices.Clone(data), raw.GroupIdentifier, raw.GroupMasterKey)
	if err != nil {
		return fmt.Errorf("upload group avatar: %w", err)
	}

	if !utf8.ValidString(path) || strings.TrimSpace(path) == "" || strings.ContainsFunc(path, unicode.IsControl) {
		return fmt.Errorf("%w: avatar upload returned an invalid path", ErrInvalidGroupUpdate)
	}

	change.ModifyAvatar = new(path)

	return nil
}
