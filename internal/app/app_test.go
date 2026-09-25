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

var errBoom = errors.New("boom")

func testAccount() signal.Account {
	return signal.Account{
		Number:   "+4915112345678",
		ACI:      "11111111-2222-3333-4444-555555555555",
		DeviceID: 2,
	}
}

// open returns an App on a client of fake, closed when the test ends.
func open(t *testing.T, fake *signaltest.Fake) *app.App {
	t.Helper()

	client, err := fake.Factory(t.Context(), signal.Options{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	t.Cleanup(func() {
		err := client.Close()
		if err != nil {
			t.Errorf("close: %v", err)
		}
	})

	return app.New(client)
}

func TestAccountShow(t *testing.T) {
	t.Parallel()

	fake := &signaltest.Fake{Linked: []signal.Account{testAccount()}}

	acc, err := open(t, fake).AccountShow(t.Context())
	if err != nil {
		t.Fatalf("AccountShow: %v", err)
	}

	if acc != testAccount() {
		t.Errorf("AccountShow = %+v, want %+v", acc, testAccount())
	}
}

func TestAccountShowNotLinked(t *testing.T) {
	t.Parallel()

	_, err := open(t, &signaltest.Fake{}).AccountShow(t.Context())
	if !errors.Is(err, signal.ErrNotLinked) {
		t.Errorf("AccountShow error = %v, want ErrNotLinked", err)
	}
}

func TestDevicesList(t *testing.T) {
	t.Parallel()

	devices := []signal.Device{{ID: 1}, {ID: 2, Name: "laptop", Current: true}}
	fake := &signaltest.Fake{Linked: []signal.Account{testAccount()}, Devices: devices}

	got, err := open(t, fake).DevicesList(t.Context())
	if err != nil {
		t.Fatalf("DevicesList: %v", err)
	}

	if !slices.Equal(got, devices) {
		t.Errorf("DevicesList = %+v, want %+v", got, devices)
	}
}

func TestDevicesListError(t *testing.T) {
	t.Parallel()

	fake := &signaltest.Fake{Linked: []signal.Account{testAccount()}, DevicesErr: errBoom}

	_, err := open(t, fake).DevicesList(t.Context())
	if !errors.Is(err, errBoom) {
		t.Errorf("DevicesList error = %v, want %v", err, errBoom)
	}
}

func TestAccountUnlink(t *testing.T) {
	t.Parallel()

	unlinked := testAccount()
	unlinked.UnlinkedAt = time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name          string
		account       signal.Account
		localOnly     bool
		wantLocalOnly bool
	}{
		{"server", testAccount(), false, false},
		{"local only", testAccount(), true, true},
		{"already unlinked", unlinked, false, true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			fake := &signaltest.Fake{Linked: []signal.Account{test.account}}

			res, err := open(t, fake).AccountUnlink(t.Context(), app.UnlinkRequest{LocalOnly: test.localOnly})
			if err != nil {
				t.Fatalf("AccountUnlink: %v", err)
			}

			if res.Account.ACI != test.account.ACI || res.LocalOnly != test.wantLocalOnly {
				t.Errorf("AccountUnlink = %+v, want ACI %s, LocalOnly %v",
					res, test.account.ACI, test.wantLocalOnly)
			}
		})
	}
}

func TestAccountUnlinkInUse(t *testing.T) {
	t.Parallel()

	fake := &signaltest.Fake{Linked: []signal.Account{testAccount()}, InUse: true}

	_, err := open(t, fake).AccountUnlink(t.Context(), app.UnlinkRequest{})
	if !errors.Is(err, signal.ErrAccountInUse) {
		t.Errorf("AccountUnlink error = %v, want ErrAccountInUse", err)
	}
}
