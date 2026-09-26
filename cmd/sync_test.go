package cmd_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/cwbudde/go-signal/cmd"
	"github.com/cwbudde/go-signal/internal/output"
	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/go-signal/internal/signal/signaltest"
)

// linkedLine is what link prints for testAccount.
const linkedLine = "Linked +15550100 (ACI 11111111-1111-1111-1111-111111111111, device 2)\n"

var errStorage = errors.New("storage service unreachable")

// fullSync is a complete sync result.
func fullSync() signal.SyncResult {
	return signal.SyncResult{Contacts: 12, Groups: 3, MasterKey: true, Storage: true, ContactList: true}
}

// timedOut is the error of a sync whose timeout ran out before the contact list arrived.
func timedOut() error {
	return fmt.Errorf("%w (missing contact list): wait for contact list: %w",
		signal.ErrSyncIncomplete, context.DeadlineExceeded)
}

// partialSync is the result that goes with timedOut.
func partialSync() signal.SyncResult {
	return signal.SyncResult{Contacts: 5, Groups: 3, MasterKey: true, Storage: true}
}

func TestLinkSyncs(t *testing.T) {
	t.Parallel()

	// Only link_sync has a golden stdout; the others differ in the summary line alone.
	tests := []struct {
		name    string
		result  signal.SyncResult
		syncErr error
		tail    string
	}{
		{"link_sync", fullSync(), nil, linkedLine + "Synced 12 contacts and 3 groups.\n"},
		{"link_sync_incomplete", partialSync(), timedOut(), linkedLine + "Synced 5 contacts and 3 groups.\n"},
		{"link_sync_failed", signal.SyncResult{}, errStorage, linkedLine},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			fake := &signaltest.Fake{LinkAs: *testAccount(), SyncResult: test.result, SyncErr: test.syncErr}

			out, stderr, err := runStderr(t, t.Context(), fake, "link")
			if err != nil {
				t.Fatalf("link must succeed once linked: %v", err)
			}

			if test.name == "link_sync" {
				golden(t, test.name, out)
			}

			if !strings.HasSuffix(out, test.tail) {
				t.Errorf("stdout doesn't end with %q:\n%s", test.tail, out)
			}

			golden(t, test.name+"_stderr", stderr)

			if got := fake.Syncs(); !slices.Equal(got, []string{testAccount().ACI}) {
				t.Errorf("syncs = %v, want one for the new account", got)
			}
		})
	}
}

func TestLinkWithoutSync(t *testing.T) {
	t.Parallel()

	fake := &signaltest.Fake{LinkAs: *testAccount()}

	out, stderr, err := runStderr(t, t.Context(), fake, "link", "--sync-timeout", "0")
	if err != nil {
		t.Fatalf("link: %v", err)
	}

	if !strings.HasSuffix(out, linkedLine) || stderr != "" {
		t.Errorf("stdout doesn't end with %q or stderr isn't empty:\n%s\nstderr:\n%s", linkedLine, out, stderr)
	}

	if len(fake.Connects()) != 0 || len(fake.Syncs()) != 0 {
		t.Errorf("--sync-timeout 0 connected (%v) or synced (%v)", fake.Connects(), fake.Syncs())
	}
}

func TestAccountSync(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		format  output.Format
		result  signal.SyncResult
		syncErr error
	}{
		{"account_sync", output.Plain, fullSync(), nil},
		{"account_sync_json", output.JSON, fullSync(), nil},
		{"account_sync_incomplete", output.Plain, partialSync(), timedOut()},
		{"account_sync_incomplete_json", output.JSON, partialSync(), timedOut()},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			fake := &signaltest.Fake{
				Linked: []signal.Account{*testAccount()}, SyncResult: test.result, SyncErr: test.syncErr,
			}

			out, stderr, err := runStderr(t, t.Context(), fake,
				"-o", string(test.format), accountCmd, "sync", "--timeout", "5s")
			if err != nil {
				t.Fatalf("account sync: %v", err)
			}

			golden(t, test.name, out)

			if warned := strings.Contains(stderr, "Warning: sync incomplete"); warned != (test.syncErr != nil) {
				t.Errorf("warning on stderr = %v, want %v:\n%s", warned, test.syncErr != nil, stderr)
			}

			if !strings.Contains(stderr, "Sync: requesting contacts from the phone...\n") {
				t.Errorf("no progress on stderr:\n%s", stderr)
			}
		})
	}
}

func TestAccountSyncErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		fake *signaltest.Fake
		want error
		code int
	}{
		{
			"storage", &signaltest.Fake{Linked: []signal.Account{*testAccount()}, SyncErr: errStorage},
			errStorage, cmd.ExitFailure,
		},
		{
			"connect", &signaltest.Fake{Linked: []signal.Account{*testAccount()}, ConnectErr: errStorage},
			errStorage, cmd.ExitFailure,
		},
		{
			"in use", &signaltest.Fake{Linked: []signal.Account{*testAccount()}, InUse: true},
			signal.ErrAccountInUse, cmd.ExitFailure,
		},
		{"logged out", &signaltest.Fake{
			Linked:   []signal.Account{*testAccount()},
			Incoming: []signal.Event{&signal.Connection{State: signal.StateLoggedOut}},
		}, signal.ErrDeviceUnlinked, cmd.ExitUnlinked},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			out, err := run(t, test.fake, accountCmd, "sync")
			if !errors.Is(err, test.want) {
				t.Fatalf("got %v, want %v", err, test.want)
			}

			if code := cmd.ExitCode(err); code != test.code {
				t.Errorf("exit code %d, want %d", code, test.code)
			}

			if out != "" {
				t.Errorf("printed a result after a failure: %q", out)
			}
		})
	}
}
