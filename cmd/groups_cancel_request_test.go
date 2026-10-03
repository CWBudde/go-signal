package cmd_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/cmd"
	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/go-signal/internal/signal/signaltest"
	"github.com/spf13/cobra"
)

const (
	cancelRequestOperationStage = "operation"
	cancelRequestHost           = "signal.group"
	cancelRequestUnknownFlag    = "unknown flag"
	cancelRequestCount          = "count"
	cancelRequestMissing        = "missing"
	cancelRequestSecondACI      = "33333333-3333-4333-8333-333333333333"
	cancelRequestAccountFlag    = "--account"
	cancelRequestPatch          = "PATCH"
	cancelRequestVerifiedStage  = "verified"
	cancelRequestResolveStage   = "resolve"
	cancelRequestNoop           = "noop"
	cancelRequestCachedTitle    = "Cached request"
	cancelRequestConnectStage   = "connect"
	cancelRequestFactoryStage   = "factory"
	cancelRequestUncertainStage = "uncertain"
	cancelRequestOutputFlag     = "--output"
	cancelRequestLogFormatFlag  = "--log-format"
	cancelRequestCmd            = "cancel-request"
	cancelRequestTitle          = "Request \"friends\"\nweekend"
)

func commandCancelRequestFixture(account signal.Account, kind string) (*signaltest.Fake, string) {
	key := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32)))

	pending := signal.Recipient{ACI: account.ACI}

	group := signal.Group{
		ID: groupID, Title: cancelRequestTitle, Revision: 7,
		Members:    []signal.GroupMember{{Recipient: signal.Recipient{ACI: aliceACI}, Role: signal.GroupRoleAdmin}},
		Requesting: []signal.RequestingMember{{Recipient: pending}},
	}
	if kind == cancelRequestNoop {
		group.Requesting = nil
		group.Members = append(group.Members, signal.GroupMember{
			Recipient: signal.Recipient{ACI: account.ACI},
			Role:      signal.GroupRoleMember,
		})
	}

	fake := &signaltest.Fake{
		Linked:             []signal.Account{account},
		GroupJoinServer:    map[string]signal.Group{key: group},
		GroupJoinKnownKeys: map[string]map[string]string{account.ACI: {key: groupID}},
		GroupJoinTitleCache: map[string]map[string]signal.CachedGroup{
			account.ACI: {groupID: {Title: cancelRequestCachedTitle}},
		},
	}
	if kind == cancelRequestNoop {
		fake.CancelGroupJoinRequestErr = signal.ErrGroupChanged
	}

	return fake, key
}

func TestGroupsCancelRequestCommand(t *testing.T) {
	t.Parallel()

	first := *testAccount()
	first.PNI = "44444444-4444-4444-8444-444444444444"
	second := signal.Account{
		ACI: cancelRequestSecondACI,
		PNI: "55555555-5555-4555-8555-555555555555", Number: "+15550101", DeviceID: 2,
	}

	for _, account := range []signal.Account{first, second} {
		for _, kind := range []string{"ACI", cancelRequestNoop} {
			for _, jsonOutput := range []bool{false, true} {
				name := "groups_cancel_request"
				if kind == cancelRequestNoop {
					name += "_noop"
				}

				if jsonOutput {
					name += "_" + formatJSON
				}

				t.Run(account.ACI+"/"+kind+"/"+name, func(t *testing.T) {
					t.Parallel()

					fake, key := commandCancelRequestFixture(account, kind)
					fake.Linked = []signal.Account{first, second}

					args := []string{cancelRequestAccountFlag, account.ACI, groupsCmd, cancelRequestCmd, key, yes}
					if jsonOutput {
						args = append([]string{"-o", formatJSON}, args...)
					}

					out, err := run(t, fake, args...)
					if err != nil {
						t.Fatal(err)
					}

					golden(t, name, out)

					if !reflect.DeepEqual(fake.Connects(), []string{account.ACI}) || !fake.AllClosed() {
						t.Fatalf("connections/closed = %v/%v", fake.Connects(), fake.AllClosed())
					}
				})
			}
		}
	}
}

func TestGroupsCancelRequestSecretBoundariesPreflight(t *testing.T) {
	t.Parallel()

	_, key := commandCancelRequestFixture(*testAccount(), "ACI")
	for _, test := range []struct {
		name string
		args []string
	}{
		{"confirmation missing", []string{groupsCmd, cancelRequestCmd, key}},
		{"confirmation false", []string{groupsCmd, cancelRequestCmd, key, "--yes=false"}},
		{"confirmation secret", []string{groupsCmd, cancelRequestCmd, key, "--yes=" + key}},
		{"blank", []string{groupsCmd, cancelRequestCmd, " \t"}},
		{"long before trim", []string{groupsCmd, cancelRequestCmd, strings.Repeat(" ", 4097)}},
		{"malformed explicit", []string{groupsCmd, cancelRequestCmd, "group:SECRET"}},
		{"https invite", []string{groupsCmd, cancelRequestCmd, "https://signal.group/#" + key}},
		{"sgnl invite", []string{groupsCmd, cancelRequestCmd, "sgnl://signal.group/#" + key}},
		{"malformed https", []string{groupsCmd, cancelRequestCmd, "https://signal.group/#%zz"}},
		{"malformed sgnl", []string{groupsCmd, cancelRequestCmd, "sgnl://signal.group/#%zz"}},
		{cancelRequestMissing, []string{groupsCmd, cancelRequestCmd}},
		{cancelRequestCount, []string{groupsCmd, cancelRequestCmd, key, key}},
		{cancelRequestUnknownFlag, []string{groupsCmd, cancelRequestCmd, "--" + key}},
		{"unknown value", []string{groupsCmd, cancelRequestCmd, "--mystery=" + key}},
		{"boolean after", []string{groupsCmd, cancelRequestCmd, key, "--verbose=" + key}},
		{"boolean before", []string{"--verbose=" + key, groupsCmd, cancelRequestCmd, key, yes}},
		{"output after", []string{groupsCmd, cancelRequestCmd, key, cancelRequestOutputFlag, key}},
		{"output before", []string{cancelRequestOutputFlag, key, groupsCmd, cancelRequestCmd, key, yes}},
		{"logging after", []string{groupsCmd, cancelRequestCmd, key, cancelRequestLogFormatFlag, key}},
		{"logging before", []string{cancelRequestLogFormatFlag, key, groupsCmd, cancelRequestCmd, key, yes}},
		{"config after", []string{groupsCmd, cancelRequestCmd, key, avatarConfigFlag, key}},
		{"config before", []string{avatarConfigFlag, key, groupsCmd, cancelRequestCmd, key, yes}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			fake, _ := commandCancelRequestFixture(*testAccount(), "ACI")

			args := test.args
			if !strings.HasPrefix(test.name, "confirmation ") {
				args = append(args, yes)
			}

			out, stderr, err := runStderr(t, t.Context(), fake, args...)
			if err == nil || out != "" {
				t.Fatalf("expected empty output and error: %q / %v", out, err)
			}

			for _, forbidden := range []string{key, "SECRET", cancelRequestHost, "%zz"} {
				if strings.Contains(err.Error()+stderr, forbidden) {
					t.Fatalf("unsafe error: %v / %s", err, stderr)
				}
			}

			if len(fake.Opened()) != 0 || len(fake.Connects()) != 0 {
				t.Fatal("preflight opened an account")
			}
		})
	}
}

type commandCancelRequestClient struct {
	signal.Client

	result   signal.GroupCancelRequestResult
	err      error
	closeErr error
	calls    int
}

func (c *commandCancelRequestClient) CancelGroupJoinRequest(
	context.Context, string,
) (signal.GroupCancelRequestResult, error) {
	c.calls++

	return c.result, c.err
}

func (c *commandCancelRequestClient) Close() error {
	_ = c.Client.Close()

	return c.closeErr
}

//nolint:funlen,cyclop,gocognit,gocyclo // preserve outcome and typed errors at each lifecycle boundary
func TestGroupsCancelRequestSecretBoundariesLifecycle(t *testing.T) {
	t.Parallel()

	for _, stage := range []string{
		cancelRequestFactoryStage, cancelRequestResolveStage, cancelRequestConnectStage, cancelRequestOperationStage,
		cancelRequestUncertainStage, joinAcceptedLabel, cancelRequestVerifiedStage,
	} {
		t.Run(stage, func(t *testing.T) {
			t.Parallel()

			fake, key := commandCancelRequestFixture(*testAccount(), "ACI")
			cause := signal.ErrDeviceUnlinked
			result := signal.GroupCancelRequestResult{}

			if stage == cancelRequestUncertainStage || stage == cancelRequestConnectStage {
				cause = signal.ErrGroupUpdateUncertain
				result = signal.GroupCancelRequestResult{ID: groupID, Revision: 8}
			}

			if stage == joinAcceptedLabel || stage == cancelRequestVerifiedStage {
				cause = context.Canceled
				result = signal.GroupCancelRequestResult{
					ID: groupID, Revision: 8, Changed: true, Accepted: true,
					Verified: stage == cancelRequestVerifiedStage,
				}
			}

			secret := &url.Error{
				Op: cancelRequestPatch, URL: "https://signal.group/#" + key,
				Err: fmt.Errorf("arbitrary master key=%s: %w", key, cause),
			}
			if stage == cancelRequestConnectStage {
				fake.ConnectErr = secret
			}

			ref := key
			if stage == cancelRequestResolveStage {
				fake.GroupTitlesErr, ref = secret, cancelRequestCachedTitle
			}

			var observing *commandCancelRequestClient

			factory := func(ctx context.Context, opts signal.Options) (signal.Client, error) {
				if stage == cancelRequestFactoryStage {
					return nil, secret
				}

				client, err := fake.Factory(ctx, opts)
				if err != nil {
					return nil, fmt.Errorf("test factory: %w", err)
				}

				observing = &commandCancelRequestClient{Client: client, result: result, err: secret}

				return observing, nil
			}
			out, stderr, err := runJoinFactory(t, factory, groupsCmd, cancelRequestCmd, ref, yes)

			var nested *url.Error

			if out != "" || !errors.Is(err, cause) || !errors.Is(err, secret) ||
				!errors.As(err, &nested) || nested != secret || strings.Contains(err.Error()+stderr, key) ||
				strings.Contains(err.Error()+stderr, cancelRequestHost) {
				t.Fatalf("unsafe result/error: %q / %s / %v", out, stderr, err)
			}

			if errors.Is(cause, signal.ErrDeviceUnlinked) && cmd.ExitCode(err) != 3 {
				t.Fatalf("unlink exit = %d", cmd.ExitCode(err))
			}

			wantCalls := 1
			if stage == cancelRequestFactoryStage || stage == cancelRequestResolveStage || stage == cancelRequestConnectStage {
				wantCalls = 0
			}

			if observing != nil && observing.calls != wantCalls || !fake.AllClosed() {
				t.Fatalf("wrong operation count or client not closed: %+v", observing)
			}

			if stage == cancelRequestConnectStage && (!strings.Contains(err.Error(), "before submission") ||
				strings.Contains(err.Error(), cancelRequestUncertainStage) || strings.Contains(err.Error(), "attempted")) {
				t.Fatalf("false connection outcome: %v", err)
			}

			if stage == cancelRequestUncertainStage && (!strings.Contains(err.Error(), "revision 8") ||
				strings.Contains(err.Error(), "join request cancellation accepted")) {
				t.Fatalf("false uncertainty outcome: %v", err)
			}

			if result.Accepted && (!strings.Contains(err.Error(), "join request cancellation accepted") ||
				!strings.Contains(err.Error(), "groups show") || !strings.Contains(err.Error(), "phone")) {
				t.Fatalf("missing follow-up guidance: %v", err)
			}
		})
	}
}

//nolint:cyclop // test inherited configuration, account selection and error redaction
func TestGroupsCancelRequestSecretBoundariesSetup(t *testing.T) {
	t.Parallel()

	for _, failing := range []bool{false, true} {
		t.Run(strconv.FormatBool(failing), func(t *testing.T) {
			t.Parallel()

			account := *testAccount()
			fake, key := commandCancelRequestFixture(account, "ACI")
			cfgFile := filepath.Join(t.TempDir(), "config.yaml")

			err := os.WriteFile(cfgFile, []byte("output: json\naccount: "+account.ACI+"\n"), 0o600)
			if err != nil {
				t.Fatal(err)
			}

			var out bytes.Buffer

			root := cmd.NewRootCmd(cmd.WithClientFactory(fake.Factory), cmd.WithLocation(time.UTC))
			inherited := root.PersistentPreRunE
			setups := 0
			secret := fmt.Errorf("master key %s: %w", key, signal.ErrDeviceUnlinked)
			root.PersistentPreRunE = func(command *cobra.Command, args []string) error {
				setups++

				if failing {
					return secret
				}

				return inherited(command, args)
			}
			root.SetOut(&out)
			root.SetArgs([]string{avatarConfigFlag, cfgFile, groupsCmd, cancelRequestCmd, cancelRequestCachedTitle, yes})

			err = root.ExecuteContext(t.Context())
			if setups != 1 {
				t.Fatalf("setup calls = %d", setups)
			}

			if failing {
				if !errors.Is(err, secret) || cmd.ExitCode(err) != 3 || out.Len() != 0 ||
					strings.Contains(err.Error(), key) || strings.Contains(err.Error(), "groups join") ||
					len(fake.Opened()) != 0 {
					t.Fatalf("unsafe inherited setup failure: %v / %s", err, out.String())
				}
			} else if err != nil || !strings.HasPrefix(out.String(), `{"version":1,"groupCancelRequest":`) ||
				!reflect.DeepEqual(fake.Connects(), []string{account.ACI}) || !fake.AllClosed() {
				t.Fatalf("configuration/account/result = %v / %v / %s", err, fake.Connects(), out.String())
			}
		})
	}
}

type cancelRequestFailingWriter struct{ err error }

func (w cancelRequestFailingWriter) Write([]byte) (int, error) { return 0, w.err }

func TestGroupsCancelRequestSecretBoundariesWriter(t *testing.T) {
	t.Parallel()

	fake, key := commandCancelRequestFixture(*testAccount(), "ACI")
	secret := fmt.Errorf("master key %s: %w", key, signal.ErrDeviceUnlinked)
	cfgFile := filepath.Join(t.TempDir(), "config.yaml")

	err := os.WriteFile(cfgFile, nil, 0o600)
	if err != nil {
		t.Fatal(err)
	}

	root := cmd.NewRootCmd(cmd.WithClientFactory(fake.Factory), cmd.WithLocation(time.UTC))
	root.SetOut(cancelRequestFailingWriter{err: secret})
	root.SetArgs([]string{avatarConfigFlag, cfgFile, groupsCmd, cancelRequestCmd, key, yes})

	err = root.ExecuteContext(t.Context())
	if !errors.Is(err, secret) || cmd.ExitCode(err) != 3 || strings.Contains(err.Error(), key) || !fake.AllClosed() {
		t.Fatalf("unsafe writer error: %v", err)
	}
}

//nolint:paralleltest // captures the process-global logger
func TestGroupsCancelRequestSecretBoundariesClose(t *testing.T) {
	fake, key := commandCancelRequestFixture(*testAccount(), "ACI")
	secret := fmt.Errorf("master key %s: %w", key, signal.ErrDeviceUnlinked)
	oldLogger := slog.Default()

	t.Cleanup(func() { slog.SetDefault(oldLogger) })

	var logs bytes.Buffer

	factory := func(ctx context.Context, opts signal.Options) (signal.Client, error) {
		client, err := fake.Factory(ctx, opts)
		if err != nil {
			return nil, fmt.Errorf("test factory: %w", err)
		}

		slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))

		return &commandCancelRequestClient{
			Client: client, closeErr: secret,
			result: signal.GroupCancelRequestResult{ID: groupID, Revision: 7, Verified: true},
		}, nil
	}

	out, _, err := runJoinFactory(t, factory, groupsCmd, cancelRequestCmd, key, yes)
	if err != nil || out == "" || !strings.Contains(logs.String(), "close client") ||
		strings.Contains(logs.String(), key) || strings.Contains(logs.String(), "groups join") || !fake.AllClosed() {
		t.Fatalf("unsafe close output/log/error: %q / %s / %v", out, logs.String(), err)
	}
}

// TestGroupsCancelRequestConfiguration catches loss of flag > env > config precedence
// when this command replaces Cobra's inherited setup hook.
func TestGroupsCancelRequestConfiguration(t *testing.T) {
	first := *testAccount()
	second := signal.Account{ACI: cancelRequestSecondACI, Number: "+15550101", DeviceID: 2}
	t.Setenv("GOSIGNAL_ACCOUNT", second.ACI)
	t.Setenv("GOSIGNAL_OUTPUT", formatJSON)

	for _, flags := range []bool{false, true} { //nolint:paralleltest // inherited environment forbids parallel subtests
		t.Run(strconv.FormatBool(flags), func(t *testing.T) {
			selected := second
			if flags {
				selected = first
			}

			fake, key := commandCancelRequestFixture(selected, "ACI")
			fake.Linked = []signal.Account{first, second}
			cfgFile := filepath.Join(t.TempDir(), "config.yaml")

			err := os.WriteFile(cfgFile, []byte("output: plain\naccount: "+first.ACI+"\n"), 0o600)
			if err != nil {
				t.Fatal(err)
			}

			args := []string{avatarConfigFlag, cfgFile, groupsCmd, cancelRequestCmd, key, yes}
			if flags {
				args = append(args, cancelRequestAccountFlag, first.ACI, cancelRequestOutputFlag, "plain")
			}

			var out bytes.Buffer

			root := cmd.NewRootCmd(cmd.WithClientFactory(fake.Factory), cmd.WithLocation(time.UTC))
			root.SetOut(&out)
			root.SetArgs(args)

			err = root.ExecuteContext(t.Context())
			if err != nil || !reflect.DeepEqual(fake.Connects(), []string{selected.ACI}) || !fake.AllClosed() {
				t.Fatalf("selected account = %v / %v", fake.Connects(), err)
			}

			prefix := `{"version":1,"groupCancelRequest":`
			if flags {
				prefix = "Join request cancelled\n"
			}

			if !strings.HasPrefix(out.String(), prefix) {
				t.Fatalf("output precedence = %q", out.String())
			}
		})
	}
}
