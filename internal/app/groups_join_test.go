package app_test

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/go-signal/internal/signal/signaltest"
)

const joinFailureHint = "failed"

func appJoinLink() string {
	// Protobuf v1: outer contents, 32-byte master key, 16-byte password.
	contents := append([]byte{0x0a, 0x34, 0x0a, 0x20}, []byte(strings.Repeat("k", 32))...)
	contents = append(contents, 0x12, 0x10)
	contents = append(contents, []byte(strings.Repeat("p", 16))...)

	return "https://signal.group/#" + base64.RawURLEncoding.EncodeToString(contents)
}

func TestGroupsJoinPreflight(t *testing.T) {
	t.Parallel()

	for _, link := range []string{"", "https://signal.group/#SECRET", strings.Repeat("x", 4097)} {
		err := (app.JoinGroupRequest{Link: link}).Check()
		if !errors.Is(err, signal.ErrInvalidGroupInviteLink) {
			t.Fatalf("Check = %v", err)
		}

		_, err = app.New(nil).GroupsJoin(t.Context(), app.JoinGroupRequest{Link: link})
		if !errors.Is(err, signal.ErrInvalidGroupInviteLink) || strings.Contains(err.Error(), "SECRET") {
			t.Fatalf("preflight = %v", err)
		}
	}
}

type joinFailureClient struct {
	signal.Client

	result         signal.GroupJoinResult
	err            error
	calls          int
	observeContext func(context.Context)
	connectOptions signal.ConnectOptions
}

func (c *joinFailureClient) Connect(ctx context.Context, opts ...signal.ConnectOption) error {
	c.observeContext(ctx)
	c.connectOptions = signal.NewConnectOptions(opts...)

	return c.Client.Connect(ctx, opts...) //nolint:wrapcheck // test adapter retains identity
}

func (c *joinFailureClient) JoinGroup(ctx context.Context, _ string) (signal.GroupJoinResult, error) {
	c.calls++
	c.observeContext(ctx)

	return c.result, c.err
}

func TestGroupsJoinNoRetry(t *testing.T) { //nolint:cyclop,funlen // independent result, identity and secrecy assertions
	t.Parallel()

	for _, test := range []struct {
		name   string
		result signal.GroupJoinResult
		cause  error
		hint   string
	}{
		{"definite", signal.GroupJoinResult{ID: groupID, Revision: 5}, signal.ErrGroupChanged, "changed"},
		{
			"transport uncertainty",
			signal.GroupJoinResult{ID: groupID, Revision: 5},
			signal.ErrGroupUpdateUncertain, "attempted revision 5",
		},
		{
			"accepted unverified",
			signal.GroupJoinResult{ID: groupID, Revision: 5, Accepted: true, Changed: true},
			errBoom, "accepted",
		},
		{
			"accepted verified", signal.GroupJoinResult{
				ID: groupID, Title: "Latest", Revision: 8, Status: signal.GroupJoinMember,
				Accepted: true, Changed: true, Verified: true,
			}, errBoom, "revision 8",
		},
		{
			"verified noop", signal.GroupJoinResult{
				ID: groupID, Title: "Latest", Revision: 8, Status: signal.GroupJoinMember, Verified: true,
			}, errBoom, joinFailureHint,
		},
		{"cancelled", signal.GroupJoinResult{}, context.Canceled, joinFailureHint},
		{"device unlink", signal.GroupJoinResult{}, signal.ErrDeviceUnlinked, joinFailureHint},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			fake := &signaltest.Fake{Linked: []signal.Account{testAccount()}}

			client, err := fake.Factory(t.Context(), signal.Options{})
			if err != nil {
				t.Fatal(err)
			}

			t.Cleanup(func() { _ = client.Close() })

			secretErr := &url.Error{
				Op: "PATCH", URL: appJoinLink(),
				Err: fmt.Errorf("secret password=%s: %w", strings.Repeat("p", 16), test.cause),
			}
			contexts := 0
			failing := &joinFailureClient{
				Client: client, result: test.result, err: secretErr,
				observeContext: func(ctx context.Context) {
					if ctx != t.Context() {
						t.Fatal("caller context lost")
					}

					contexts++
				},
			}

			got, err := app.New(failing).GroupsJoin(t.Context(), app.JoinGroupRequest{Link: appJoinLink()})
			if !reflect.DeepEqual(got, test.result) || !errors.Is(err, test.cause) || !errors.Is(err, secretErr) ||
				failing.calls != 1 || contexts != 2 || !failing.connectOptions.SendOnly {
				t.Fatalf("result %v, error %v, calls %d", got, err, failing.calls)
			}

			var urlErr *url.Error
			if !errors.As(err, &urlErr) || !strings.Contains(err.Error(), test.hint) ||
				strings.Contains(err.Error(), "signal.group") || strings.Contains(err.Error(), strings.Repeat("p", 16)) {
				t.Fatalf("unsafe/missing guidance: %v", err)
			}

			if !got.Accepted && strings.Contains(err.Error(), "accepted") {
				t.Fatalf("false acceptance: %v", err)
			}

			if errors.Is(test.cause, signal.ErrGroupUpdateUncertain) &&
				(!strings.Contains(err.Error(), "administrator") || !strings.Contains(err.Error(), "phone") ||
					!strings.Contains(err.Error(), "unavailable until approval")) {
				t.Fatalf("missing pending inspection guidance: %v", err)
			}
		})
	}
}

func TestGroupsJoinConnectError(t *testing.T) {
	t.Parallel()

	secret := &url.Error{Op: "CONNECT", URL: appJoinLink(), Err: context.Canceled}
	fake := &signaltest.Fake{Linked: []signal.Account{testAccount()}, ConnectErr: secret}

	_, err := open(t, fake).GroupsJoin(t.Context(), app.JoinGroupRequest{Link: appJoinLink()})
	if !errors.Is(err, context.Canceled) || !errors.Is(err, secret) || strings.Contains(err.Error(), "signal.group") {
		t.Fatalf("connect error = %v", err)
	}
}

func TestGroupsJoinSelectedAccounts(t *testing.T) { //nolint:cyclop // exact success fields and account-owned effects
	t.Parallel()

	second := signal.Account{ACI: "22222222-2222-4222-8222-222222222222", Number: "+12025550102", DeviceID: 2}
	key := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32)))
	password := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("p", 16)))

	for _, account := range []signal.Account{testAccount(), second} {
		t.Run(account.ACI, func(t *testing.T) {
			t.Parallel()

			fake := &signaltest.Fake{
				Linked:             []signal.Account{testAccount(), second},
				GroupJoinServer:    map[string]signal.Group{key: {ID: groupID, Title: "Fresh", Revision: 4}},
				GroupLinkStates:    map[string]signal.GroupLinkState{groupID: signal.GroupLinkEnabled},
				GroupLinkPasswords: map[string]string{groupID: password},
			}

			client, err := fake.Factory(t.Context(), signal.Options{Account: account.Number})
			if err != nil {
				t.Fatal(err)
			}

			t.Cleanup(func() { _ = client.Close() })

			got, err := app.New(client).GroupsJoin(t.Context(), app.JoinGroupRequest{Link: appJoinLink()})
			if err != nil || got.ID != groupID || got.Title != "Fresh" || got.Status != signal.GroupJoinMember ||
				!got.Accepted || !got.Changed || !got.Verified || got.Revision != 5 {
				t.Fatalf("join %v, %v", got, err)
			}

			if fake.GroupJoinKnownKeys[account.ACI][key] != groupID ||
				len(fake.Connects()) != 1 || fake.Connects()[0] != account.ACI {
				t.Fatal("wrong selected account")
			}
		})
	}
}
