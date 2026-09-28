package app_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/go-signal/internal/signal/signaltest"
)

func TestGroupsRename(t *testing.T) {
	t.Parallel()

	for _, ref := range []string{familyTitle, groupID, masterKey} {
		t.Run(ref, func(t *testing.T) {
			t.Parallel()

			fake := groupsFake()
			use := open(t, fake)

			const title = "Family 🐶"

			group, err := use.GroupsRename(t.Context(), app.RenameGroupRequest{Group: ref, Title: title})
			if err != nil || group.Title != title || group.Revision != 5 || len(group.Members) != 2 {
				t.Fatalf("GroupsRename = %+v, %v", group, err)
			}

			id, err := use.ResolveGroup(t.Context(), title)
			if err != nil || id != groupID {
				t.Errorf("new title = %q, %v", id, err)
			}

			_, err = use.ResolveGroup(t.Context(), familyTitle)
			if !errors.Is(err, signal.ErrUnknownGroup) {
				t.Errorf("old title still resolves: %v", err)
			}
		})
	}
}

func TestGroupsRenameErrors(t *testing.T) {
	const title = "New"

	t.Parallel()

	conflict := groupsFake()
	conflict.RenameErr = signal.ErrGroupChanged
	failed := groupsFake()
	failed.RenameErr = errBoom
	removed := groupsFake()
	removed.GroupErrs = map[string]error{groupID: signal.ErrNotAMember}

	for _, test := range []struct {
		name     string
		fake     *signaltest.Fake
		ref      string
		title    string
		want     error
		connects int
	}{
		{"blank", groupsFake(), familyTitle, " ", signal.ErrInvalidGroupTitle, 0},
		{"unknown", groupsFake(), nobody, title, signal.ErrUnknownGroup, 0},
		{"permission", groupsFake(), clubTitle, title, signal.ErrGroupPermission, 1},
		{"removed", removed, familyTitle, title, signal.ErrNotAMember, 1},
		{"conflict", conflict, familyTitle, title, signal.ErrGroupChanged, 1},
		{"failed", failed, familyTitle, title, errBoom, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, err := open(t, test.fake).GroupsRename(t.Context(), app.RenameGroupRequest{Group: test.ref, Title: test.title})
			if !errors.Is(err, test.want) || !strings.HasPrefix(err.Error(), "groups rename:") {
				t.Fatalf("GroupsRename = %v, want %v", err, test.want)
			}

			if len(test.fake.Connects()) != test.connects {
				t.Errorf("connects = %d, want %d", len(test.fake.Connects()), test.connects)
			}

			if test.fake.GroupInfo[groupID].Title != familyTitle || test.fake.GroupTitleCache[groupID].Title != familyTitle {
				t.Error("failed rename changed the title")
			}
		})
	}
}

func TestGroupsRenameUnchanged(t *testing.T) {
	t.Parallel()

	fake := groupsFake()
	fake.RenameErr = errBoom // An unchanged title must not send an update.

	group, err := open(t, fake).GroupsRename(t.Context(), app.RenameGroupRequest{Group: familyTitle, Title: familyTitle})
	if err != nil || group.Revision != 4 {
		t.Errorf("unchanged title = %+v, %v", group, err)
	}
}
