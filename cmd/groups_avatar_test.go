package cmd_test

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/cmd"
	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/go-signal/internal/signal/signaltest"
)

const (
	avatarConfigFlag      = "--config"
	groupAvatarFlag       = "--avatar"
	groupRemoveAvatarFlag = "--remove-avatar"
	existingAvatarPath    = "existing-avatar-path"
)

func groupAvatarImage(t *testing.T, width int) []byte {
	t.Helper()

	var data bytes.Buffer

	err := png.Encode(&data, image.NewRGBA(image.Rect(0, 0, width, 1)))
	if err != nil {
		t.Fatal(err)
	}

	return data.Bytes()
}

func groupAvatarFile(t *testing.T, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "group-avatar.png")

	err := os.WriteFile(path, data, 0o600)
	if err != nil {
		t.Fatal(err)
	}

	return path
}

// Catches invalid avatar files and mutually exclusive settings reaching account open.
func TestGroupsAvatarPreflightBeforeOpen(t *testing.T) {
	t.Parallel()
	valid := groupAvatarImage(t, 1)

	path := groupAvatarFile(t, valid)
	for _, args := range [][]string{
		{groupAvatarFlag, ""},
		{groupAvatarFlag, filepath.Join(t.TempDir(), "missing-avatar")},
		{groupAvatarFlag, t.TempDir()},
		{groupAvatarFlag, groupAvatarFile(t, make([]byte, signal.MaxGroupAvatarSize+1))},
		{groupAvatarFlag, groupAvatarFile(t, []byte("invalid avatar"))},
		{groupAvatarFlag, groupAvatarFile(t, valid[:33])},
		{groupAvatarFlag, groupAvatarFile(t, groupAvatarImage(t, signal.MaxGroupAvatarDimension+1))},
		{groupAvatarFlag, path, groupRemoveAvatarFlag},
		{groupAvatarFlag, path, "--remove-avatar=false"},
		{"--remove-avatar=false"},
	} {
		fake := groupsFake()

		out, err := run(t, fake, append([]string{groupsCmd, updateCmd, familyTitle}, args...)...)
		if err == nil || out != "" || len(fake.Opened()) != 0 {
			t.Fatalf("preflight output %q, error %v, opened %v", out, err, fake.Opened())
		}
	}
}

// Catches loading the avatar only after account opening or reopening it in the app.
func TestGroupsAvatarPreparedBeforeAccountOpen(t *testing.T) {
	t.Parallel()
	data := groupAvatarImage(t, 1)
	path := groupAvatarFile(t, data)
	cfg := filepath.Join(t.TempDir(), "config.yaml")

	err := os.WriteFile(cfg, nil, 0o600)
	if err != nil {
		t.Fatal(err)
	}

	fake := groupsFake()
	factory := func(ctx context.Context, opts signal.Options) (signal.Client, error) {
		removeErr := os.Remove(path)
		if removeErr != nil {
			t.Fatal(removeErr)
		}

		return fake.Factory(ctx, opts)
	}

	var out bytes.Buffer

	root := cmd.NewRootCmd(cmd.WithClientFactory(factory), cmd.WithLocation(time.UTC))
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{
		avatarConfigFlag, cfg, dataDirFlag, t.TempDir(), groupsCmd, updateCmd, familyTitle, groupAvatarFlag, path,
	})

	err = root.ExecuteContext(t.Context())
	if err != nil || !bytes.Equal(fake.GroupAvatarData[groupID], data) || !fake.AllClosed() {
		t.Fatalf("prepared CLI output %q, error %v, uploaded %x", out.String(), err, fake.GroupAvatarData[groupID])
	}
}

// Catches incorrect set/clear/no-op/combined mutation output and revision behavior.
func TestGroupsAvatarGolden(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		initial bool
		args    []string
	}{
		{"groups_update_avatar", false, []string{groupAvatarFlag, groupAvatarFile(t, groupAvatarImage(t, 1))}},
		{"groups_update_avatar_clear", true, []string{groupRemoveAvatarFlag}},
		{"groups_update_avatar_noop", false, []string{groupRemoveAvatarFlag}},
		{"groups_update_avatar_combined", false, []string{
			groupAvatarFlag, groupAvatarFile(t, groupAvatarImage(t, 1)),
			groupDescriptionFlag, groupSettingsNewDescription, groupTimerFlag, "3600",
		}},
	} {
		for _, jsonOutput := range []bool{false, true} {
			name := test.name
			args := []string{groupsCmd, updateCmd, familyTitle}

			if jsonOutput {
				name += "_" + formatJSON

				args = append([]string{"-o", formatJSON}, args...)
			}

			t.Run(name, func(t *testing.T) {
				t.Parallel()

				fake := groupsFake()
				if test.initial {
					group := fake.GroupInfo[groupID]
					group.AvatarPath = existingAvatarPath
					fake.GroupInfo[groupID] = group
				}

				out, err := run(t, fake, append(args, test.args...)...)
				if err != nil {
					t.Fatal(err)
				}

				golden(t, name, out)
			})
		}
	}
}

// Catches treating an explicitly false removal flag as an avatar clear.
func TestGroupsAvatarFalseRemovalPreservesAvatar(t *testing.T) {
	t.Parallel()

	fake := groupsFake()
	initial := fake.GroupInfo[groupID]
	initial.AvatarPath = existingAvatarPath
	fake.GroupInfo[groupID] = initial

	_, err := run(t, fake, groupsCmd, updateCmd, familyTitle, "--remove-avatar=false", groupTimerFlag, "3600")
	if err != nil || fake.GroupInfo[groupID].AvatarPath != existingAvatarPath || fake.GroupInfo[groupID].Revision != 13 {
		t.Fatalf("explicit false removal group %+v, error %v", fake.GroupInfo[groupID], err)
	}
}

// Catches printing success or mutating state on failed avatar uploads/patches.
func TestGroupsAvatarFailureOutput(t *testing.T) { //nolint:cyclop // accepted and atomic failure contracts
	t.Parallel()

	for _, test := range []struct {
		name     string
		setup    func(*signaltest.Fake)
		want     error
		accepted bool
	}{
		{
			"avatar upload failure", func(f *signaltest.Fake) { f.GroupAvatarUploadErr = errGroupUpdateFollowUp },
			errGroupUpdateFollowUp, false,
		},
		{
			"avatar revision conflict", func(f *signaltest.Fake) { f.UpdateGroupErr = signal.ErrGroupChanged },
			signal.ErrGroupChanged, false,
		},
		{
			"avatar uncertain update", func(f *signaltest.Fake) { f.UpdateGroupErr = signal.ErrGroupUpdateUncertain },
			signal.ErrGroupUpdateUncertain, false,
		},
		{
			"avatar accepted follow-up", func(f *signaltest.Fake) { f.GroupUpdateFollowUpErr = errGroupUpdateFollowUp },
			errGroupUpdateFollowUp, true,
		},
		{"avatar forbidden", func(f *signaltest.Fake) {
			group := f.GroupInfo[groupID]
			group.Members[0].Role = signal.GroupRoleMember
			f.GroupInfo[groupID] = group
		}, signal.ErrGroupPermission, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			fake := groupsFake()
			test.setup(fake)
			before := avatarGroupSnapshot(fake.GroupInfo[groupID])

			avatars := maps.Clone(fake.GroupAvatarData)
			for id, data := range avatars {
				avatars[id] = slices.Clone(data)
			}

			path := groupAvatarFile(t, groupAvatarImage(t, 1))

			out, err := run(t, fake, groupsCmd, updateCmd, familyTitle, groupAvatarFlag, path)
			if !errors.Is(err, test.want) || out != "" || strings.Contains(err.Error(), "accepted") != test.accepted {
				t.Fatalf("avatar output %q, error %v", out, err)
			}

			if test.accepted {
				if fake.GroupInfo[groupID].Revision != 13 || fake.GroupInfo[groupID].AvatarPath == "" ||
					!strings.Contains(err.Error(), "groups show") {
					t.Fatalf("accepted avatar: %v", err)
				}
			} else if !reflect.DeepEqual(before, fake.GroupInfo[groupID]) || !reflect.DeepEqual(avatars, fake.GroupAvatarData) {
				t.Fatalf("failed avatar changed state: %+v", fake.GroupInfo[groupID])
			}
		})
	}
}

func avatarGroupSnapshot(group signal.Group) signal.Group {
	group.Members = slices.Clone(group.Members)
	group.Pending = slices.Clone(group.Pending)
	group.Requesting = slices.Clone(group.Requesting)
	group.Banned = slices.Clone(group.Banned)

	return group
}
