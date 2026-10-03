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
	appCancelRequestRevision     = "revision"
	appCancelRequestPrefix       = "group:"
	appCancelRequestSecretURL    = "https://signal.group/#SECRET"
	appCancelRequestTitleRef     = " Family "
	appCancelRequestSecondACI    = "22222222-2222-4222-8222-222222222222"
	appCancelRequestResolveStage = "resolve"
	appCancelRequestDefinite     = "definite"
	appCancelRequestChangedHint  = "changed"
	appCancelRequestUncertain    = "uncertain"
	appCancelRequestVerified     = "verified"
	appCancelRequestAttempted    = "attempted"

	appCancelRequestNoop             = "noop"
	appCancelRequestFreshTitle       = "Fresh"
	appCancelRequestConnectStage     = "connect"
	appCancelRequestUnknownStage     = "unknown"
	appCancelRequestOperationStage   = "operation"
	appCancelRequestAcceptedHint     = "accepted"
	appCancelRequestBeforeSubmission = "before submission"
)

func TestGroupCancelRequestRequestCheck(t *testing.T) {
	t.Parallel()

	tests := []struct {
		ref   string
		valid bool
	}{
		{ref: ""},
		{ref: " \t\n"},
		{ref: strings.Repeat(" ", 4097)},
		{ref: "group:SECRET"},
		{ref: appCancelRequestPrefix},
		{ref: appCancelRequestSecretURL},
		{ref: "sgnl://signal.group/#SECRET"},
		{ref: "https://signal.group/#%zz"},
		{ref: "sgnl://signal.group/#%zz"},
		{ref: appCancelRequestTitleRef, valid: true},
		{ref: groupID, valid: true},
		{ref: appCancelRequestPrefix + groupID, valid: true},
		{ref: masterKey, valid: true},
		{ref: base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("k", 32))), valid: true},
	}
	for _, test := range tests {
		err := (app.GroupCancelRequestRequest{Group: test.ref}).Check()
		if test.valid {
			if err != nil {
				t.Fatalf("valid cancellation reference refused: %v", err)
			}

			continue
		}

		if !errors.Is(err, signal.ErrUnknownGroup) {
			t.Fatalf("invalid cancellation reference accepted: %v", err)
		}

		_, err = app.New(nil).GroupsCancelRequest(t.Context(), app.GroupCancelRequestRequest{Group: test.ref})

		if !errors.Is(err, signal.ErrUnknownGroup) || strings.Contains(err.Error(), "SECRET") ||
			strings.Contains(err.Error(), "signal.group") {
			t.Fatalf("unsafe preflight: %v", err)
		}
	}
}

type cancelRequestObservingClient struct {
	signal.Client

	calls          int
	connects       int
	refs           []string
	result         *signal.GroupCancelRequestResult
	operationErr   error
	connectOptions signal.ConnectOptions
	order          []string
	observeContext func(context.Context)
}

func (c *cancelRequestObservingClient) Connect(ctx context.Context, opts ...signal.ConnectOption) error {
	c.order = append(c.order, appCancelRequestConnectStage)
	c.connects++
	c.observeContext(ctx)
	c.connectOptions = signal.NewConnectOptions(opts...)

	return c.Client.Connect(ctx, opts...) //nolint:wrapcheck // retain injected error identity
}

func (c *cancelRequestObservingClient) CancelGroupJoinRequest(
	ctx context.Context, ref string,
) (signal.GroupCancelRequestResult, error) {
	c.order = append(c.order, "cancel")
	c.calls++
	c.refs = append(c.refs, ref)
	c.observeContext(ctx)

	if c.result != nil {
		return *c.result, c.operationErr
	}

	return c.Client.CancelGroupJoinRequest(ctx, ref) //nolint:wrapcheck // retain real fake behavior
}

func (c *cancelRequestObservingClient) GroupTitles(ctx context.Context) (map[string]signal.CachedGroup, error) {
	c.order = append(c.order, appCancelRequestResolveStage)
	return c.Client.GroupTitles(ctx) //nolint:wrapcheck // retain injected error identity
}

// TestGroupsCancelRequest catches wrong account/ref selection, duplicate operations and loss of preview no-op evidence.
func TestGroupsCancelRequest(t *testing.T) { //nolint:funlen,cyclop,gocognit // account, reference and membership matrix
	t.Parallel()

	first := testAccount()
	first.PNI = "33333333-3333-4333-8333-333333333333"
	second := signal.Account{
		ACI: appCancelRequestSecondACI,
		PNI: "44444444-4444-4444-8444-444444444444", Number: "+12025550102", DeviceID: 2,
	}

	for _, account := range []signal.Account{first, second} {
		for _, kind := range []string{"ACI", appCancelRequestNoop} {
			for _, ref := range []string{appCancelRequestTitleRef, appCancelRequestPrefix + groupID, groupID, masterKey} {
				t.Run(account.ACI+"/"+kind+"/"+ref, func(t *testing.T) {
					t.Parallel()

					pending := signal.Recipient{ACI: account.ACI}

					group := signal.Group{
						ID: groupID, Title: appCancelRequestFreshTitle, Revision: 7,
						Requesting: []signal.RequestingMember{{Recipient: pending}},
					}
					if kind == appCancelRequestNoop {
						group.Requesting = nil
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
					if kind == appCancelRequestNoop {
						fake.CancelGroupJoinRequestErr = signal.ErrGroupChanged
					}

					client, err := fake.Factory(t.Context(), signal.Options{Account: account.Number})
					if err != nil {
						t.Fatal(err)
					}

					t.Cleanup(func() { _ = client.Close() })

					contexts := 0
					observing := &cancelRequestObservingClient{Client: client, observeContext: func(ctx context.Context) {
						if ctx != t.Context() {
							t.Fatal("caller context lost")
						}

						contexts++
					}}
					got, err := app.New(observing).GroupsCancelRequest(t.Context(), app.GroupCancelRequestRequest{Group: ref})

					want := signal.GroupCancelRequestResult{
						ID: groupID, Title: appCancelRequestFreshTitle, Revision: 8,
						Changed: true, Accepted: true, Verified: true,
					}
					if kind == appCancelRequestNoop {
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

					wantOrder := []string{appCancelRequestConnectStage, "cancel"}
					if ref == appCancelRequestTitleRef {
						wantOrder = []string{appCancelRequestResolveStage, appCancelRequestConnectStage, "cancel"}
					}

					if !reflect.DeepEqual(observing.order, wantOrder) {
						t.Fatalf("wrong operation ordering: %v", observing.order)
					}

					if !reflect.DeepEqual(fake.Connects(), []string{account.ACI}) {
						t.Fatalf("wrong selected account: %v", fake.Connects())
					}
				})
			}
		}
	}
}

func TestGroupsCancelRequestFailures(t *testing.T) {
	t.Parallel()

	for _, test := range []cancelRequestFailureCase{
		{
			"resolution", appCancelRequestResolveStage, signal.ErrGroupUpdateUncertain,
			signal.GroupCancelRequestResult{},
			appCancelRequestBeforeSubmission,
		},
		{
			appCancelRequestConnectStage, appCancelRequestConnectStage, signal.ErrGroupUpdateUncertain,
			signal.GroupCancelRequestResult{},
			appCancelRequestBeforeSubmission,
		},
		{
			"missing title", appCancelRequestUnknownStage, signal.ErrUnknownGroup,
			signal.GroupCancelRequestResult{},
			appCancelRequestBeforeSubmission,
		},
		{
			appCancelRequestDefinite, appCancelRequestOperationStage, signal.ErrGroupChanged,
			signal.GroupCancelRequestResult{ID: groupID, Revision: 8},
			appCancelRequestChangedHint,
		},
		{
			appCancelRequestUncertain, appCancelRequestOperationStage, signal.ErrGroupUpdateUncertain,
			signal.GroupCancelRequestResult{ID: groupID, Revision: 8},
			"revision 8",
		},
		{appCancelRequestAcceptedHint, appCancelRequestOperationStage, errBoom, signal.GroupCancelRequestResult{
			ID: groupID, Revision: 8,
			Changed: true, Accepted: true,
		}, appCancelRequestAcceptedHint},
		{appCancelRequestVerified, appCancelRequestOperationStage, errBoom, signal.GroupCancelRequestResult{
			ID: groupID, Revision: 9,
			Changed: true, Accepted: true, Verified: true,
		}, "revision 9"},
		{
			appCancelRequestNoop, appCancelRequestOperationStage, errBoom,
			signal.GroupCancelRequestResult{ID: groupID, Revision: 7, Verified: true},
			joinFailureHint,
		},
		{
			"unlink", appCancelRequestOperationStage, signal.ErrDeviceUnlinked,
			signal.GroupCancelRequestResult{},
			joinFailureHint,
		},
		{"context", appCancelRequestOperationStage, context.Canceled, signal.GroupCancelRequestResult{}, joinFailureHint},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			checkCancelRequestFailure(t, test)
		})
	}
}

type cancelRequestFailureCase struct {
	name   string
	stage  string
	cause  error
	result signal.GroupCancelRequestResult
	hint   string
}

//nolint:funlen,cyclop // exercise typed partial outcomes and each privacy boundary
func checkCancelRequestFailure(t *testing.T, test cancelRequestFailureCase) {
	t.Helper()

	secret := &url.Error{
		Op: appCancelRequestOperationStage, URL: "https://signal.group/#" + masterKey,
		Err: fmt.Errorf("key=%s: %w", masterKey, test.cause),
	}
	fake := &signaltest.Fake{Linked: []signal.Account{testAccount()}}

	ref := masterKey
	if test.stage == appCancelRequestResolveStage {
		fake.GroupTitlesErr, ref = secret, "secret "+masterKey
	}

	if test.stage == appCancelRequestUnknownStage {
		ref = "secret " + masterKey
	}

	if test.stage == appCancelRequestConnectStage {
		fake.ConnectErr = secret
	}

	client, err := fake.Factory(t.Context(), signal.Options{})
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = client.Close() })

	observing := &cancelRequestObservingClient{
		Client: client, result: &test.result, operationErr: secret,
		observeContext: func(context.Context) {},
	}

	got, err := app.New(observing).GroupsCancelRequest(t.Context(), app.GroupCancelRequestRequest{Group: ref})
	if got != test.result || !errors.Is(err, test.cause) || !strings.Contains(err.Error(), test.hint) ||
		strings.Contains(err.Error(), masterKey) || strings.Contains(err.Error(), "signal.group") {
		t.Fatalf("result/error = %+v / %v", got, err)
	}

	if test.stage != appCancelRequestUnknownStage {
		var nested *url.Error

		if !errors.Is(err, secret) || !errors.As(err, &nested) || nested != secret {
			t.Fatalf("lost cause identity: %v", err)
		}
	}

	wantCalls, wantConnects := 0, 0

	switch test.stage {
	case appCancelRequestOperationStage:
		wantCalls, wantConnects = 1, 1
	case appCancelRequestConnectStage:
		wantConnects = 1
	}

	if observing.calls != wantCalls || observing.connects != wantConnects {
		t.Fatalf("calls/connects = %d/%d", observing.calls, observing.connects)
	}

	if test.stage != appCancelRequestOperationStage {
		for _, falseOutcome := range []string{
			appCancelRequestUncertain, appCancelRequestAttempted, appCancelRequestRevision,
			"join request cancellation accepted",
		} {
			if strings.Contains(err.Error(), falseOutcome) {
				t.Fatalf("false pre-submission outcome: %v", err)
			}
		}
	}
}
