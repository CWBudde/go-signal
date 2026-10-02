package app_test

import (
	"bytes"
	"errors"
	"image"
	"image/jpeg"
	"image/png"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/signal"
)

func avatarImage(t *testing.T, width int, jpegImage bool) []byte {
	t.Helper()

	var data bytes.Buffer

	var err error
	if jpegImage {
		err = jpeg.Encode(&data, image.NewRGBA(image.Rect(0, 0, width, 1)), nil)
	} else {
		err = png.Encode(&data, image.NewRGBA(image.Rect(0, 0, width, 1)))
	}

	if err != nil {
		t.Fatal(err)
	}

	return data.Bytes()
}

func avatarFile(t *testing.T, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "avatar.image")

	err := os.WriteFile(path, data, 0o600)
	if err != nil {
		t.Fatal(err)
	}

	return path
}

// Catches rereading a file after CLI preparation or borrowing caller-owned image bytes.
func TestGroupAvatarPreparationOwnsFileBytes(t *testing.T) { //nolint:cyclop // both image formats and removed source
	t.Parallel()

	for _, jpegImage := range []bool{false, true} {
		data := avatarImage(t, 1, jpegImage)
		path := avatarFile(t, data)
		req := app.UpdateGroupRequest{Group: familyTitle, AvatarFile: new(path)}

		prepared, err := req.Prepare()
		if err != nil {
			t.Fatal(err)
		}

		if prepared.AvatarFile != nil || prepared.RemoveAvatar || prepared.Update.Avatar == nil ||
			!bytes.Equal(prepared.Update.Avatar.Data, data) || req.Update.Avatar != nil {
			t.Fatalf("prepared request %+v", prepared)
		}

		err = os.Remove(path)
		if err != nil {
			t.Fatal(err)
		}

		got, err := open(t, groupsFake()).GroupsUpdate(t.Context(), prepared)
		if err != nil || got.AvatarPath == "" || got.Revision != 5 {
			t.Fatalf("update after removal = %+v, %v", got, err)
		}
	}
}

func TestGroupAvatarPreparationOwnsProgrammaticBytes(t *testing.T) {
	t.Parallel()

	data := avatarImage(t, 1, false)
	req := app.UpdateGroupRequest{Group: familyTitle, Update: signal.GroupUpdate{
		Avatar: &signal.GroupAvatarUpdate{Data: data},
	}}

	prepared, err := req.Prepare()
	if err != nil {
		t.Fatal(err)
	}

	data[0] = 0

	if prepared.Update.Avatar.Data[0] != 0x89 || prepared.Update.Avatar == req.Update.Avatar {
		t.Fatal("prepared request borrowed caller image bytes")
	}
}

// Catches invalid files reaching title resolution, Connect or a combined settings patch.
func TestGroupAvatarPreflightBeforeConnect(t *testing.T) {
	t.Parallel()

	valid := avatarImage(t, 1, false)
	for _, test := range []struct {
		name string
		path string
		want error
	}{
		{"empty avatar file", "", signal.ErrInvalidGroupUpdate},
		{"missing avatar file", filepath.Join(t.TempDir(), "missing-avatar"), fs.ErrNotExist},
		{"directory avatar", t.TempDir(), app.ErrNotAFile},
		{"oversized avatar", avatarFile(t, make([]byte, signal.MaxGroupAvatarSize+1)), signal.ErrInvalidGroupUpdate},
		{"invalid avatar", avatarFile(t, []byte("invalid avatar contents")), signal.ErrInvalidGroupUpdate},
		{"truncated avatar", avatarFile(t, valid[:33]), signal.ErrInvalidGroupUpdate},
		{
			"large dimensions", avatarFile(t, avatarImage(t, signal.MaxGroupAvatarDimension+1, false)),
			signal.ErrInvalidGroupUpdate,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			fake := groupsFake()

			_, err := open(t, fake).GroupsUpdate(t.Context(), app.UpdateGroupRequest{
				Group: familyTitle, AvatarFile: new(test.path), Update: signal.GroupUpdate{Description: new("Avatar description")},
			})
			if !errors.Is(err, test.want) || len(fake.Connects()) != 0 || fake.GroupInfo[groupID].Revision != 4 {
				t.Fatalf("preflight error %v, connections %v", err, fake.Connects())
			}
		})
	}
}

// Catches ambiguous local/programmatic avatar operations before client access.
func TestGroupAvatarConflictingOperations(t *testing.T) {
	t.Parallel()

	path := avatarFile(t, avatarImage(t, 1, false))
	for _, req := range []app.UpdateGroupRequest{
		{Group: familyTitle, AvatarFile: new(path), RemoveAvatar: true},
		{
			Group: familyTitle, AvatarFile: new(path),
			Update: signal.GroupUpdate{Avatar: &signal.GroupAvatarUpdate{Remove: true}},
		},
		{Group: familyTitle, RemoveAvatar: true, Update: signal.GroupUpdate{Avatar: &signal.GroupAvatarUpdate{Remove: true}}},
	} {
		fake := groupsFake()

		_, err := open(t, fake).GroupsUpdate(t.Context(), req)
		if !errors.Is(err, signal.ErrInvalidGroupUpdate) || len(fake.Connects()) != 0 {
			t.Fatalf("conflicting avatar: %v, connections %v", err, fake.Connects())
		}
	}
}

// Catches dropped avatar operations, extra patches and losing omitted existing settings.
func TestGroupsUpdateAvatarLifecycle(t *testing.T) { //nolint:cyclop // sequential combined set, clear and no-op
	t.Parallel()

	for _, ref := range []string{familyTitle, groupID, masterKey, app.GroupPrefix + groupID} {
		t.Run(ref, func(t *testing.T) {
			t.Parallel()

			fake := groupsFake()
			use := open(t, fake)
			path := avatarFile(t, avatarImage(t, 1, false))

			got, err := use.GroupsUpdate(t.Context(), app.UpdateGroupRequest{
				Group: ref, AvatarFile: new(path), Update: signal.GroupUpdate{Description: new("With avatar")},
			})
			if err != nil || got.AvatarPath == "" || got.Revision != 5 || got.Description != "With avatar" {
				t.Fatalf("set combined = %+v, %v", got, err)
			}

			got, err = use.GroupsUpdate(t.Context(), app.UpdateGroupRequest{Group: ref, RemoveAvatar: true})
			if err != nil || got.AvatarPath != "" || got.Revision != 6 || got.Description != "With avatar" {
				t.Fatalf("clear = %+v, %v", got, err)
			}

			fake.UpdateGroupErr = errBoom

			got, err = use.GroupsUpdate(t.Context(), app.UpdateGroupRequest{Group: ref, RemoveAvatar: true})
			if err != nil || got.Revision != 6 || len(fake.Connects()) != 1 {
				t.Fatalf("clear no-op = %+v, %v", got, err)
			}
		})
	}
}
