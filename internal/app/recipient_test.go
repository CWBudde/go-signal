package app_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/go-signal/internal/signal/signaltest"
)

const (
	aliceNumber = "+4915199999999"
	aliceACI    = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	bobACI      = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	carolACI    = "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
	bobUsername = "@bob.42"
	groupID     = "Z3JvdXAtaWQtZ3JvdXAtaWQtZ3JvdXAtaWQtZ3JvdXA="
)

// directory returns a fake with the test account and alice (by number) and bob (by username)
// on Signal.
func directory() *signaltest.Fake {
	return &signaltest.Fake{
		Linked: []signal.Account{testAccount()},
		Directory: []signal.Recipient{
			{ACI: aliceACI, PNI: carolACI, Number: aliceNumber},
			{ACI: bobACI, Username: "bob.42"},
		},
	}
}

// connected returns an App on a connected client of fake.
func connected(t *testing.T, fake *signaltest.Fake) *app.App {
	t.Helper()

	client, err := fake.Factory(t.Context(), signal.Options{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	t.Cleanup(func() { _ = client.Close() })

	err = client.Connect(t.Context())
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	return app.New(client)
}

func TestParseRecipient(t *testing.T) {
	t.Parallel()

	tests := []struct {
		arg  string
		want app.Target
	}{
		{"+4915112345678", app.Target{Recipient: signal.Recipient{Number: "+4915112345678"}}},
		{" +12025550123 ", app.Target{Recipient: signal.Recipient{Number: "+12025550123"}}},
		{strings.ToUpper(aliceACI), app.Target{Recipient: signal.Recipient{ACI: aliceACI}}},
		{bobUsername, app.Target{Recipient: signal.Recipient{Username: "bob.42"}}},
		{app.SelfRecipient, app.Target{Self: true}},
		{"SELF", app.Target{Self: true}},
		{"group:" + groupID, app.Target{GroupID: groupID}},
		// The URL-safe alphabet of group links.
		{"group:Z3JvdXAtaWQtZ3JvdXAtaWQtZ3JvdXAtaWQtZ3JvdXA", app.Target{GroupID: groupID}},
	}

	for _, test := range tests {
		got, err := app.ParseRecipient(test.arg)
		if err != nil {
			t.Errorf("ParseRecipient(%q): %v", test.arg, err)

			continue
		}

		if got != test.want {
			t.Errorf("ParseRecipient(%q) = %+v, want %+v", test.arg, got, test.want)
		}
	}
}

func TestParseRecipientInvalid(t *testing.T) {
	t.Parallel()

	for _, arg := range []string{
		"", "015112345678", "+0151", "+49 151 12345678", "+1234567890123456", "@", "@bob 42",
		"aaaaaaaa-aaaa-4aaa-8aaa", "{" + aliceACI + "}", "group:", "group:c2hvcnQ=", "bob",
	} {
		_, err := app.ParseRecipient(arg)
		if !errors.Is(err, app.ErrInvalidRecipient) {
			t.Errorf("ParseRecipient(%q) error = %v, want ErrInvalidRecipient", arg, err)
		}
	}
}

func TestTargetString(t *testing.T) {
	t.Parallel()

	for _, arg := range []string{"+4915112345678", aliceACI, bobUsername, app.SelfRecipient, "group:" + groupID} {
		target, err := app.ParseRecipient(arg)
		if err != nil {
			t.Fatalf("ParseRecipient(%q): %v", arg, err)
		}

		if got := target.String(); got != arg {
			t.Errorf("String() = %q, want %q", got, arg)
		}
	}
}

func TestResolveRecipients(t *testing.T) {
	t.Parallel()

	own := testAccount()
	self := app.Target{Self: true, Recipient: signal.Recipient{ACI: own.ACI, Number: own.Number}}

	got, err := connected(t, directory()).ResolveRecipients(t.Context(), []string{
		aliceNumber, "@Bob.42", carolACI, "group:" + groupID, app.SelfRecipient,
		// Duplicates: alice by ACI, and self by own number and ACI.
		aliceACI, own.Number, own.ACI,
	})
	if err != nil {
		t.Fatalf("ResolveRecipients: %v", err)
	}

	want := []app.Target{
		{Recipient: signal.Recipient{ACI: aliceACI, PNI: carolACI, Number: aliceNumber}},
		{Recipient: signal.Recipient{ACI: bobACI, Username: "Bob.42"}},
		{Recipient: signal.Recipient{ACI: carolACI}},
		{GroupID: groupID},
		self,
	}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("ResolveRecipients =\n%+v\nwant\n%+v", got, want)
	}
}

func TestResolveRecipientsNotOnSignal(t *testing.T) {
	t.Parallel()

	_, err := connected(t, directory()).ResolveRecipients(t.Context(),
		[]string{aliceNumber, "+4915100000000", "@nobody.01"})
	if !errors.Is(err, signal.ErrNotOnSignal) {
		t.Fatalf("ResolveRecipients error = %v, want ErrNotOnSignal", err)
	}

	for _, missing := range []string{"+4915100000000", "@nobody.01"} {
		if !strings.Contains(err.Error(), missing) {
			t.Errorf("error %q doesn't name %s", err, missing)
		}
	}

	if strings.Contains(err.Error(), aliceNumber) {
		t.Errorf("error %q names a known recipient", err)
	}
}

func TestResolveRecipientsInvalid(t *testing.T) {
	t.Parallel()

	_, err := connected(t, directory()).ResolveRecipients(t.Context(), []string{"bob", aliceNumber, "+0"})
	if !errors.Is(err, app.ErrInvalidRecipient) {
		t.Fatalf("ResolveRecipients error = %v, want ErrInvalidRecipient", err)
	}

	if !strings.Contains(err.Error(), `"bob"`) || !strings.Contains(err.Error(), `"+0"`) {
		t.Errorf("error %q doesn't name both invalid arguments", err)
	}
}

func TestResolveRecipientsOffline(t *testing.T) {
	t.Parallel()

	// Without Connect, usernames, ACIs, groups and self still resolve; numbers don't.
	got, err := open(t, directory()).ResolveRecipients(t.Context(),
		[]string{bobUsername, carolACI, "group:" + groupID, testAccount().Number})
	if err != nil {
		t.Fatalf("ResolveRecipients: %v", err)
	}

	if len(got) != 4 || got[0].Recipient.ACI != bobACI || !got[3].Self {
		t.Errorf("ResolveRecipients = %+v", got)
	}

	_, err = open(t, directory()).ResolveRecipients(t.Context(), []string{aliceNumber})
	if !errors.Is(err, signal.ErrNotConnected) {
		t.Errorf("ResolveRecipients error = %v, want ErrNotConnected", err)
	}
}

func TestResolveRecipientsNotLinked(t *testing.T) {
	t.Parallel()

	_, err := open(t, &signaltest.Fake{}).ResolveRecipients(t.Context(), []string{aliceACI})
	if !errors.Is(err, signal.ErrNotLinked) {
		t.Errorf("ResolveRecipients error = %v, want ErrNotLinked", err)
	}

	// A group needs no account lookup.
	got, err := open(t, &signaltest.Fake{}).ResolveRecipients(t.Context(), []string{"group:" + groupID})
	if err != nil || len(got) != 1 {
		t.Errorf("ResolveRecipients = %+v, %v", got, err)
	}
}
