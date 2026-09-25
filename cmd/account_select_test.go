package cmd_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/go-signal/internal/signal/signaltest"
)

func secondAccount() signal.Account {
	return signal.Account{
		Number:   "+15550101",
		ACI:      "33333333-3333-3333-3333-333333333333",
		PNI:      "44444444-4444-4444-4444-444444444444",
		DeviceID: 3,
	}
}

func TestLinkAddsAccount(t *testing.T) {
	t.Parallel()

	fake := &signaltest.Fake{Linked: []signal.Account{*testAccount()}, LinkAs: secondAccount()}

	_, err := run(t, fake, "link")
	if err != nil {
		t.Fatalf("link: %v", err)
	}

	want := []signal.Account{*testAccount(), secondAccount()}
	if !slices.Equal(fake.Linked, want) {
		t.Errorf("got %+v, want %+v", fake.Linked, want)
	}
}

func TestRelinkReplacesAccount(t *testing.T) {
	t.Parallel()

	relinked := *testAccount()
	relinked.DeviceID = 5
	fake := &signaltest.Fake{Linked: []signal.Account{*testAccount(), secondAccount()}, LinkAs: relinked}

	_, err := run(t, fake, "link")
	if err != nil {
		t.Fatalf("link: %v", err)
	}

	want := []signal.Account{secondAccount(), relinked}
	if !slices.Equal(fake.Linked, want) {
		t.Errorf("got %+v, want %+v", fake.Linked, want)
	}
}

func TestReceiveOneAccountWithoutFlag(t *testing.T) {
	t.Parallel()

	fake := &signaltest.Fake{Linked: []signal.Account{*testAccount()}}

	_, err := run(t, fake, "receive", "--timeout", "10ms")
	if err != nil {
		t.Fatalf("receive: %v", err)
	}

	if got := fake.Connects(); !slices.Equal(got, []string{testAccount().ACI}) {
		t.Errorf("connected as %v", got)
	}
}

func TestReceiveTwoAccountsNeedsFlag(t *testing.T) {
	t.Parallel()

	fake := &signaltest.Fake{Linked: []signal.Account{*testAccount(), secondAccount()}}

	_, err := run(t, fake, "receive", "--timeout", "10ms")
	if !errors.Is(err, signal.ErrAccountRequired) {
		t.Fatalf("got %v, want ErrAccountRequired", err)
	}

	if got := fake.Connects(); len(got) != 0 {
		t.Errorf("connected as %v", got)
	}
}

func TestReceiveTwoAccountsSelects(t *testing.T) {
	t.Parallel()

	for _, sel := range []string{secondAccount().Number, secondAccount().ACI} {
		fake := &signaltest.Fake{Linked: []signal.Account{*testAccount(), secondAccount()}}

		_, err := run(t, fake, "receive", "-a", sel, "--timeout", "10ms")
		if err != nil {
			t.Fatalf("-a %s: %v", sel, err)
		}

		if got := fake.Connects(); !slices.Equal(got, []string{secondAccount().ACI}) {
			t.Errorf("-a %s: connected as %v", sel, got)
		}
	}
}

func TestReceiveInvalidAccountFlag(t *testing.T) {
	t.Parallel()

	fake := &signaltest.Fake{Linked: []signal.Account{*testAccount()}}

	_, err := run(t, fake, "receive", "-a", "alice", "--timeout", "10ms")
	if !errors.Is(err, signal.ErrInvalidAccount) {
		t.Fatalf("got %v, want ErrInvalidAccount", err)
	}
}
