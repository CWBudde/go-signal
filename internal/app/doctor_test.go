package app_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/go-signal/internal/signal/signaltest"
)

// serverFailed are the checks when only the server check fails.
const serverFailed = "account=ok server=fail inbox=ok"

// statuses renders checks as "name=status" for comparison.
func statuses(checks []app.Check) string {
	parts := make([]string, 0, len(checks))
	for _, check := range checks {
		parts = append(parts, check.Name+"="+string(check.Status))
	}

	return strings.Join(parts, " ")
}

func TestDoctor(t *testing.T) { //nolint:funlen // table-driven
	t.Parallel()

	current := []signal.Device{{ID: 1}, {ID: 2, Current: true}}
	unlinked := testAccount()
	unlinked.UnlinkedAt = time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name     string
		fake     *signaltest.Fake
		req      app.DoctorRequest
		want     string
		wantErr  error
		wantHint bool
	}{
		{
			name: "local only",
			fake: &signaltest.Fake{Linked: []signal.Account{testAccount()}},
			want: "account=ok inbox=ok",
		},
		{
			name: "healthy",
			fake: &signaltest.Fake{Linked: []signal.Account{testAccount()}, Devices: current},
			req:  app.DoctorRequest{Lock: true, Server: true},
			want: "account=ok lock=ok server=ok inbox=ok",
		},
		{
			name:     "not linked",
			fake:     &signaltest.Fake{},
			req:      app.DoctorRequest{Lock: true, Server: true},
			want:     "account=fail",
			wantErr:  signal.ErrNotLinked,
			wantHint: true,
		},
		{
			name:     "marked unlinked",
			fake:     &signaltest.Fake{Linked: []signal.Account{unlinked}},
			req:      app.DoctorRequest{Server: true},
			want:     "account=fail",
			wantErr:  signal.ErrDeviceUnlinked,
			wantHint: true,
		},
		{
			name: "lock held",
			fake: &signaltest.Fake{Linked: []signal.Account{testAccount()}, InUse: true},
			req:  app.DoctorRequest{Lock: true},
			want: "account=ok lock=warn inbox=ok",
		},
		{
			name: "unlinked on the server",
			fake: &signaltest.Fake{
				Linked:     []signal.Account{testAccount()},
				DevicesErr: fmt.Errorf("%w: 403", signal.ErrDeviceUnlinked),
			},
			req:      app.DoctorRequest{Server: true},
			want:     serverFailed,
			wantErr:  signal.ErrDeviceUnlinked,
			wantHint: true,
		},
		{
			name:     "server unreachable",
			fake:     &signaltest.Fake{Linked: []signal.Account{testAccount()}, DevicesErr: errBoom},
			req:      app.DoctorRequest{Server: true},
			want:     serverFailed,
			wantErr:  errBoom,
			wantHint: true,
		},
		{
			name:     "device missing",
			fake:     &signaltest.Fake{Linked: []signal.Account{testAccount()}, Devices: []signal.Device{{ID: 1}}},
			req:      app.DoctorRequest{Server: true},
			want:     serverFailed,
			wantErr:  signal.ErrDeviceUnlinked,
			wantHint: true,
		},
		{
			name:    "inbox broken",
			fake:    &signaltest.Fake{Linked: []signal.Account{testAccount()}, InboxErr: errBoom},
			want:    "account=ok inbox=fail",
			wantErr: errBoom,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			checks := open(t, test.fake).Doctor(t.Context(), test.req)
			if got := statuses(checks); got != test.want {
				t.Errorf("checks = %s, want %s", got, test.want)
			}

			checkDoctorError(t, app.DoctorError(checks), test.wantErr)

			if hint := failureHint(checks); hint != test.wantHint {
				t.Errorf("failure hint = %v, want %v: %+v", hint, test.wantHint, checks)
			}
		})
	}
}

// checkDoctorError checks that err is nil, or wraps ErrUnhealthy and want if want is set.
func checkDoctorError(t *testing.T, err, want error) {
	t.Helper()

	switch {
	case want == nil && err != nil:
		t.Errorf("DoctorError = %v, want nil", err)
	case want != nil && (!errors.Is(err, app.ErrUnhealthy) || !errors.Is(err, want)):
		t.Errorf("DoctorError = %v, want ErrUnhealthy and %v", err, want)
	}
}

// failureHint reports whether a failed check has a hint.
func failureHint(checks []app.Check) bool {
	for _, check := range checks {
		if check.Status == app.CheckFail && check.Hint != "" {
			return true
		}
	}

	return false
}

func TestDoctorInboxCounts(t *testing.T) {
	t.Parallel()

	fake := &signaltest.Fake{Linked: []signal.Account{testAccount()}}
	a := open(t, fake)

	checks := a.Doctor(t.Context(), app.DoctorRequest{})
	if got := checks[len(checks)-1].Detail; got != "0 entries in 0 chats, 0 unread" {
		t.Errorf("inbox detail = %q", got)
	}
}

func TestDoctorLockHeldByOwnClient(t *testing.T) {
	t.Parallel()

	fake := &signaltest.Fake{Linked: []signal.Account{testAccount()}}

	client, err := fake.Factory(t.Context(), signal.Options{})
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = client.Close() })

	err = client.Connect(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	// Our own connection holds the lock: that's fine.
	checks := app.New(client).Doctor(t.Context(), app.DoctorRequest{Lock: true})
	if got := statuses(checks); got != "account=ok lock=ok inbox=ok" {
		t.Errorf("own client: checks = %s", got)
	}

	// Another client sees it held.
	checks = open(t, fake).Doctor(t.Context(), app.DoctorRequest{Lock: true})
	if got := statuses(checks); got != "account=ok lock=warn inbox=ok" {
		t.Errorf("other client: checks = %s", got)
	}
}

func TestPolicyCheck(t *testing.T) {
	t.Parallel()

	all, err := app.ParseAllowlist([]string{app.AllowAll})
	if err != nil {
		t.Fatal(err)
	}

	two, err := app.ParseAllowlist([]string{"+4915111111111", testAccount().Number})
	if err != nil {
		t.Fatal(err)
	}

	none, err := app.ParseAllowlist(nil)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name       string
		check      app.Check
		wantStatus app.CheckStatus
		wantDetail string
	}{
		{"read-only", app.PolicyCheck(true, none, "", false), app.CheckOK, "read-only: no tool sends"},
		{"unrestricted", app.PolicyCheck(false, nil, "", false), app.CheckWarn, "sends to anyone; no attachments"},
		{
			"allow all", app.PolicyCheck(false, all, "/att", true), app.CheckWarn,
			"sends to anyone; attachments from /att; the user confirms each message",
		},
		{"allow nobody", app.PolicyCheck(false, none, "", false), app.CheckWarn, "sends to nobody; no attachments"},
		{"two", app.PolicyCheck(false, two, "", false), app.CheckOK, "sends to 2 allowed recipients; no attachments"},
	}

	for _, test := range tests {
		if test.check.Status != test.wantStatus || test.check.Detail != test.wantDetail {
			t.Errorf("%s: got %s %q, want %s %q",
				test.name, test.check.Status, test.check.Detail, test.wantStatus, test.wantDetail)
		}
	}
}
