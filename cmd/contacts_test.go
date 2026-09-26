package cmd_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/go-signal/internal/signal/signaltest"
)

const (
	contactsCmd = "contacts"
	listCmd     = "list"
	showCmd     = "show"
	blockCmd    = "block"
	bobNumber   = "+15550102"
)

// contactsFake has us, alice (named), bob (number only, blocked) and carol (nickname) in the
// store, and alice and bob on Signal.
func contactsFake() *signaltest.Fake {
	own := testAccount()

	return &signaltest.Fake{
		Linked: []signal.Account{*own},
		Contacts: []signal.Contact{
			{Recipient: signal.Recipient{ACI: own.ACI, Number: own.Number}, ProfileName: "Me"},
			{
				Recipient:   signal.Recipient{ACI: aliceACI, PNI: "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee", Number: aliceNumber},
				ContactName: "Alice Smith", ProfileName: "Ali", Accepted: new(true),
			},
			{Recipient: signal.Recipient{ACI: bobACI, Number: bobNumber}, Blocked: true, Accepted: new(false)},
			{Recipient: signal.Recipient{ACI: carolACI}, ProfileName: "Caroline", Nickname: "carol"},
		},
		Directory: []signal.Recipient{{ACI: aliceACI, Number: aliceNumber}, {ACI: bobACI, Number: bobNumber}},
	}
}

func TestContactsGolden(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
	}{
		{"contacts_list", []string{contactsCmd, listCmd}},
		{"contacts_list_json", []string{"-o", formatJSON, contactsCmd, listCmd}},
		{"contacts_list_blocked", []string{contactsCmd, listCmd, "--blocked"}},
		{"contacts_show", []string{contactsCmd, showCmd, aliceNumber}},
		{"contacts_show_json", []string{"-o", formatJSON, contactsCmd, showCmd, aliceACI}},
		{"contacts_show_nickname", []string{contactsCmd, showCmd, carolACI}},
		{"contacts_block", []string{contactsCmd, blockCmd, aliceNumber, bobACI}},
		{"contacts_block_json", []string{"-o", formatJSON, contactsCmd, blockCmd, aliceNumber, bobACI}},
		{"contacts_unblock", []string{contactsCmd, "unblock", bobNumber, aliceNumber}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			out, err := run(t, contactsFake(), test.args...)
			if err != nil {
				t.Fatalf("%v: %v", test.args, err)
			}

			golden(t, test.name, out)
		})
	}
}

func TestContactsListQuery(t *testing.T) {
	t.Parallel()

	out, err := run(t, contactsFake(), contactsCmd, listCmd, "-q", "CAROL")
	if err != nil {
		t.Fatal(err)
	}

	if lines := strings.Split(strings.TrimSpace(out), "\n"); len(lines) != 2 || !strings.HasPrefix(lines[1], "carol ") {
		t.Errorf("got\n%s\nwant only carol", out)
	}
}

func TestContactsBlockSendsList(t *testing.T) {
	t.Parallel()

	fake := contactsFake()

	_, err := run(t, fake, contactsCmd, blockCmd, aliceNumber)
	if err != nil {
		t.Fatal(err)
	}

	blocks := fake.Blocks()
	if len(blocks) != 1 || len(blocks[0].List) != 2 || blocks[0].List[0].ACI != aliceACI {
		t.Errorf("SetBlocked calls = %+v, want one with alice and bob", blocks)
	}
}

func TestContactsErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		args []string
		want error
	}{
		{[]string{contactsCmd, showCmd, "+15550199"}, signal.ErrUnknownContact},
		{[]string{contactsCmd, showCmd, app.GroupPrefix + groupID}, app.ErrNotAUser},
		{[]string{contactsCmd, blockCmd, app.SelfRecipient}, app.ErrNotAUser},
		{[]string{contactsCmd, "unblock", "+15550199"}, signal.ErrNotOnSignal},
	}

	for _, test := range tests {
		fake := contactsFake()

		_, err := run(t, fake, test.args...)
		if !errors.Is(err, test.want) {
			t.Errorf("%v: got %v, want %v", test.args, err, test.want)
		}

		if len(fake.Blocks()) != 0 {
			t.Errorf("%v: blocked anyway", test.args)
		}
	}

	// block needs at least one user.
	_, err := run(t, contactsFake(), contactsCmd, blockCmd)
	if err == nil {
		t.Error("block without users succeeded")
	}
}

func TestSendShowsNames(t *testing.T) {
	t.Parallel()

	fake := sendFake()
	fake.Contacts = contactsFake().Contacts

	out, err := runSend(t, fake, "", sendCmd, aliceNumber, "-m", "hi")
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(out, "Alice Smith ") {
		t.Errorf("send output without alice's name:\n%s", out)
	}
}
