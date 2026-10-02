package app_test

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/go-signal/internal/signal/signaltest"
)

const linkPassword = "MDEyMzQ1Njc4OWFiY2RlZg=="

func linkFake() *signaltest.Fake {
	fake := groupsFake()
	group := fake.GroupInfo[groupID]
	group.MasterKey = masterKey
	fake.GroupInfo[groupID] = group
	fake.GroupLinkStates = map[string]signal.GroupLinkState{groupID: signal.GroupLinkEnabled}
	fake.GroupLinkPasswords = map[string]string{groupID: linkPassword}

	return fake
}

// Catches wrong group resolution, wrong state/reset arguments and extra revision increments.
func TestGroupsLinkLifecycle(t *testing.T) { //nolint:cyclop // sequential show, update, no-op and reset
	t.Parallel()

	for _, ref := range []string{familyTitle, groupID, masterKey, app.GroupPrefix + groupID} {
		t.Run(ref, func(t *testing.T) {
			t.Parallel()

			fake := linkFake()
			use := open(t, fake)

			shown, err := use.GroupsLinkShow(t.Context(), app.GroupLinkRequest{Group: ref})
			if err != nil || shown.ID != groupID || shown.Revision != 4 ||
				shown.State != signal.GroupLinkEnabled || shown.URL == "" {
				t.Fatalf("show = %+v, %v", shown, err)
			}

			req := app.GroupLinkUpdateRequest{Group: ref, Update: signal.GroupLinkUpdate{State: new(signal.GroupLinkApproval)}}

			updated, err := use.GroupsLinkUpdate(t.Context(), req)
			if err != nil || updated.Revision != 5 || updated.State != signal.GroupLinkApproval || updated.URL != shown.URL {
				t.Fatalf("approval = %+v, %v", updated, err)
			}

			noop, err := use.GroupsLinkUpdate(t.Context(), req)
			if err != nil || noop != updated {
				t.Fatalf("no-op = %+v, %v", noop, err)
			}

			req.Update = signal.GroupLinkUpdate{Reset: true}

			reset, err := use.GroupsLinkUpdate(t.Context(), req)
			if err != nil || reset.Revision != 6 || reset.State != signal.GroupLinkApproval || reset.URL == updated.URL {
				t.Fatalf("reset = %+v, %v", reset, err)
			}

			req.Update = signal.GroupLinkUpdate{State: new(signal.GroupLinkDisabled), Reset: true}

			disabled, err := use.GroupsLinkUpdate(t.Context(), req)
			if err != nil || disabled.Revision != 7 || disabled.State != signal.GroupLinkDisabled || disabled.URL != "" {
				t.Fatalf("disable/reset = %+v, %v", disabled, err)
			}

			if calls := fake.Connects(); len(calls) != 1 || calls[0] != testAccount().ACI {
				t.Fatalf("connections = %+v", calls)
			}
		})
	}
}

// Catches preflight that opens a connection or echoes a secret-bearing reference/state.
func TestGroupsLinkPreflight(t *testing.T) {
	t.Parallel()

	for _, ref := range []string{"", "group:invalid-link-ref", "https://signal.group/#private-link-secret"} {
		fake := linkFake()
		use := open(t, fake)
		_, showErr := use.GroupsLinkShow(t.Context(), app.GroupLinkRequest{Group: ref})

		_, updateErr := use.GroupsLinkUpdate(t.Context(), app.GroupLinkUpdateRequest{
			Group: ref, Update: signal.GroupLinkUpdate{Reset: true},
		})
		for _, err := range []error{showErr, updateErr} {
			if err == nil || len(fake.Connects()) != 0 || (ref != "" && strings.Contains(err.Error(), ref)) {
				t.Fatalf("preflight error %v, connects %v", err, fake.Connects())
			}
		}
	}
}

func TestGroupsLinkInvalidUpdatePreflight(t *testing.T) {
	t.Parallel()

	for _, update := range []signal.GroupLinkUpdate{{}, {State: new(signal.GroupLinkState("private-state-secret"))}} {
		fake := linkFake()

		_, err := open(t, fake).GroupsLinkUpdate(t.Context(), app.GroupLinkUpdateRequest{Group: familyTitle, Update: update})
		if !errors.Is(err, signal.ErrInvalidGroupLinkUpdate) || len(fake.Connects()) != 0 ||
			strings.Contains(err.Error(), "private-state-secret") {
			t.Fatalf("invalid update: %v, connects %v", err, fake.Connects())
		}
	}
}

// Catches reference leaks from backend lookup as well as app title resolution.
func TestGroupsLinkPrivateUnknownReferences(t *testing.T) {
	t.Parallel()

	for _, ref := range []string{nobody, strings.Repeat("A", 43) + "="} {
		use := open(t, linkFake())
		_, showErr := use.GroupsLinkShow(t.Context(), app.GroupLinkRequest{Group: ref})

		_, updateErr := use.GroupsLinkUpdate(t.Context(), app.GroupLinkUpdateRequest{
			Group: ref, Update: signal.GroupLinkUpdate{Reset: true},
		})
		for _, err := range []error{showErr, updateErr} {
			if !errors.Is(err, signal.ErrUnknownGroup) || strings.Contains(err.Error(), ref) {
				t.Fatalf("reference leaked: %v", err)
			}
		}
	}
}

// Catches mutation before fresh authorization, failed connection or rejected patch.
func TestGroupsLinkFailuresAreAtomic(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name  string
		setup func(*signaltest.Fake)
		want  error
		show  bool
	}{
		{"link connect failure", func(f *signaltest.Fake) { f.ConnectErr = errBoom }, errBoom, true},
		{"link read failure", func(f *signaltest.Fake) { f.GroupLinkErr = errBoom }, errBoom, true},
		{
			"link conflict", func(f *signaltest.Fake) { f.UpdateGroupLinkErr = signal.ErrGroupChanged },
			signal.ErrGroupChanged, false,
		},
		{"link member read", func(f *signaltest.Fake) {
			group := f.GroupInfo[groupID]
			group.Members[0].Role = signal.GroupRoleMember
			f.GroupInfo[groupID] = group
		}, nil, true},
		{"link member write", func(f *signaltest.Fake) {
			group := f.GroupInfo[groupID]
			group.Members[0].Role = signal.GroupRoleMember
			f.GroupInfo[groupID] = group
		}, signal.ErrGroupPermission, false},
		{"link removed read", func(f *signaltest.Fake) {
			group := f.GroupInfo[groupID]
			group.Members = slices.Clone(group.Members[1:])
			f.GroupInfo[groupID] = group
		}, signal.ErrNotAMember, true},
		{"link removed write", func(f *signaltest.Fake) {
			group := f.GroupInfo[groupID]
			group.Members = slices.Clone(group.Members[1:])
			f.GroupInfo[groupID] = group
		}, signal.ErrNotAMember, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			fake := linkFake()
			test.setup(fake)
			before := snapshotLinkGroup(fake.GroupInfo[groupID])
			passwords, states := maps.Clone(fake.GroupLinkPasswords), maps.Clone(fake.GroupLinkStates)
			use := open(t, fake)

			var err error
			if test.show {
				_, err = use.GroupsLinkShow(t.Context(), app.GroupLinkRequest{Group: familyTitle})
			} else {
				_, err = use.GroupsLinkUpdate(t.Context(), app.GroupLinkUpdateRequest{
					Group: familyTitle, Update: signal.GroupLinkUpdate{State: new(signal.GroupLinkDisabled)},
				})
			}

			if !errors.Is(err, test.want) || !reflect.DeepEqual(before, fake.GroupInfo[groupID]) ||
				!maps.Equal(passwords, fake.GroupLinkPasswords) || !maps.Equal(states, fake.GroupLinkStates) {
				t.Fatalf("error %v, group %+v", err, fake.GroupInfo[groupID])
			}
		})
	}
}

func snapshotLinkGroup(group signal.Group) signal.Group {
	group.Members = slices.Clone(group.Members)
	group.Pending = slices.Clone(group.Pending)
	group.Requesting = slices.Clone(group.Requesting)
	group.Banned = slices.Clone(group.Banned)

	return group
}

type partialLinkClient struct {
	signal.Client

	calls   int
	result  signal.GroupLink
	failure error
}

func (c *partialLinkClient) UpdateGroupLink(context.Context, string, signal.GroupLinkUpdate) (signal.GroupLink, error) {
	c.calls++
	return c.result, c.failure
}

// Catches retries or the loss of accepted metadata/inspection guidance.
func TestGroupsLinkDoesNotRetry(t *testing.T) {
	t.Parallel()

	for _, accepted := range []bool{false, true} {
		client, err := linkFake().Factory(t.Context(), signal.Options{})
		if err != nil {
			t.Fatal(err)
		}

		t.Cleanup(func() { _ = client.Close() })

		partial := &partialLinkClient{Client: client, failure: signal.ErrGroupUpdateUncertain}
		want := signal.ErrGroupUpdateUncertain

		if accepted {
			partial.result = signal.GroupLink{ID: groupID, Revision: 5}
			partial.failure = errBoom
			want = errBoom
		}

		result, err := app.New(partial).GroupsLinkUpdate(t.Context(), app.GroupLinkUpdateRequest{
			Group: familyTitle, Update: signal.GroupLinkUpdate{Reset: true},
		})
		if !errors.Is(err, want) || partial.calls != 1 || result != partial.result ||
			strings.Contains(err.Error(), "accepted") != accepted || !strings.Contains(err.Error(), "inspect groups link show") {
			t.Fatalf("result %+v, error %v, calls %d", result, err, partial.calls)
		}
	}
}

// Catches overbroad URL filtering that rejects otherwise valid cached group titles.
func TestGroupsLinkTitleContainingURL(t *testing.T) {
	t.Parallel()

	fake := linkFake()
	title := "Discuss https://example.com"
	fake.GroupTitleCache[groupID] = signal.CachedGroup{Title: title}

	got, err := open(t, fake).GroupsLinkShow(t.Context(), app.GroupLinkRequest{Group: title})
	if err != nil || got.ID != groupID {
		t.Fatalf("URL phrase title = %+v, %v", got, err)
	}
}
