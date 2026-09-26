package cmd_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/cmd"
	"github.com/cwbudde/go-signal/internal/output"
	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/go-signal/internal/signal/signaltest"
)

const (
	accountCmd = "account"
	unlinkCmd  = "unlink"
)

// unlinkedAccount is namedAccount after go-signal noticed that it was unlinked on the phone.
func unlinkedAccount() signal.Account {
	acc := namedAccount()
	acc.UnlinkedAt = time.Date(2026, 9, 25, 8, 0, 0, 0, time.UTC)

	return acc
}

// wantUnlinked checks that err is the unlinked error with what to do next and maps to
// cmd.ExitUnlinked.
func wantUnlinked(t *testing.T, err error) {
	t.Helper()

	if !errors.Is(err, signal.ErrDeviceUnlinked) {
		t.Fatalf("got %v, want ErrDeviceUnlinked", err)
	}

	msg := err.Error()
	if !strings.Contains(msg, testAccount().Number) || !strings.Contains(msg, "account unlink") {
		t.Errorf("error doesn't name the account and the fix: %s", msg)
	}

	if code := cmd.ExitCode(err); code != cmd.ExitUnlinked {
		t.Errorf("exit code %d, want %d", code, cmd.ExitUnlinked)
	}
}

func TestRemoteUnlink(t *testing.T) {
	t.Parallel()

	fake := &signaltest.Fake{
		Linked:   []signal.Account{namedAccount()},
		Incoming: []signal.Event{&signal.Connection{State: signal.StateLoggedOut}},
	}

	// The first receive hits the logout and marks the account.
	_, err := run(t, fake, "receive", "--timeout", "5s")
	wantUnlinked(t, err)

	if !fake.Linked[0].Unlinked() {
		t.Fatalf("account not marked as unlinked: %+v", fake.Linked[0])
	}

	// The next receive and devices list fail fast without connecting.
	_, err = run(t, fake, "receive", "--timeout", "5s")
	wantUnlinked(t, err)

	_, err = run(t, fake, "devices", "list")
	wantUnlinked(t, err)

	_, err = run(t, fake, accountCmd, "sync")
	wantUnlinked(t, err)

	_, err = run(t, fake, "contacts", "block", "+15550101")
	wantUnlinked(t, err)

	_, err = run(t, fake, "groups", "list")
	wantUnlinked(t, err)

	if got := fake.Connects(); len(got) != 1 {
		t.Errorf("connected %d times, want only the first receive", len(got))
	}

	// account show still works and shows the state.
	out, err := run(t, fake, accountCmd, "show")
	if err != nil || !strings.Contains(out, "Status:      unlinked") {
		t.Errorf("account show: %v\n%s", err, out)
	}

	// account unlink only deletes the local data.
	_, err = run(t, fake, accountCmd, unlinkCmd, yes)
	if err != nil {
		t.Fatalf("account unlink: %v", err)
	}

	wantCall := signaltest.UnlinkCall{ACI: testAccount().ACI, LocalOnly: true}
	if got := fake.Unlinks(); len(got) != 1 || got[0] != wantCall || len(fake.Linked) != 0 {
		t.Errorf("unlinks: %+v, remaining: %+v", got, fake.Linked)
	}
}

func TestDevicesListMarksUnlinked(t *testing.T) {
	t.Parallel()

	fake := &signaltest.Fake{
		Linked:     []signal.Account{*testAccount()},
		DevicesErr: signal.ErrDeviceUnlinked,
	}

	_, err := run(t, fake, "devices", "list")
	wantUnlinked(t, err)

	if !fake.Linked[0].Unlinked() {
		t.Errorf("account not marked as unlinked: %+v", fake.Linked[0])
	}
}

func TestRelinkClearsUnlinked(t *testing.T) {
	t.Parallel()

	fake := &signaltest.Fake{Linked: []signal.Account{unlinkedAccount()}, LinkAs: *testAccount()}

	_, err := run(t, fake, "link")
	if err != nil {
		t.Fatalf("link: %v", err)
	}

	// With nothing incoming, receive runs into its timeout, which is not an error.
	_, err = run(t, fake, "receive", "--timeout", "10ms")
	if err != nil || fake.Linked[0].Unlinked() {
		t.Fatalf("receive after relink: %v, %+v", err, fake.Linked)
	}
}

func TestUnlinkedGolden(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
	}{
		{"account_show_unlinked", []string{accountCmd, "show"}},
		{"account_show_unlinked_json", []string{"-o", string(output.JSON), accountCmd, "show"}},
		{"account_unlink_unlinked", []string{accountCmd, unlinkCmd, yes}},
		{"account_unlink_unlinked_json", []string{"-o", string(output.JSON), accountCmd, unlinkCmd, yes}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			fake := &signaltest.Fake{Linked: []signal.Account{unlinkedAccount()}}

			out, err := run(t, fake, test.args...)
			if err != nil {
				t.Fatalf("%v: %v", test.args, err)
			}

			golden(t, test.name, out)
		})
	}
}

func TestExitCode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		err  error
		want int
	}{
		{nil, cmd.ExitOK},
		{output.ErrInvalidFormat, cmd.ExitFailure},
		{signal.ErrAccountInUse, cmd.ExitFailure},
		{signal.ErrDeviceUnlinked, cmd.ExitUnlinked},
		{signal.UnlinkedError(*testAccount()), cmd.ExitUnlinked},
	}

	for _, test := range tests {
		if got := cmd.ExitCode(test.err); got != test.want {
			t.Errorf("ExitCode(%v) = %d, want %d", test.err, got, test.want)
		}
	}
}
