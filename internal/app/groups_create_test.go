package app_test

import (
	"context"
	"errors"
	"testing"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/signal"
)

func TestGroupsCreate(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		members []string
		count   int
	}{
		{"alone", nil, 1},
		{"duplicates", []string{aliceNumber, aliceACI, app.SelfRecipient, testAccount().Number}, 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			fake := directory()
			use := open(t, fake)

			group, err := use.GroupsCreate(t.Context(), app.CreateGroupRequest{Title: familyTitle, Members: test.members})
			if err != nil {
				t.Fatal(err)
			}

			if group.ID == "" || len(group.Members) != test.count || group.Role != signal.GroupRoleAdmin {
				t.Fatalf("created = %+v", group)
			}

			id, err := use.ResolveGroup(t.Context(), familyTitle)
			if err != nil || id != group.ID {
				t.Errorf("cached title = %q, %v", id, err)
			}

			if calls := fake.Connects(); len(calls) != 1 {
				t.Errorf("connections = %+v", calls)
			}
		})
	}
}

func TestGroupsCreateErrors(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name     string
		req      app.CreateGroupRequest
		want     error
		connects int
	}{
		{"blank", app.CreateGroupRequest{Title: " "}, signal.ErrInvalidGroupTitle, 0},
		{
			"invalid member",
			app.CreateGroupRequest{Title: familyTitle, Members: []string{"not a recipient"}},
			app.ErrInvalidRecipient, 0,
		},
		{
			"group member",
			app.CreateGroupRequest{Title: familyTitle, Members: []string{"group:" + groupID}},
			app.ErrInvalidRecipient, 0,
		},
		{
			"unknown member",
			app.CreateGroupRequest{Title: familyTitle, Members: []string{"+4915999999999"}},
			signal.ErrNotOnSignal, 1,
		},
		{"server error", app.CreateGroupRequest{Title: familyTitle}, errBoom, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			fake := directory()
			fake.CreateGroupErr = errBoom

			_, err := open(t, fake).GroupsCreate(t.Context(), test.req)
			if !errors.Is(err, test.want) {
				t.Errorf("error = %v, want %v", err, test.want)
			}

			if len(fake.GroupInfo) != 0 || len(fake.Connects()) != test.connects {
				t.Errorf("groups = %+v, connects = %+v", fake.GroupInfo, fake.Connects())
			}
		})
	}
}

type partialCreateClient struct {
	signal.Client

	calls int
}

func (c *partialCreateClient) CreateGroup(context.Context, signal.CreateGroupOptions) (signal.Group, error) {
	c.calls++

	return signal.Group{ID: groupID}, errBoom
}

func TestGroupsCreatePartialFailure(t *testing.T) {
	t.Parallel()

	client, err := directory().Factory(t.Context(), signal.Options{})
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = client.Close() })

	partial := &partialCreateClient{Client: client}

	group, err := app.New(partial).GroupsCreate(t.Context(), app.CreateGroupRequest{Title: familyTitle})
	if !errors.Is(err, errBoom) || group.ID != groupID || partial.calls != 1 {
		t.Errorf("partial result = %+v, %v; attempts %d", group, err, partial.calls)
	}
}

func TestGroupsCreateIsNotIdempotent(t *testing.T) {
	t.Parallel()

	use := open(t, directory())

	first, err := use.GroupsCreate(t.Context(), app.CreateGroupRequest{Title: familyTitle})
	if err != nil {
		t.Fatal(err)
	}

	second, err := use.GroupsCreate(t.Context(), app.CreateGroupRequest{Title: familyTitle})
	if err != nil || second.ID == first.ID {
		t.Errorf("second creation = %+v, %v", second, err)
	}
}
