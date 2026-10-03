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
	acceptVerifiedStage  = "verified"
	acceptResolveStage   = "resolve"
	acceptNoop           = "noop"
	acceptCachedTitle    = "Cached invitation"
	acceptConnectStage   = "connect"
	acceptFactoryStage   = "factory"
	acceptUncertainStage = "uncertain"
	acceptOutputFlag     = "--output"
	acceptLogFormatFlag  = "--log-format"
	acceptCmd            = "accept"
	acceptTitle          = "Accept \"friends\"\nweekend"
)

func commandAcceptFixture(account signal.Account, kind string) (*signaltest.Fake, string) {
	key := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32)))

	pending := signal.Recipient{ACI: account.ACI}
	if kind == "PNI" {
		pending = signal.Recipient{PNI: account.PNI}
	}

	group := signal.Group{
		ID: groupID, Title: acceptTitle, Revision: 7,
		Members: []signal.GroupMember{{Recipient: signal.Recipient{ACI: aliceACI}, Role: signal.GroupRoleAdmin}},
		Pending: []signal.PendingMember{{Recipient: pending, Role: signal.GroupRoleMember}},
	}
	if kind == acceptNoop {
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
			account.ACI: {groupID: {Title: acceptCachedTitle}},
		},
	}
	if kind == acceptNoop {
		fake.AcceptGroupInvitationErr = signal.ErrGroupChanged
	}

	return fake, key
}

func TestGroupsAcceptCommand(t *testing.T) {
	t.Parallel()

	first := *testAccount()
	first.PNI = "44444444-4444-4444-8444-444444444444"
	second := signal.Account{
		ACI: "33333333-3333-4333-8333-333333333333",
		PNI: "55555555-5555-4555-8555-555555555555", Number: "+15550101", DeviceID: 2,
	}

	for _, account := range []signal.Account{first, second} {
		for _, kind := range []string{"ACI", "PNI", acceptNoop} {
			for _, jsonOutput := range []bool{false, true} {
				name := "groups_accept"
				if kind == acceptNoop {
					name += "_noop"
				}

				if jsonOutput {
					name += "_" + formatJSON
				}

				t.Run(account.ACI+"/"+kind+"/"+name, func(t *testing.T) {
					t.Parallel()

					fake, key := commandAcceptFixture(account, kind)
					fake.Linked = []signal.Account{first, second}

					args := []string{"--account", account.ACI, groupsCmd, acceptCmd, key}
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

func TestGroupsAcceptSecretBoundariesPreflight(t *testing.T) {
	t.Parallel()

	_, key := commandAcceptFixture(*testAccount(), "ACI")
	for _, test := range []struct {
		name string
		args []string
	}{
		{"blank", []string{groupsCmd, acceptCmd, " \t"}},
		{"long before trim", []string{groupsCmd, acceptCmd, strings.Repeat(" ", 4097)}},
		{"malformed explicit", []string{groupsCmd, acceptCmd, "group:SECRET"}},
		{"https invite", []string{groupsCmd, acceptCmd, "https://signal.group/#" + key}},
		{"sgnl invite", []string{groupsCmd, acceptCmd, "sgnl://signal.group/#" + key}},
		{"malformed https", []string{groupsCmd, acceptCmd, "https://signal.group/#%zz"}},
		{"malformed sgnl", []string{groupsCmd, acceptCmd, "sgnl://signal.group/#%zz"}},
		{"missing", []string{groupsCmd, acceptCmd}},
		{"count", []string{groupsCmd, acceptCmd, key, key}},
		{"unknown flag", []string{groupsCmd, acceptCmd, "--" + key}},
		{"unknown value", []string{groupsCmd, acceptCmd, "--mystery=" + key}},
		{"boolean after", []string{groupsCmd, acceptCmd, key, "--verbose=" + key}},
		{"boolean before", []string{"--verbose=" + key, groupsCmd, acceptCmd, key}},
		{"output after", []string{groupsCmd, acceptCmd, key, acceptOutputFlag, key}},
		{"output before", []string{acceptOutputFlag, key, groupsCmd, acceptCmd, key}},
		{"logging after", []string{groupsCmd, acceptCmd, key, acceptLogFormatFlag, key}},
		{"logging before", []string{acceptLogFormatFlag, key, groupsCmd, acceptCmd, key}},
		{"config after", []string{groupsCmd, acceptCmd, key, avatarConfigFlag, key}},
		{"config before", []string{avatarConfigFlag, key, groupsCmd, acceptCmd, key}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			fake, _ := commandAcceptFixture(*testAccount(), "ACI")

			out, stderr, err := runStderr(t, t.Context(), fake, test.args...)
			if err == nil || out != "" {
				t.Fatalf("expected empty output and error: %q / %v", out, err)
			}

			for _, forbidden := range []string{key, "SECRET", "signal.group", "%zz"} {
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

type commandAcceptClient struct {
	signal.Client

	result   signal.GroupAcceptResult
	err      error
	closeErr error
	calls    int
}

func (c *commandAcceptClient) AcceptGroupInvitation(context.Context, string) (signal.GroupAcceptResult, error) {
	c.calls++

	return c.result, c.err
}

func (c *commandAcceptClient) Close() error {
	_ = c.Client.Close()

	return c.closeErr
}

//nolint:funlen,cyclop,gocognit,gocyclo // preserve outcome and typed errors at each lifecycle boundary
func TestGroupsAcceptSecretBoundariesLifecycle(t *testing.T) {
	t.Parallel()

	for _, stage := range []string{
		acceptFactoryStage, acceptResolveStage, acceptConnectStage, "operation",
		acceptUncertainStage, "accepted", acceptVerifiedStage,
	} {
		t.Run(stage, func(t *testing.T) {
			t.Parallel()

			fake, key := commandAcceptFixture(*testAccount(), "ACI")
			cause := signal.ErrDeviceUnlinked
			result := signal.GroupAcceptResult{}

			if stage == acceptUncertainStage || stage == acceptConnectStage {
				cause = signal.ErrGroupUpdateUncertain
				result = signal.GroupAcceptResult{ID: groupID, Revision: 8}
			}

			if stage == "accepted" || stage == acceptVerifiedStage {
				cause = context.Canceled
				result = signal.GroupAcceptResult{
					ID: groupID, Revision: 8, Changed: true, Accepted: true,
					Verified: stage == acceptVerifiedStage,
				}
			}

			secret := &url.Error{
				Op: "PATCH", URL: "https://signal.group/#" + key,
				Err: fmt.Errorf("arbitrary master key=%s: %w", key, cause),
			}
			if stage == acceptConnectStage {
				fake.ConnectErr = secret
			}

			ref := key
			if stage == acceptResolveStage {
				fake.GroupTitlesErr, ref = secret, acceptCachedTitle
			}

			var observing *commandAcceptClient

			factory := func(ctx context.Context, opts signal.Options) (signal.Client, error) {
				if stage == acceptFactoryStage {
					return nil, secret
				}

				client, err := fake.Factory(ctx, opts)
				if err != nil {
					return nil, fmt.Errorf("test factory: %w", err)
				}

				observing = &commandAcceptClient{Client: client, result: result, err: secret}

				return observing, nil
			}
			out, stderr, err := runJoinFactory(t, factory, groupsCmd, acceptCmd, ref)

			var nested *url.Error

			if out != "" || !errors.Is(err, cause) || !errors.Is(err, secret) ||
				!errors.As(err, &nested) || nested != secret || strings.Contains(err.Error()+stderr, key) ||
				strings.Contains(err.Error()+stderr, "signal.group") {
				t.Fatalf("unsafe result/error: %q / %s / %v", out, stderr, err)
			}

			if errors.Is(cause, signal.ErrDeviceUnlinked) && cmd.ExitCode(err) != 3 {
				t.Fatalf("unlink exit = %d", cmd.ExitCode(err))
			}

			wantCalls := 1
			if stage == acceptFactoryStage || stage == acceptResolveStage || stage == acceptConnectStage {
				wantCalls = 0
			}

			if observing != nil && observing.calls != wantCalls || !fake.AllClosed() {
				t.Fatalf("wrong operation count or client not closed: %+v", observing)
			}

			if stage == acceptConnectStage && (!strings.Contains(err.Error(), "before submission") ||
				strings.Contains(err.Error(), acceptUncertainStage) || strings.Contains(err.Error(), "attempted")) {
				t.Fatalf("false connection outcome: %v", err)
			}

			if stage == acceptUncertainStage && (!strings.Contains(err.Error(), "attempted revision 8") ||
				strings.Contains(err.Error(), "invitation accepted")) {
				t.Fatalf("false uncertainty outcome: %v", err)
			}

			if result.Accepted && (!strings.Contains(err.Error(), "invitation accepted") ||
				!strings.Contains(err.Error(), "groups show") || !strings.Contains(err.Error(), "phone")) {
				t.Fatalf("missing follow-up guidance: %v", err)
			}
		})
	}
}

//nolint:cyclop // test inherited configuration, account selection and error redaction
func TestGroupsAcceptSecretBoundariesSetup(t *testing.T) {
	t.Parallel()

	for _, failing := range []bool{false, true} {
		t.Run(strconv.FormatBool(failing), func(t *testing.T) {
			t.Parallel()

			account := *testAccount()
			fake, key := commandAcceptFixture(account, "ACI")
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
			root.SetArgs([]string{avatarConfigFlag, cfgFile, groupsCmd, acceptCmd, acceptCachedTitle})

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
			} else if err != nil || !strings.HasPrefix(out.String(), `{"version":1,"groupAccept":`) ||
				!reflect.DeepEqual(fake.Connects(), []string{account.ACI}) || !fake.AllClosed() {
				t.Fatalf("configuration/account/result = %v / %v / %s", err, fake.Connects(), out.String())
			}
		})
	}
}

type acceptFailingWriter struct{ err error }

func (w acceptFailingWriter) Write([]byte) (int, error) { return 0, w.err }

func TestGroupsAcceptSecretBoundariesWriter(t *testing.T) {
	t.Parallel()

	fake, key := commandAcceptFixture(*testAccount(), "ACI")
	secret := fmt.Errorf("master key %s: %w", key, signal.ErrDeviceUnlinked)
	cfgFile := filepath.Join(t.TempDir(), "config.yaml")

	err := os.WriteFile(cfgFile, nil, 0o600)
	if err != nil {
		t.Fatal(err)
	}

	root := cmd.NewRootCmd(cmd.WithClientFactory(fake.Factory), cmd.WithLocation(time.UTC))
	root.SetOut(acceptFailingWriter{err: secret})
	root.SetArgs([]string{avatarConfigFlag, cfgFile, groupsCmd, acceptCmd, key})

	err = root.ExecuteContext(t.Context())
	if !errors.Is(err, secret) || cmd.ExitCode(err) != 3 || strings.Contains(err.Error(), key) || !fake.AllClosed() {
		t.Fatalf("unsafe writer error: %v", err)
	}
}

func TestGroupsAcceptSecretBoundariesClose(t *testing.T) { //nolint:paralleltest // captures the process-global logger
	fake, key := commandAcceptFixture(*testAccount(), "ACI")
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

		return &commandAcceptClient{
			Client: client, closeErr: secret,
			result: signal.GroupAcceptResult{ID: groupID, Revision: 7, Verified: true},
		}, nil
	}

	out, _, err := runJoinFactory(t, factory, groupsCmd, acceptCmd, key)
	if err != nil || out == "" || !strings.Contains(logs.String(), "close client") ||
		strings.Contains(logs.String(), key) || strings.Contains(logs.String(), "groups join") || !fake.AllClosed() {
		t.Fatalf("unsafe close output/log/error: %q / %s / %v", out, logs.String(), err)
	}
}
