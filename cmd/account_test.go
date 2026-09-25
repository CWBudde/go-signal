package cmd_test

import (
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/internal/output"
	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/go-signal/internal/signal/signaltest"
)

const yes = "--yes"

// namedAccount is testAccount with the details that only accounts.json records.
func namedAccount() signal.Account {
	acc := *testAccount()
	acc.DeviceName = "laptop"
	acc.LinkedAt = time.Date(2026, 9, 20, 12, 30, 0, 0, time.UTC)

	return acc
}

func testDevices() []signal.Device {
	return []signal.Device{
		{ID: 1, LastSeen: time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC)},
		{
			ID: 2, Name: "laptop", Current: true,
			Created:  time.Date(2026, 9, 20, 12, 30, 0, 0, time.UTC),
			LastSeen: time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC),
		},
		{ID: 3, Name: "tablet"},
	}
}

func TestAccountShow(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		account signal.Account
		format  output.Format
	}{
		{"account_show", namedAccount(), output.Plain},
		{"account_show_json", namedAccount(), output.JSON},
		{"account_show_unknown", *testAccount(), output.Plain},
		{"account_show_unknown_json", *testAccount(), output.JSON},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			fake := &signaltest.Fake{Linked: []signal.Account{test.account}}

			out, err := run(t, fake, "-o", string(test.format), "account", "show")
			if err != nil {
				t.Fatalf("account show: %v", err)
			}

			golden(t, test.name, out)

			if len(fake.Connects()) != 0 {
				t.Error("account show connected")
			}
		})
	}
}

func TestAccountShowNotLinked(t *testing.T) {
	t.Parallel()

	_, err := run(t, &signaltest.Fake{}, "account", "show")
	if !errors.Is(err, signal.ErrNotLinked) {
		t.Fatalf("got %v, want ErrNotLinked", err)
	}
}

func TestInvalidOutputFormat(t *testing.T) {
	t.Parallel()

	fake := &signaltest.Fake{Linked: []signal.Account{*testAccount()}}

	_, err := run(t, fake, "-o", "yaml", "account", "show")
	if !errors.Is(err, output.ErrInvalidFormat) {
		t.Fatalf("got %v, want ErrInvalidFormat", err)
	}

	if len(fake.Opened()) != 0 {
		t.Error("client opened despite the invalid format")
	}
}

func TestDevicesList(t *testing.T) {
	t.Parallel()

	for _, format := range []string{string(output.Plain), string(output.JSON)} {
		t.Run(format, func(t *testing.T) {
			t.Parallel()

			fake := &signaltest.Fake{Linked: []signal.Account{*testAccount()}, Devices: testDevices()}

			out, err := run(t, fake, "-o", format, "devices", "list")
			if err != nil {
				t.Fatalf("devices list: %v", err)
			}

			golden(t, "devices_list_"+format, out)
		})
	}
}

func TestDevicesListError(t *testing.T) {
	t.Parallel()

	fake := &signaltest.Fake{Linked: []signal.Account{*testAccount()}, DevicesErr: signal.ErrLoggedOut}

	_, err := run(t, fake, "devices", "list")
	if !errors.Is(err, signal.ErrLoggedOut) {
		t.Fatalf("got %v, want ErrLoggedOut", err)
	}
}

func TestAccountUnlink(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string // besides account unlink --yes
	}{
		{"account_unlink", nil},
		{"account_unlink_json", []string{"-o", "json"}},
		{"account_unlink_local", []string{"--local-only"}},
		{"account_unlink_local_json", []string{"-o", "json", "--local-only"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			fake := &signaltest.Fake{Linked: []signal.Account{*testAccount(), secondAccount()}}

			out, err := run(t, fake, append([]string{"-a", testAccount().Number, "account", "unlink", yes}, test.args...)...)
			if err != nil {
				t.Fatalf("account unlink: %v", err)
			}

			golden(t, test.name, out)

			want := []signal.Account{secondAccount()}
			if !slices.Equal(fake.Linked, want) {
				t.Errorf("remaining accounts: %+v", fake.Linked)
			}

			wantCall := signaltest.UnlinkCall{ACI: testAccount().ACI, LocalOnly: slices.Contains(test.args, "--local-only")}
			if got := fake.Unlinks(); len(got) != 1 || got[0] != wantCall {
				t.Errorf("unlinks: %+v", got)
			}
		})
	}
}

func TestAccountUnlinkNeedsYes(t *testing.T) {
	t.Parallel()

	fake := &signaltest.Fake{Linked: []signal.Account{*testAccount()}}

	_, err := run(t, fake, "account", "unlink")
	if err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("got %v, want the --yes guard", err)
	}

	if len(fake.Opened()) != 0 || len(fake.Linked) != 1 {
		t.Error("unlink without --yes touched the account")
	}
}

func TestAccountUnlinkServerError(t *testing.T) {
	t.Parallel()

	errOffline := os.ErrDeadlineExceeded
	fake := &signaltest.Fake{Linked: []signal.Account{*testAccount()}, UnlinkErr: errOffline}

	_, err := run(t, fake, "account", "unlink", yes)
	if !errors.Is(err, errOffline) {
		t.Fatalf("got %v, want the server error", err)
	}

	if len(fake.Linked) != 1 {
		t.Error("local data deleted although the server failed")
	}

	_, err = run(t, fake, "account", "unlink", yes, "--local-only")
	if err != nil {
		t.Fatalf("--local-only: %v", err)
	}

	if len(fake.Linked) != 0 {
		t.Errorf("--local-only kept the account: %+v", fake.Linked)
	}
}

func TestAccountUnlinkInUse(t *testing.T) {
	t.Parallel()

	fake := &signaltest.Fake{Linked: []signal.Account{*testAccount()}, InUse: true}

	_, err := run(t, fake, "account", "unlink", yes)
	if !errors.Is(err, signal.ErrAccountInUse) {
		t.Fatalf("got %v, want ErrAccountInUse", err)
	}
}
