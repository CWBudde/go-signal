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

const (
	appAcceptNoop             = "noop"
	appAcceptFreshTitle       = "Fresh"
	appAcceptConnectStage     = "connect"
	appAcceptUnknownStage     = "unknown"
	appAcceptOperationStage   = "operation"
	appAcceptAcceptedHint     = "accepted"
	appAcceptBeforeSubmission = "before submission"
)

func TestGroupAcceptRequestCheck(t *testing.T) {
	t.Parallel()

	for _, ref := range []string{
		"", " \t\n", strings.Repeat(" ", 4097), "group:SECRET", "group:",
		"https://signal.group/#SECRET", "sgnl://signal.group/#SECRET",
		"https://signal.group/#%zz", "sgnl://signal.group/#%zz",
	} {
		err := (app.GroupAcceptRequest{Group: ref}).Check()
		if !errors.Is(err, signal.ErrUnknownGroup) {
			t.Fatalf("invalid reference accepted: %v", err)
		}

		_, err = app.New(nil).GroupsAccept(t.Context(), app.GroupAcceptRequest{Group: ref})
		if !errors.Is(err, signal.ErrUnknownGroup) || strings.Contains(err.Error(), "SECRET") ||
			strings.Contains(err.Error(), "signal.group") {
			t.Fatalf("unsafe preflight: %v", err)
		}
	}

	for _, ref := range []string{
		" Family ", groupID, "group:" + groupID, masterKey,
		base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("k", 32))),
	} {
		err := (app.GroupAcceptRequest{Group: ref}).Check()
		if err != nil {
			t.Fatalf("valid reference rejected: %v", err)
		}
	}
}

type acceptObservingClient struct {
	signal.Client

	calls          int
	connects       int
	refs           []string
	result         *signal.GroupAcceptResult
	operationErr   error
	connectOptions signal.ConnectOptions
	observeContext func(context.Context)
}

func (c *acceptObservingClient) Connect(ctx context.Context, opts ...signal.ConnectOption) error {
	c.connects++
	c.observeContext(ctx)
	c.connectOptions = signal.NewConnectOptions(opts...)

	return c.Client.Connect(ctx, opts...) //nolint:wrapcheck // retain injected error identity
}

func (c *acceptObservingClient) AcceptGroupInvitation(
	ctx context.Context, ref string,
) (signal.GroupAcceptResult, error) {
	c.calls++
	c.refs = append(c.refs, ref)
	c.observeContext(ctx)

	if c.result != nil {
		return *c.result, c.operationErr
	}

	return c.Client.AcceptGroupInvitation(ctx, ref) //nolint:wrapcheck // retain real fake behavior
}

// TestGroupsAccept catches wrong account/ref selection, duplicate operations and loss of fresh no-op evidence.
func TestGroupsAccept(t *testing.T) { //nolint:funlen,cyclop,gocognit // account, reference and membership matrix
	t.Parallel()

	first := testAccount()
	first.PNI = "33333333-3333-4333-8333-333333333333"
	second := signal.Account{
		ACI: "22222222-2222-4222-8222-222222222222",
		PNI: "44444444-4444-4444-8444-444444444444", Number: "+12025550102", DeviceID: 2,
	}

	for _, account := range []signal.Account{first, second} {
		for _, kind := range []string{"ACI", "PNI", appAcceptNoop} {
			for _, ref := range []string{" Family ", "group:" + groupID, groupID, masterKey} {
				t.Run(account.ACI+"/"+kind+"/"+ref, func(t *testing.T) {
					t.Parallel()

					pending := signal.Recipient{ACI: account.ACI}
					if kind == "PNI" {
						pending = signal.Recipient{PNI: account.PNI}
					}

					group := signal.Group{
						ID: groupID, Title: appAcceptFreshTitle, Revision: 7,
						Pending: []signal.PendingMember{{Recipient: pending, Role: signal.GroupRoleMember}},
					}
					if kind == appAcceptNoop {
						group.Members = []signal.GroupMember{{
							Recipient: signal.Recipient{ACI: account.ACI},
							Role:      signal.GroupRoleMember,
						}}
					}

					fake := &signaltest.Fake{
						Linked:             []signal.Account{first, second},
						GroupJoinServer:    map[string]signal.Group{masterKey: group},
						GroupJoinKnownKeys: map[string]map[string]string{account.ACI: {masterKey: groupID}},
						GroupJoinTitleCache: map[string]map[string]signal.CachedGroup{
							account.ACI: {groupID: {Title: familyTitle}},
						},
					}
					if kind == appAcceptNoop {
						fake.AcceptGroupInvitationErr = signal.ErrGroupChanged
					}

					client, err := fake.Factory(t.Context(), signal.Options{Account: account.Number})
					if err != nil {
						t.Fatal(err)
					}

					t.Cleanup(func() { _ = client.Close() })

					contexts := 0
					observing := &acceptObservingClient{Client: client, observeContext: func(ctx context.Context) {
						if ctx != t.Context() {
							t.Fatal("caller context lost")
						}

						contexts++
					}}
					got, err := app.New(observing).GroupsAccept(t.Context(), app.GroupAcceptRequest{Group: ref})

					want := signal.GroupAcceptResult{
						ID: groupID, Title: appAcceptFreshTitle, Revision: 8,
						Changed: true, Accepted: true, Verified: true,
					}
					if kind == appAcceptNoop {
						want.Revision, want.Changed, want.Accepted = 7, false, false
					}

					wantRef := groupID
					if ref == masterKey {
						wantRef = masterKey
					}

					if err != nil || got != want || observing.calls != 1 || observing.connects != 1 ||
						!observing.connectOptions.SendOnly || contexts != 2 ||
						!reflect.DeepEqual(observing.refs, []string{wantRef}) {
						t.Fatalf("accept = %+v / %v; calls %d connects %d refs %v", got, err,
							observing.calls, observing.connects, observing.refs)
					}

					if !reflect.DeepEqual(fake.Connects(), []string{account.ACI}) {
						t.Fatalf("wrong selected account: %v", fake.Connects())
					}
				})
			}
		}
	}
}

//nolint:funlen,cyclop,gocognit // independent pre-submission/result/error/privacy assertions
func TestGroupsAcceptFailures(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name   string
		stage  string
		cause  error
		result signal.GroupAcceptResult
		hint   string
	}{
		{"resolution", "resolve", signal.ErrGroupUpdateUncertain, signal.GroupAcceptResult{}, appAcceptBeforeSubmission},
		{
			appAcceptConnectStage, appAcceptConnectStage, signal.ErrGroupUpdateUncertain,
			signal.GroupAcceptResult{},
			appAcceptBeforeSubmission,
		},
		{
			"missing title", appAcceptUnknownStage, signal.ErrUnknownGroup,
			signal.GroupAcceptResult{},
			appAcceptBeforeSubmission,
		},
		{
			"definite", appAcceptOperationStage, signal.ErrGroupChanged,
			signal.GroupAcceptResult{ID: groupID, Revision: 8},
			"changed",
		},
		{
			"uncertain", appAcceptOperationStage, signal.ErrGroupUpdateUncertain,
			signal.GroupAcceptResult{ID: groupID, Revision: 8},
			"attempted revision 8",
		},
		{appAcceptAcceptedHint, appAcceptOperationStage, errBoom, signal.GroupAcceptResult{
			ID: groupID, Revision: 8,
			Changed: true, Accepted: true,
		}, appAcceptAcceptedHint},
		{"verified", appAcceptOperationStage, errBoom, signal.GroupAcceptResult{
			ID: groupID, Revision: 9,
			Changed: true, Accepted: true, Verified: true,
		}, "revision 9"},
		{
			appAcceptNoop, appAcceptOperationStage, errBoom,
			signal.GroupAcceptResult{ID: groupID, Revision: 7, Verified: true},
			joinFailureHint,
		},
		{"unlink", appAcceptOperationStage, signal.ErrDeviceUnlinked, signal.GroupAcceptResult{}, joinFailureHint},
		{"context", appAcceptOperationStage, context.Canceled, signal.GroupAcceptResult{}, joinFailureHint},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			secret := &url.Error{
				Op: appAcceptOperationStage, URL: "https://signal.group/#" + masterKey,
				Err: fmt.Errorf("key=%s: %w", masterKey, test.cause),
			}
			fake := &signaltest.Fake{Linked: []signal.Account{testAccount()}}

			ref := masterKey
			if test.stage == "resolve" {
				fake.GroupTitlesErr, ref = secret, "secret "+masterKey
			}

			if test.stage == appAcceptUnknownStage {
				ref = "secret " + masterKey
			}

			if test.stage == appAcceptConnectStage {
				fake.ConnectErr = secret
			}

			client, err := fake.Factory(t.Context(), signal.Options{})
			if err != nil {
				t.Fatal(err)
			}

			t.Cleanup(func() { _ = client.Close() })

			observing := &acceptObservingClient{
				Client: client, result: &test.result, operationErr: secret,
				observeContext: func(context.Context) {},
			}

			got, err := app.New(observing).GroupsAccept(t.Context(), app.GroupAcceptRequest{Group: ref})
			if got != test.result || !errors.Is(err, test.cause) || !strings.Contains(err.Error(), test.hint) ||
				strings.Contains(err.Error(), masterKey) || strings.Contains(err.Error(), "signal.group") {
				t.Fatalf("result/error = %+v / %v", got, err)
			}

			if test.stage != appAcceptUnknownStage {
				var nested *url.Error

				if !errors.Is(err, secret) || !errors.As(err, &nested) || nested != secret {
					t.Fatalf("lost cause identity: %v", err)
				}
			}

			wantCalls, wantConnects := 0, 0

			switch test.stage {
			case appAcceptOperationStage:
				wantCalls, wantConnects = 1, 1
			case appAcceptConnectStage:
				wantConnects = 1
			}

			if observing.calls != wantCalls || observing.connects != wantConnects {
				t.Fatalf("calls/connects = %d/%d", observing.calls, observing.connects)
			}

			if test.stage != appAcceptOperationStage {
				for _, falseOutcome := range []string{"uncertain", "attempted", "revision", "invitation accepted"} {
					if strings.Contains(err.Error(), falseOutcome) {
						t.Fatalf("false pre-submission outcome: %v", err)
					}
				}
			}
		})
	}
}
