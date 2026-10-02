package signal

import (
	"bytes"
	"fmt"
	"image"
	_ "image/jpeg" // Register supported encoded avatar formats.
	_ "image/png"  // Register supported encoded avatar formats.
)

// MaxGroupAvatarSize bounds original encoded avatar bytes.
const MaxGroupAvatarSize = 2 << 20

// MaxGroupAvatarDimension bounds both decoded image dimensions.
const MaxGroupAvatarDimension = 2048

// GroupAvatarUpdate sets an encoded PNG or JPEG, or removes the current avatar.
type GroupAvatarUpdate struct {
	Data   []byte
	Remove bool
}

func (avatar GroupAvatarUpdate) check() error {
	if avatar.Remove && len(avatar.Data) == 0 {
		return nil
	}

	if avatar.Remove || len(avatar.Data) == 0 || len(avatar.Data) > MaxGroupAvatarSize {
		return fmt.Errorf("%w: provide PNG or JPEG avatar data within the size limit, or remove it", ErrInvalidGroupUpdate)
	}

	config, format, err := image.DecodeConfig(bytes.NewReader(avatar.Data))
	if err != nil || !validAvatarConfig(config, format) {
		return fmt.Errorf("%w: avatar must be a PNG or JPEG within the dimension limit", ErrInvalidGroupUpdate)
	}

	_, _, err = image.Decode(bytes.NewReader(avatar.Data))
	if err != nil {
		return fmt.Errorf("%w: avatar image is incomplete or invalid", ErrInvalidGroupUpdate)
	}

	return nil
}

func validAvatarConfig(config image.Config, format string) bool {
	return (format == "png" || format == "jpeg") && config.Width > 0 && config.Height > 0 &&
		config.Width <= MaxGroupAvatarDimension && config.Height <= MaxGroupAvatarDimension
}
