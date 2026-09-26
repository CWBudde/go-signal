package app_test

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/go-signal/internal/signal/signaltest"
)

const (
	clubID      = "Y2x1Yi1pZC1jbHViLWlkLWNsdWItaWQtY2x1Yi1pZC0="
	masterKey   = "bWFzdGVyLWtleS1tYXN0ZXIta2V5LW1hc3Rlci1rZXk="
	familyTitle = "Family"
	clubTitle   = "Club"
	nobody      = "Nobody"
)

// groupsFake returns a fake on which the test account is the only admin of "Family" (groupID,
// with alice) and a member of "Club" (clubID); masterKey is Family's master key.
func groupsFake() *signaltest.Fake {
	fake := directory()
	own := testAccount().ACI
	fake.GroupInfo = map[string]signal.Group{
		groupID: {ID: groupID, Title: familyTitle, Revision: 4, Members: []signal.GroupMember{
			{Recipient: signal.Recipient{ACI: own}, Role: signal.GroupRoleAdmin},
			{Recipient: signal.Recipient{ACI: aliceACI}, Role: signal.GroupRoleMember},
		}},
		clubID: {ID: clubID, Title: clubTitle, Members: []signal.GroupMember{
			{Recipient: signal.Recipient{ACI: own}, Role: signal.GroupRoleMember},
		}},
	}
	fake.GroupKeys = map[string]string{masterKey: groupID}

	return fake
}

func TestGroupsList(t *testing.T) {
	t.Parallel()

	fake := groupsFake()
	fake.GroupErrs = map[string]error{"gone": signal.ErrNotAMember}

	groups, err := open(t, fake).GroupsList(t.Context())
	if err != nil {
		t.Fatalf("GroupsList: %v", err)
	}

	got := make([]string, 0, len(groups))
	for _, group := range groups {
		got = append(got, group.Title+"/"+group.Membership.String()+"/"+group.Role.String())
	}

	// Sorted by title; the unavailable group without a title last.
	if want := []string{"Club/member/member", "Family/member/admin", "/none/unknown"}; !slices.Equal(got, want) {
		t.Errorf("groups = %v, want %v", got, want)
	}

	if !errors.Is(groups[2].Err, signal.ErrNotAMember) {
		t.Errorf("unavailable group error = %v", groups[2].Err)
	}

	if len(fake.Connects()) != 1 {
		t.Errorf("connected %d times, want once", len(fake.Connects()))
	}
}

func TestGroupsListErrors(t *testing.T) {
	t.Parallel()

	failing := groupsFake()
	failing.GroupErrs = map[string]error{clubID: errBoom}

	unlinked := groupsFake()
	unlinked.Incoming = []signal.Event{&signal.Connection{State: signal.StateLoggedOut}}

	for name, test := range map[string]struct {
		fake *signaltest.Fake
		want error
	}{
		"fetch":      {failing, errBoom},
		"logged out": {unlinked, signal.ErrDeviceUnlinked},
		"in use":     {&signaltest.Fake{Linked: []signal.Account{testAccount()}, InUse: true}, signal.ErrAccountInUse},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := open(t, test.fake).GroupsList(t.Context())
			if !errors.Is(err, test.want) || !strings.HasPrefix(err.Error(), "groups list: ") {
				t.Errorf("GroupsList error = %v, want %v prefixed with the command", err, test.want)
			}
		})
	}
}

func TestResolveGroup(t *testing.T) {
	t.Parallel()

	fake := groupsFake()
	info := fake.GroupInfo[clubID]
	info.Title = "  family "
	fake.GroupInfo["ZHVwLWR1cC1kdXAtZHVwLWR1cC1kdXAtZHVwLWR1cC0="] = info

	a := open(t, fake)

	tests := []struct {
		arg  string
		want string
		err  error
	}{
		{"group:" + groupID, groupID, nil},
		{groupID, groupID, nil},
		{masterKey, masterKey, nil},
		// URL-safe alphabet without padding.
		{"Z3JvdXAtaWQtZ3JvdXAtaWQtZ3JvdXAtaWQtZ3JvdXA", groupID, nil},
		{"CLUB", clubID, nil},
		{familyTitle, "", app.ErrAmbiguousGroup},
		{nobody, "", signal.ErrUnknownGroup},
		{" ", "", signal.ErrUnknownGroup},
		{"group:short", "", app.ErrInvalidRecipient},
	}

	for _, test := range tests {
		got, err := a.ResolveGroup(t.Context(), test.arg)
		if got != test.want || !errors.Is(err, test.err) || (test.err == nil && err != nil) {
			t.Errorf("ResolveGroup(%q) = %q, %v; want %q, %v", test.arg, got, err, test.want, test.err)
		}
	}

	_, err := a.ResolveGroup(t.Context(), "family")
	if err == nil || !strings.Contains(err.Error(), "group:"+groupID) || !strings.Contains(err.Error(), "group:ZHVw") {
		t.Errorf("ambiguous error doesn't list the candidates: %v", err)
	}

	if len(fake.Connects()) != 0 {
		t.Error("ResolveGroup connected")
	}
}

func TestGroupsShow(t *testing.T) {
	t.Parallel()

	for _, arg := range []string{groupID, masterKey, "family"} {
		group, err := open(t, groupsFake()).GroupsShow(t.Context(), arg)
		if err != nil || group.ID != groupID || group.Role != signal.GroupRoleAdmin || len(group.Members) != 2 {
			t.Errorf("GroupsShow(%s) = %+v, %v", arg, group, err)
		}
	}
}

func TestGroupsShowErrors(t *testing.T) {
	t.Parallel()

	removed := groupsFake()
	removed.GroupErrs = map[string]error{groupID: signal.ErrNotAMember}

	tests := []struct {
		name     string
		fake     *signaltest.Fake
		arg      string
		want     error
		connects int
	}{
		{"unknown title", groupsFake(), nobody, signal.ErrUnknownGroup, 0},
		{"unknown ID", groupsFake(), "group:" + strings.Repeat("A", 43) + "=", signal.ErrUnknownGroup, 1},
		{"removed", removed, groupID, signal.ErrNotAMember, 1},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, err := open(t, test.fake).GroupsShow(t.Context(), test.arg)
			if !errors.Is(err, test.want) || !strings.HasPrefix(err.Error(), "groups show: ") {
				t.Errorf("GroupsShow error = %v, want %v", err, test.want)
			}

			if got := len(test.fake.Connects()); got != test.connects {
				t.Errorf("connected %d times, want %d", got, test.connects)
			}
		})
	}
}

func TestGroupsLeave(t *testing.T) {
	t.Parallel()

	fake := groupsFake()
	fake.LeaveTime = time.Date(2026, 9, 26, 10, 0, 0, 0, time.UTC)

	// The only admin of Family has to promote alice.
	_, err := once(t, fake, func(a *app.App) (signal.LeaveResult, error) {
		return a.GroupsLeave(t.Context(), app.LeaveRequest{Group: familyTitle})
	})
	if !errors.Is(err, signal.ErrLastAdmin) {
		t.Fatalf("GroupsLeave error = %v, want ErrLastAdmin", err)
	}

	res, err := once(t, fake, func(a *app.App) (signal.LeaveResult, error) {
		return a.GroupsLeave(t.Context(), app.LeaveRequest{Group: familyTitle, Promote: []string{aliceNumber}})
	})
	if err != nil {
		t.Fatalf("GroupsLeave: %v", err)
	}

	wantLeft(t, fake, res)

	// Afterwards the group is listed as left.
	groups, err := once(t, fake, func(a *app.App) ([]signal.Group, error) { return a.GroupsList(t.Context()) })
	if err != nil {
		t.Fatalf("GroupsList: %v", err)
	}

	i := slices.IndexFunc(groups, func(g signal.Group) bool { return g.ID == groupID })
	if i < 0 || !errors.Is(groups[i].Err, signal.ErrNotAMember) || !groups[i].LeftAt.Equal(fake.LeaveTime) {
		t.Errorf("left group listed as %+v", groups)
	}
}

// wantLeft checks that res and fake show that we left Family and promoted alice.
func wantLeft(t *testing.T, fake *signaltest.Fake, res signal.LeaveResult) {
	t.Helper()

	alice := signal.Recipient{ACI: aliceACI, PNI: carolACI, Number: aliceNumber}

	if res.Group.ID != groupID || res.Group.Membership != signal.MembershipMember || res.Revision != 5 ||
		!res.Group.LeftAt.Equal(fake.LeaveTime) || !slices.Equal(res.Promoted, []signal.Recipient{{ACI: aliceACI}}) {
		t.Errorf("GroupsLeave = %+v", res)
	}

	want := signaltest.LeaveCall{ACI: testAccount().ACI, GroupID: groupID, Promote: []signal.Recipient{alice}}

	got := fake.Leaves()
	if len(got) != 1 || got[0].ACI != want.ACI || got[0].GroupID != want.GroupID ||
		!slices.Equal(got[0].Promote, want.Promote) {
		t.Errorf("leaves = %+v, want %+v", got, want)
	}
}

func TestGroupsLeaveErrors(t *testing.T) {
	t.Parallel()

	invited := groupsFake()
	invited.GroupInfo[clubID] = signal.Group{ID: clubID, Title: clubTitle, Pending: []signal.PendingMember{
		{Recipient: signal.Recipient{ACI: testAccount().ACI}},
	}}

	failing := groupsFake()
	failing.LeaveErr = errBoom

	// promote leaves Family promoting who.
	promote := func(who string) app.LeaveRequest {
		return app.LeaveRequest{Group: familyTitle, Promote: []string{who}}
	}

	tests := []struct {
		name string
		fake *signaltest.Fake
		req  app.LeaveRequest
		want error
	}{
		{"promote a group", groupsFake(), promote(app.GroupPrefix + clubID), app.ErrInvalidRecipient},
		{"promote self", groupsFake(), promote(app.SelfRecipient), app.ErrInvalidRecipient},
		{"promote unknown", groupsFake(), promote("+4915100000000"), signal.ErrNotOnSignal},
		{"promote non-member", groupsFake(), promote(bobUsername), signal.ErrInvalidPromotion},
		{
			"invited promotes", invited,
			app.LeaveRequest{Group: clubTitle, Promote: []string{aliceNumber}},
			signal.ErrInvalidPromotion,
		},
		{"unknown", groupsFake(), app.LeaveRequest{Group: nobody}, signal.ErrUnknownGroup},
		{"leave fails", failing, app.LeaveRequest{Group: clubTitle}, errBoom},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, err := open(t, test.fake).GroupsLeave(t.Context(), test.req)
			if !errors.Is(err, test.want) || !strings.HasPrefix(err.Error(), "groups leave: ") {
				t.Errorf("GroupsLeave error = %v, want %v", err, test.want)
			}

			if len(test.fake.Leaves()) != 0 {
				t.Error("left anyway")
			}
		})
	}
}

func TestGroupsLeaveInvitation(t *testing.T) {
	t.Parallel()

	fake := groupsFake()
	fake.GroupInfo[clubID] = signal.Group{ID: clubID, Title: clubTitle, Pending: []signal.PendingMember{
		{Recipient: signal.Recipient{ACI: testAccount().ACI}, AddedBy: signal.Recipient{ACI: aliceACI}},
	}}

	res, err := open(t, fake).GroupsLeave(t.Context(), app.LeaveRequest{Group: "group:" + clubID})
	if err != nil || res.Group.Membership != signal.MembershipPending {
		t.Errorf("GroupsLeave = %+v, %v; want a declined invitation", res, err)
	}
}

// once runs one use case like a CLI command: on a fresh client of fake, closed afterwards.
func once[T any](t *testing.T, fake *signaltest.Fake, run func(*app.App) (T, error)) (T, error) {
	t.Helper()

	client, err := fake.Factory(t.Context(), signal.Options{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	defer func() { _ = client.Close() }()

	return run(app.New(client))
}
