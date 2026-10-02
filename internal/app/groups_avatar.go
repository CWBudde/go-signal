package app

import (
	"fmt"
	"io"
	"slices"

	"github.com/cwbudde/go-signal/internal/signal"
)

// Prepare validates the update, loads a bounded local avatar file, and returns a request
// owning its avatar bytes. The result has no local path and can be used after the file changes.
// Existing programmatic Update.Avatar operations are supported, but cannot be combined with
// AvatarFile or RemoveAvatar.
func (r UpdateGroupRequest) Prepare() (UpdateGroupRequest, error) {
	err := r.checkGroup()
	if err != nil {
		return UpdateGroupRequest{}, err
	}

	avatar, err := r.prepareAvatar()
	if err != nil {
		return UpdateGroupRequest{}, err
	}

	r.Update.Avatar = avatar

	err = r.Update.Check()
	if err != nil {
		return UpdateGroupRequest{}, err //nolint:wrapcheck // self-contained facade validation
	}

	r.AvatarFile = nil
	r.RemoveAvatar = false

	return r, nil
}

func (r UpdateGroupRequest) prepareAvatar() (*signal.GroupAvatarUpdate, error) {
	if r.Update.Avatar != nil {
		if r.AvatarFile != nil || r.RemoveAvatar {
			return nil, fmt.Errorf("%w: local and programmatic avatar operations are mutually exclusive",
				signal.ErrInvalidGroupUpdate)
		}

		avatar := *r.Update.Avatar
		avatar.Data = slices.Clone(avatar.Data)

		return &avatar, nil
	}

	if r.AvatarFile != nil {
		if r.RemoveAvatar {
			return nil, fmt.Errorf("%w: avatar file and removal are mutually exclusive", signal.ErrInvalidGroupUpdate)
		}

		if *r.AvatarFile == "" {
			return nil, fmt.Errorf("%w: avatar file path is empty", signal.ErrInvalidGroupUpdate)
		}

		data, err := readGroupAvatar(*r.AvatarFile)
		if err != nil {
			return nil, err
		}

		return &signal.GroupAvatarUpdate{Data: data}, nil
	}

	if r.RemoveAvatar {
		return &signal.GroupAvatarUpdate{Remove: true}, nil
	}

	return nil, nil //nolint:nilnil // nil preserves the current avatar
}

func readGroupAvatar(path string) ([]byte, error) {
	files := osFiles{}

	info, err := files.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("group avatar: %w", err)
	}

	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("group avatar: %w", ErrNotAFile)
	}

	if info.Size() > signal.MaxGroupAvatarSize {
		return nil, fmt.Errorf("%w: avatar exceeds %d bytes", signal.ErrInvalidGroupUpdate, signal.MaxGroupAvatarSize)
	}

	file, err := files.Open(path)
	if err != nil {
		return nil, fmt.Errorf("group avatar: %w", err)
	}
	defer file.Close()

	info, err = file.Stat()
	if err != nil {
		return nil, fmt.Errorf("group avatar: %w", err)
	}

	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("group avatar: %w", ErrNotAFile)
	}

	data, err := io.ReadAll(io.LimitReader(file, signal.MaxGroupAvatarSize+1))
	if err != nil {
		return nil, fmt.Errorf("group avatar: %w", err)
	}

	if len(data) > signal.MaxGroupAvatarSize {
		return nil, fmt.Errorf("%w: avatar exceeds %d bytes", signal.ErrInvalidGroupUpdate, signal.MaxGroupAvatarSize)
	}

	return data, nil
}
