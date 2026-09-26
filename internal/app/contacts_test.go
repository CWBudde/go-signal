package app_test

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/go-signal/internal/signal/signaltest"
)

const (
	bobNumber = "+15550102"
	daveACI   = "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
)

// contactsFake has us, alice (contact name), bob (number only, blocked) and carol (nickname
// over profile name) in the store; alice, bob and dave are on Signal.
func contactsFake() *signaltest.Fake {
	own := testAccount()

	return &signaltest.Fake{
		Linked: []signal.Account{own},
		Contacts: []signal.Contact{
			{Recipient: signal.Recipient{ACI: own.ACI, Number: own.Number}, ProfileName: "Me"},
			{Recipient: signal.Recipient{ACI: aliceACI, Number: aliceNumber}, ContactName: "Alice Smith"},
			{Recipient: signal.Recipient{ACI: bobACI, Number: bobNumber}, Blocked: true},
			{Recipient: signal.Recipient{ACI: carolACI}, ProfileName: "Caroline", Nickname: "carol"},
		},
		Directory: []signal.Recipient{
			{ACI: aliceACI, Number: aliceNumber},
			{ACI: bobACI, Number: bobNumber},
			{ACI: daveACI, Number: "+15550104", Username: "dave.04"},
		},
	}
}

func acis(contacts []signal.Contact) []string {
	out := make([]string, 0, len(contacts))
	for _, contact := range contacts {
		out = append(out, contact.ACI)
	}

	return out
}

func TestContactsList(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		req  app.ContactsListRequest
		want []string
	}{
		// By display name, ignoring case: +15550102, Alice Smith, carol; without us.
		{"all", app.ContactsListRequest{}, []string{bobACI, aliceACI, carolACI}},
		{"blocked", app.ContactsListRequest{Blocked: true}, []string{bobACI}},
		{"query name", app.ContactsListRequest{Query: " SMITH "}, []string{aliceACI}},
		{"query profile name", app.ContactsListRequest{Query: "caroline"}, []string{carolACI}},
		{"query number", app.ContactsListRequest{Query: "0102"}, []string{bobACI}},
		{"query ACI", app.ContactsListRequest{Query: "CCCC"}, []string{carolACI}},
		{"blocked and query", app.ContactsListRequest{Blocked: true, Query: "Alice S"}, []string{}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := open(t, contactsFake()).ContactsList(t.Context(), test.req)
			if err != nil {
				t.Fatalf("ContactsList: %v", err)
			}

			if !slices.Equal(acis(got), test.want) {
				t.Errorf("ContactsList = %v, want %v", acis(got), test.want)
			}
		})
	}
}

func TestContactsListError(t *testing.T) {
	t.Parallel()

	fake := contactsFake()
	fake.ContactsErr = errBoom

	_, err := open(t, fake).ContactsList(t.Context(), app.ContactsListRequest{})
	if !errors.Is(err, errBoom) {
		t.Errorf("got %v, want errBoom", err)
	}
}

func TestContactsShow(t *testing.T) {
	t.Parallel()

	tests := []struct {
		arg  string
		want string // ACI
	}{
		{aliceNumber, aliceACI},
		{bobACI, bobACI},
		{app.SelfRecipient, testAccount().ACI},
	}

	for _, test := range tests {
		got, err := open(t, contactsFake()).ContactsShow(t.Context(), test.arg)
		if err != nil || got.ACI != test.want {
			t.Errorf("ContactsShow(%s) = %+v, %v; want %s", test.arg, got, err, test.want)
		}
	}
}

func TestContactsShowErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		arg  string
		want error
	}{
		{"+15550199", signal.ErrUnknownContact},
		// On Signal, but not in the store.
		{"@dave.04", signal.ErrUnknownContact},
		{"@nobody.02", signal.ErrNotOnSignal},
		{app.GroupPrefix + groupID, app.ErrNotAUser},
		{"alice?", app.ErrInvalidRecipient},
	}

	for _, test := range tests {
		_, err := open(t, contactsFake()).ContactsShow(t.Context(), test.arg)
		if !errors.Is(err, test.want) {
			t.Errorf("ContactsShow(%s) error = %v, want %v", test.arg, err, test.want)
		}
	}
}

func TestContactsBlock(t *testing.T) {
	t.Parallel()

	fake := contactsFake()

	// alice by number (resolved), bob is blocked already, dave isn't in the store yet.
	res, err := open(t, fake).ContactsBlock(t.Context(), []string{aliceNumber, bobACI, "@dave.04", aliceACI})
	if err != nil {
		t.Fatalf("ContactsBlock: %v", err)
	}

	if !res.Blocked || len(res.Results) != 3 {
		t.Fatalf("result %+v, want 3 blocked entries", res)
	}

	wantChanged := []bool{true, false, true}
	for i, change := range res.Results {
		if change.Changed != wantChanged[i] || !change.Contact.Blocked {
			t.Errorf("result %d = %+v, want changed %v and blocked", i, change, wantChanged[i])
		}
	}

	if res.Results[0].Contact.ContactName != "Alice Smith" || res.Results[2].Contact.ACI != daveACI {
		t.Errorf("contacts not filled in: %+v", res.Results)
	}

	wantBlockCall(t, fake, 3)
}

// wantBlockCall checks that fake connected once and blocked n users in one call, which sent a
// list of n blocked users.
func wantBlockCall(t *testing.T, fake *signaltest.Fake, n int) {
	t.Helper()

	blocks := fake.Blocks()
	if len(blocks) != 1 || !blocks[0].Blocked || len(blocks[0].Recipients) != n || len(blocks[0].List) != n {
		t.Errorf("SetBlocked calls = %+v", blocks)
	}

	if got := fake.Connects(); len(got) != 1 {
		t.Errorf("connected %d times, want once", len(got))
	}
}

func TestContactsUnblock(t *testing.T) {
	t.Parallel()

	fake := contactsFake()

	res, err := open(t, fake).ContactsUnblock(t.Context(), []string{bobNumber, aliceACI})
	if err != nil {
		t.Fatalf("ContactsUnblock: %v", err)
	}

	if res.Blocked || !res.Results[0].Changed || res.Results[1].Changed || res.Results[0].Contact.Blocked {
		t.Errorf("result %+v, want bob unblocked and alice unchanged", res)
	}

	if blocks := fake.Blocks(); len(blocks) != 1 || len(blocks[0].List) != 0 {
		t.Errorf("SetBlocked calls = %+v, want one with an empty list", blocks)
	}
}

func TestContactsBlockErrors(t *testing.T) {
	t.Parallel()

	unlinked := testAccount()
	unlinked.UnlinkedAt = time.Date(2026, 9, 25, 8, 0, 0, 0, time.UTC)

	tests := []struct {
		name    string
		fake    func() *signaltest.Fake
		args    []string
		want    error
		connect bool
	}{
		{"no args", contactsFake, nil, app.ErrNoRecipients, false},
		{"group", contactsFake, []string{app.GroupPrefix + groupID}, app.ErrNotAUser, false},
		{"self argument", contactsFake, []string{aliceNumber, app.SelfRecipient}, app.ErrNotAUser, false},
		{"invalid", contactsFake, []string{"alice?"}, app.ErrInvalidRecipient, false},
		{"own number", contactsFake, []string{testAccount().Number}, app.ErrNotAUser, true},
		{"not on Signal", contactsFake, []string{"+15550199"}, signal.ErrNotOnSignal, true},
		{"set blocked fails", func() *signaltest.Fake {
			fake := contactsFake()
			fake.SetBlockedErr = signal.ErrStorageKeyUnknown

			return fake
		}, []string{aliceNumber}, signal.ErrStorageKeyUnknown, true},
		{"unlinked", func() *signaltest.Fake {
			fake := contactsFake()
			fake.Linked = []signal.Account{unlinked}

			return fake
		}, []string{aliceNumber}, signal.ErrDeviceUnlinked, false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			fake := test.fake()

			_, err := open(t, fake).ContactsBlock(t.Context(), test.args)
			if !errors.Is(err, test.want) {
				t.Errorf("error = %v, want %v", err, test.want)
			}

			if connected := len(fake.Connects()) > 0; connected != test.connect {
				t.Errorf("connected = %v, want %v", connected, test.connect)
			}

			if len(fake.Blocks()) != 0 {
				t.Errorf("blocked anyway: %+v", fake.Blocks())
			}
		})
	}
}
