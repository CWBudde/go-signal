package app_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/signal"
)

func TestGroupsUpdate(t *testing.T) { //nolint:cyclop // checks combined fields and preserved state
	t.Parallel()

	for _, ref := range []string{familyTitle, groupID, masterKey, "group:" + groupID} {
		t.Run(ref, func(t *testing.T) {
			t.Parallel()

			fake := groupsFake()
			initial := fake.GroupInfo[groupID]
			initial.Description = "Old"
			initial.Timer = time.Hour
			initial.AnnouncementsOnly = true
			fake.GroupInfo[groupID] = initial

			got, err := open(t, fake).GroupsUpdate(t.Context(), app.UpdateGroupRequest{
				Group: ref,
				Update: signal.GroupUpdate{
					Description: new(""), TimerSeconds: new(uint32(0)), AnnouncementsOnly: new(false),
					MembersCanEditAttributes: new(true), MembersCanAddMembers: new(true),
				},
			})
			if err != nil {
				t.Fatal(err)
			}

			if got.ID != groupID || got.Revision != 5 || got.Title != familyTitle || got.Description != "" ||
				got.Timer != 0 || got.AnnouncementsOnly || !got.MembersCanEditAttributes || !got.MembersCanAddMembers {
				t.Fatalf("GroupsUpdate = %+v", got)
			}

			if !reflect.DeepEqual(got.Members, initial.Members) || len(fake.Connects()) != 1 {
				t.Errorf("members/connections changed unexpectedly: %+v / %v", got.Members, fake.Connects())
			}

			if fake.GroupTitleCache[groupID].Title != familyTitle {
				t.Errorf("cache = %+v", fake.GroupTitleCache[groupID])
			}
		})
	}
}

func TestGroupsUpdatePreflight(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name string
		req  app.UpdateGroupRequest
		want error
	}{
		{"missing settings", app.UpdateGroupRequest{Group: familyTitle}, signal.ErrInvalidGroupUpdate},
		{
			"invalid text",
			app.UpdateGroupRequest{Group: familyTitle, Update: signal.GroupUpdate{Description: new("\xff")}},
			signal.ErrInvalidGroupUpdate,
		},
		{"empty group", app.UpdateGroupRequest{Update: signal.GroupUpdate{Description: new("New")}}, signal.ErrUnknownGroup},
		{
			"malformed group",
			app.UpdateGroupRequest{Group: "group:broken", Update: signal.GroupUpdate{Description: new("New")}},
			app.ErrInvalidRecipient,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Preflight must run before touching any client, including group-title lookup.
			_, err := app.New(nil).GroupsUpdate(t.Context(), test.req)
			if !errors.Is(err, test.want) {
				t.Errorf("GroupsUpdate = %v, want %v", err, test.want)
			}
		})
	}
}

func TestGroupsUpdateAtomicAndUnchanged(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name   string
		ref    string
		update signal.GroupUpdate
		want   error
	}{
		{"unknown group", nobody, signal.GroupUpdate{Description: new("New")}, signal.ErrUnknownGroup},
		{"member forbidden", clubTitle, signal.GroupUpdate{Description: new("New")}, signal.ErrGroupPermission},
		{
			"mixed permissions", clubTitle,
			signal.GroupUpdate{Description: new("New"), MembersCanEditAttributes: new(true)},
			signal.ErrGroupPermission,
		},
		{"unchanged", familyTitle, signal.GroupUpdate{Description: new("")}, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			fake := groupsFake()

			before := fake.GroupInfo[test.ref]
			switch test.ref {
			case familyTitle:
				before = fake.GroupInfo[groupID]
			case clubTitle:
				before = fake.GroupInfo[clubID]
			}

			fake.UpdateGroupErr = errBoom // No-op and permission checks must precede the patch.

			got, err := open(t, fake).GroupsUpdate(t.Context(), app.UpdateGroupRequest{Group: test.ref, Update: test.update})
			if !errors.Is(err, test.want) {
				t.Fatalf("GroupsUpdate = %+v, %v; want %v", got, err, test.want)
			}

			if test.want == nil && got.Revision != before.Revision {
				t.Errorf("no-op revision = %d, want %d", got.Revision, before.Revision)
			}

			if fake.GroupInfo[groupID].Revision != 4 || fake.GroupInfo[clubID].Description != "" {
				t.Error("failed/no-op update mutated the group")
			}
		})
	}
}

type groupUpdateFailureClient struct {
	signal.Client

	calls  int
	result signal.Group
	err    error
}

func (c *groupUpdateFailureClient) UpdateGroup(context.Context, string, signal.GroupUpdate) (signal.Group, error) {
	c.calls++

	return c.result, c.err
}

func TestGroupsUpdateReturnsAcceptedResultWithoutRetry(t *testing.T) {
	t.Parallel()

	for _, accepted := range []bool{false, true} {
		t.Run(map[bool]string{false: "revision conflict", true: "accepted follow-up"}[accepted], func(t *testing.T) {
			t.Parallel()

			fake := groupsFake()

			client, err := fake.Factory(t.Context(), signal.Options{})
			if err != nil {
				t.Fatal(err)
			}

			t.Cleanup(func() { _ = client.Close() })

			failure := &groupUpdateFailureClient{Client: client, err: signal.ErrGroupChanged}
			if accepted {
				failure.result = signal.Group{ID: groupID, Revision: 5}
				failure.err = errBoom
			}

			got, err := app.New(failure).GroupsUpdate(t.Context(), app.UpdateGroupRequest{
				Group: familyTitle, Update: signal.GroupUpdate{Description: new("New")},
			})
			if !errors.Is(err, failure.err) || !strings.HasPrefix(err.Error(), "groups update:") || failure.calls != 1 {
				t.Fatalf("GroupsUpdate = %+v, %v, calls %d", got, err, failure.calls)
			}

			if !reflect.DeepEqual(got, failure.result) {
				t.Errorf("accepted result lost: %+v, want %+v", got, failure.result)
			}
		})
	}
}
