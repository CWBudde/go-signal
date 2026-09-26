package app_test

import (
	"slices"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/signal"
)

// Names in the names tests.
const (
	ali       = "Ali"
	aliceName = "Alice"
	bob       = "Bob"
)

func TestContactDisplayName(t *testing.T) {
	t.Parallel()

	alice := signal.Recipient{ACI: aliceACI}

	tests := []struct {
		contact    signal.Contact
		name, want string
	}{
		{signal.Contact{Recipient: alice, Nickname: ali, ContactName: aliceName, ProfileName: "A"}, ali, ali},
		{signal.Contact{Recipient: alice, ContactName: aliceName, ProfileName: "A"}, aliceName, aliceName},
		{signal.Contact{Recipient: alice, ProfileName: "A"}, "A", "A"},
		{signal.Contact{Recipient: signal.Recipient{ACI: aliceACI, Number: aliceNumber}}, "", aliceNumber},
		{signal.Contact{Recipient: signal.Recipient{ACI: aliceACI}}, "", aliceACI},
		{signal.Contact{Recipient: signal.Recipient{PNI: bobACI}}, "", "PNI:" + bobACI},
	}

	for _, test := range tests {
		if got := test.contact.Name(); got != test.name {
			t.Errorf("Name(%+v) = %q, want %q", test.contact, got, test.name)
		}

		if got := test.contact.DisplayName(); got != test.want {
			t.Errorf("DisplayName(%+v) = %q, want %q", test.contact, got, test.want)
		}
	}
}

func TestNames(t *testing.T) {
	t.Parallel()

	own := testAccount().ACI
	names := app.NewNames(own, []signal.Contact{
		{Recipient: signal.Recipient{ACI: aliceACI, Number: aliceNumber}, ContactName: aliceName},
		// Two contacts called Bob: the number, else the start of the ACI tells them apart.
		{Recipient: signal.Recipient{ACI: bobACI, Number: bobNumber}, ProfileName: bob},
		{Recipient: signal.Recipient{ACI: carolACI}, ProfileName: bob},
		{Recipient: signal.Recipient{ACI: daveACI, Number: "+15550104"}},
	})

	tests := []struct {
		rcpt        signal.Recipient
		name, label string
	}{
		{signal.Recipient{ACI: aliceACI}, aliceName, aliceName},
		{signal.Recipient{Number: aliceNumber}, aliceName, aliceName},
		// An unknown ACI falls back to the number.
		{signal.Recipient{ACI: "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee", Number: aliceNumber}, aliceName, aliceName},
		{signal.Recipient{ACI: bobACI}, bob, "Bob (" + bobNumber + ")"},
		{signal.Recipient{ACI: carolACI}, bob, "Bob (cccccccc)"},
		{signal.Recipient{ACI: daveACI}, "", "+15550104"},
		{signal.Recipient{ACI: "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee"}, "", ""},
		{signal.Recipient{}, "", ""},
	}

	for _, test := range tests {
		if got := names.Name(test.rcpt); got != test.name {
			t.Errorf("Name(%+v) = %q, want %q", test.rcpt, got, test.name)
		}

		if got := names.Label(test.rcpt); got != test.label {
			t.Errorf("Label(%+v) = %q, want %q", test.rcpt, got, test.label)
		}
	}

	if !names.IsSelf(signal.Recipient{ACI: own}) || names.IsSelf(signal.Recipient{ACI: aliceACI}) {
		t.Error("IsSelf is wrong")
	}

	var zero app.Names
	if zero.Name(signal.Recipient{ACI: aliceACI}) != "" || zero.Label(signal.Recipient{ACI: aliceACI}) != "" ||
		zero.IsSelf(signal.Recipient{}) {
		t.Error("the zero Names knows something")
	}
}

func TestAppNames(t *testing.T) {
	t.Parallel()

	names, err := open(t, contactsFake()).Names(t.Context())
	if err != nil {
		t.Fatalf("Names: %v", err)
	}

	if got := names.Name(signal.Recipient{ACI: carolACI}); got != "carol" {
		t.Errorf("carol's name = %q", got)
	}

	if !names.IsSelf(signal.Recipient{ACI: testAccount().ACI}) {
		t.Error("the own account is not self")
	}
}

func TestNameBookRefresh(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	fake := contactsFake()

	client, err := fake.Factory(t.Context(), signal.Options{})
	if err != nil {
		t.Fatal(err)
	}

	defer client.Close()

	book, err := app.New(client, app.WithClock(func() time.Time { return now })).NameBook(t.Context())
	if err != nil {
		t.Fatalf("NameBook: %v", err)
	}

	dave := signal.Recipient{ACI: daveACI}
	fromDave := &signal.Message{Envelope: signal.Envelope{Sender: dave, Chat: signal.Chat{Recipient: dave}}}
	fromAlice := &signal.Message{Envelope: signal.Envelope{
		Sender: signal.Recipient{ACI: aliceACI}, Chat: signal.Chat{Recipient: signal.Recipient{ACI: aliceACI}},
	}}

	// dave's profile arrives during receive; the store has it on the next contacts read.
	fake.Contacts = append(fake.Contacts, signal.Contact{Recipient: dave, ProfileName: "Dave"})

	steps := []struct {
		after  time.Duration
		evt    signal.Event
		reload bool
	}{
		{time.Second, fromDave, false},             // too soon after loading
		{app.NameReloadInterval, fromAlice, false}, // alice has a name
		{app.NameReloadInterval, &signal.QueueEmpty{}, false},
		{app.NameReloadInterval, fromDave, true},                // a user without a name
		{app.NameReloadInterval + time.Second, fromDave, false}, // too soon after reloading
		{3 * app.NameReloadInterval, fromDave, false},           // dave has a name now
	}

	for i, step := range steps {
		now = time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC).Add(step.after)

		reloaded, err := book.Refresh(t.Context(), step.evt)
		if err != nil || reloaded != step.reload {
			t.Errorf("step %d: Refresh = %v, %v; want %v", i, reloaded, err, step.reload)
		}
	}

	if got := book.Names().Name(dave); got != "Dave" {
		t.Errorf("dave's name after reload = %q", got)
	}
}

func TestEventRecipients(t *testing.T) {
	t.Parallel()

	alice, bob := signal.Recipient{ACI: aliceACI}, signal.Recipient{ACI: bobACI}
	env := signal.Envelope{Sender: alice, Chat: signal.Chat{Recipient: alice}}

	tests := []struct {
		evt  signal.Event
		want []signal.Recipient
	}{
		{&signal.Message{Envelope: env, Quote: &signal.Quote{Author: bob}}, []signal.Recipient{alice, alice, bob}},
		{&signal.Reaction{Envelope: env, TargetAuthor: bob}, []signal.Recipient{alice, alice, bob}},
		{&signal.Receipt{Sender: bob}, []signal.Recipient{bob}},
		{&signal.ReadSync{Messages: []signal.ReadMark{{Sender: alice}, {Sender: bob}}}, []signal.Recipient{alice, bob}},
		{&signal.Connection{}, nil},
	}

	for _, test := range tests {
		if got := app.EventRecipients(test.evt); !slices.Equal(got, test.want) {
			t.Errorf("EventRecipients(%T) = %v, want %v", test.evt, got, test.want)
		}
	}
}
