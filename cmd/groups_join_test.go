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
	"strings"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/cmd"
	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/go-signal/internal/signal/signaltest"
	"github.com/spf13/cobra"
)

const (
	joinCmd            = "join"
	joinAcceptedLabel  = "accepted"
	joinConnectStage   = "connection failure"
	joinUncertainStage = "write uncertainty"
)

const joinTitle = "Join \"friends\"\nweekend"

func commandJoinFixture(t *testing.T, status signal.GroupJoinStatus, noop bool) (*signaltest.Fake, string) {
	t.Helper()

	key := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32)))
	password := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("p", 16)))
	group := signal.Group{
		ID: groupID, MasterKey: key, Title: joinTitle, Revision: 7,
		Members: []signal.GroupMember{{Recipient: signal.Recipient{ACI: aliceACI}, Role: signal.GroupRoleAdmin}},
	}

	link, err := group.Link(aliceACI, signal.GroupLinkEnabled, password)
	if err != nil {
		t.Fatal(err)
	}

	state := signal.GroupLinkEnabled
	if status == signal.GroupJoinRequesting {
		state = signal.GroupLinkApproval
	}

	fake := &signaltest.Fake{
		Linked: []signal.Account{*testAccount()}, GroupJoinServer: map[string]signal.Group{key: group},
		GroupLinkStates:    map[string]signal.GroupLinkState{groupID: state},
		GroupLinkPasswords: map[string]string{groupID: password},
	}

	if noop {
		if status == signal.GroupJoinMember {
			group.Members = append(group.Members, signal.GroupMember{
				Recipient: signal.Recipient{ACI: testAccount().ACI}, Role: signal.GroupRoleMember,
			})
		} else {
			group.Requesting = []signal.RequestingMember{{Recipient: signal.Recipient{ACI: testAccount().ACI}}}
		}

		fake.GroupJoinServer[key] = group
		fake.GroupJoinKnownKeys = map[string]map[string]string{testAccount().ACI: {key: groupID}}
		fake.GroupJoinTitleCache = map[string]map[string]signal.CachedGroup{
			testAccount().ACI: {groupID: {Title: "Stale title"}},
		}
		fake.JoinGroupErr = signal.ErrGroupChanged // Fresh no-op evidence must avoid submission.
	}

	return fake, link.URL
}

func TestGroupsJoinGolden(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name   string
		status signal.GroupJoinStatus
		noop   bool
	}{
		{"member", signal.GroupJoinMember, false},
		{"requesting", signal.GroupJoinRequesting, false},
		{"member_noop", signal.GroupJoinMember, true},
		{"requesting_noop", signal.GroupJoinRequesting, true},
	} {
		for _, jsonOutput := range []bool{false, true} {
			name := "groups_join_" + test.name
			if jsonOutput {
				name += "_" + formatJSON
			}

			t.Run(name, func(t *testing.T) {
				t.Parallel()
				fake, link := commandJoinFixture(t, test.status, test.noop)

				args := []string{groupsCmd, joinCmd, link}
				if jsonOutput {
					args = append([]string{"-o", formatJSON}, args...)
				}

				out, err := run(t, fake, args...)
				if err != nil {
					t.Fatal(err)
				}

				golden(t, name, out)
			})
		}
	}
}

func TestGroupsJoinSecretErrors(t *testing.T) {
	t.Parallel()
	_, link := commandJoinFixture(t, signal.GroupJoinMember, false)

	for _, test := range []struct {
		name string
		args []string
	}{
		{"invalid link", []string{groupsCmd, joinCmd, link + "SECRET"}},
		{"missing", []string{groupsCmd, joinCmd}},
		{"count", []string{groupsCmd, joinCmd, link, link}},
		{"unknown flag", []string{groupsCmd, joinCmd, "--" + link}},
		{"unknown flag value", []string{groupsCmd, joinCmd, "--mystery=" + link}},
		{"global boolean", []string{groupsCmd, joinCmd, link, "--verbose=" + link}},
		{"global output", []string{groupsCmd, joinCmd, link, "--output", link}},
		{"setup logging", []string{groupsCmd, joinCmd, link, "--log-format", link}},
		{"setup config", []string{groupsCmd, joinCmd, link, avatarConfigFlag, link}},
		{"flag before command", []string{"--verbose=" + link, groupsCmd, joinCmd, link}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fixture, _ := commandJoinFixture(t, signal.GroupJoinMember, false)

			out, stderr, err := runStderr(t, t.Context(), fixture, test.args...)
			if err == nil || out != "" || strings.Contains(err.Error()+stderr, link) ||
				strings.Contains(err.Error()+stderr, "SECRET") || strings.Contains(err.Error()+stderr, "signal.group") {
				t.Fatalf("unsafe output/error %q / %q / %v", out, stderr, err)
			}

			if len(fixture.Opened()) != 0 || len(fixture.Connects()) != 0 {
				t.Fatal("invalid input opened/connected account")
			}
		})
	}
}

type commandJoinClient struct {
	signal.Client

	result   signal.GroupJoinResult
	joinErr  error
	closeErr error
	calls    int
}

func (c *commandJoinClient) JoinGroup(context.Context, string) (signal.GroupJoinResult, error) {
	c.calls++
	return c.result, c.joinErr
}

func (c *commandJoinClient) Close() error {
	_ = c.Client.Close()
	return c.closeErr
}

func runJoinFactory(t *testing.T, factory signal.Factory, args ...string) (string, string, error) {
	t.Helper()
	cfgFile := filepath.Join(t.TempDir(), "config.yaml")

	err := os.WriteFile(cfgFile, nil, 0o600)
	if err != nil {
		t.Fatal(err)
	}

	var out, stderr bytes.Buffer

	root := cmd.NewRootCmd(cmd.WithClientFactory(factory), cmd.WithLocation(time.UTC))
	root.SetOut(&out)
	root.SetErr(&stderr)
	root.SetArgs(append([]string{avatarConfigFlag, cfgFile}, args...))
	err = root.ExecuteContext(t.Context())

	return out.String(), stderr.String(), err
}

//nolint:cyclop,gocognit,funlen // lifecycle failure matrix preserves identity and guidance
func TestGroupsJoinLifecycleErrors(t *testing.T) {
	t.Parallel()

	for _, stage := range []string{"factory", joinConnectStage, "operation", joinUncertainStage, joinAcceptedLabel} {
		t.Run(stage, func(t *testing.T) {
			t.Parallel()
			fake, link := commandJoinFixture(t, signal.GroupJoinRequesting, false)
			cause := signal.ErrDeviceUnlinked
			result := signal.GroupJoinResult{}

			if stage == joinUncertainStage {
				cause = signal.ErrGroupUpdateUncertain
				result = signal.GroupJoinResult{ID: groupID, Revision: 8}
			}

			if stage == joinAcceptedLabel {
				cause = context.Canceled
				result = signal.GroupJoinResult{ID: groupID, Revision: 8, Accepted: true, Changed: true}
			}

			secretErr := &url.Error{Op: "PATCH", URL: link, Err: fmt.Errorf("password pppppppppppppppp: %w", cause)}

			var wrapped *commandJoinClient

			factory := func(ctx context.Context, opts signal.Options) (signal.Client, error) {
				if stage == "factory" {
					return nil, secretErr
				}

				client, err := fake.Factory(ctx, opts)
				if err != nil {
					return nil, fmt.Errorf("test factory: %w", err)
				}

				wrapped = &commandJoinClient{Client: client, result: result, joinErr: secretErr}

				return wrapped, nil
			}

			if stage == joinConnectStage {
				fake.ConnectErr = secretErr
			}

			out, stderr, err := runJoinFactory(t, factory, groupsCmd, joinCmd, link)
			if out != "" || !errors.Is(err, cause) || !errors.Is(err, secretErr) ||
				strings.Contains(err.Error()+stderr, link) || strings.Contains(err.Error()+stderr, "pppppppppppppppp") {
				t.Fatalf("output/error %q / %q / %v", out, stderr, err)
			}

			if errors.Is(cause, signal.ErrDeviceUnlinked) && cmd.ExitCode(err) != 3 {
				t.Fatalf("unlinked exit = %d", cmd.ExitCode(err))
			}

			if stage == joinAcceptedLabel && (!strings.Contains(err.Error(), joinAcceptedLabel) ||
				!strings.Contains(err.Error(), "administrator") || !strings.Contains(err.Error(), "unavailable until approval")) {
				t.Fatalf("missing accepted/pending guidance: %v", err)
			}

			if stage == joinUncertainStage && (!strings.Contains(err.Error(), "attempted revision 8") ||
				strings.Contains(err.Error(), joinAcceptedLabel)) {
				t.Fatalf("false acceptance/missing attempt: %v", err)
			}

			if wrapped != nil && stage != joinConnectStage && wrapped.calls != 1 {
				t.Fatalf("calls = %d", wrapped.calls)
			}

			if !fake.AllClosed() {
				t.Fatal("client not closed")
			}
		})
	}
}

var errJoinCloseSecret = errors.New("secret credential")

func TestGroupsJoinCloseWarningIsSafe(t *testing.T) { //nolint:paralleltest // captures the process-global logger
	// setup installs a process-global logger, so capture Close's log after Connect.
	fake, link := commandJoinFixture(t, signal.GroupJoinMember, false)

	var logs bytes.Buffer

	oldLogger := slog.Default()

	t.Cleanup(func() { slog.SetDefault(oldLogger) })

	factory := func(ctx context.Context, opts signal.Options) (signal.Client, error) {
		client, err := fake.Factory(ctx, opts)
		if err != nil {
			return nil, fmt.Errorf("test factory: %w", err)
		}

		slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))

		return &commandJoinClient{
			Client: client, result: signal.GroupJoinResult{ID: groupID, Status: signal.GroupJoinMember, Verified: true},
			closeErr: &url.Error{Op: "close", URL: link, Err: errJoinCloseSecret},
		}, nil
	}

	out, _, err := runJoinFactory(t, factory, groupsCmd, joinCmd, link)
	if err != nil || out == "" || !strings.Contains(logs.String(), "close client") ||
		strings.Contains(logs.String(), link) || strings.Contains(logs.String(), errJoinCloseSecret.Error()) {
		t.Fatalf("close output/log/error %q / %q / %v", out, logs.String(), err)
	}

	if !fake.AllClosed() {
		t.Fatal("not closed")
	}
}

func TestGroupsJoinInheritedSetupAndAccount(t *testing.T) {
	t.Parallel()

	second := signal.Account{ACI: "33333333-3333-4333-8333-333333333333", Number: "+15550101", DeviceID: 2}
	for _, account := range []signal.Account{*testAccount(), second} {
		t.Run(account.ACI, func(t *testing.T) {
			t.Parallel()

			fake, link := commandJoinFixture(t, signal.GroupJoinMember, false)
			fake.Linked = append(fake.Linked, second)
			cfgFile := filepath.Join(t.TempDir(), "config.yaml")

			err := os.WriteFile(cfgFile, []byte("output: json\n"), 0o600)
			if err != nil {
				t.Fatal(err)
			}

			var out bytes.Buffer

			root := cmd.NewRootCmd(cmd.WithClientFactory(fake.Factory), cmd.WithLocation(time.UTC))
			inheritedSetup := root.PersistentPreRunE
			setups := 0
			root.PersistentPreRunE = func(command *cobra.Command, args []string) error {
				setups++
				return inheritedSetup(command, args)
			}
			root.SetOut(&out)
			root.SetArgs([]string{avatarConfigFlag, cfgFile, "--account", account.ACI, groupsCmd, joinCmd, link})

			err = root.ExecuteContext(t.Context())
			if err != nil || setups != 1 || !strings.HasPrefix(out.String(), `{"version":1,"groupJoin":`) {
				t.Fatalf("setup count/output/error = %d / %s / %v", setups, out.String(), err)
			}

			if len(fake.Connects()) != 1 || fake.Connects()[0] != account.ACI || !fake.AllClosed() {
				t.Fatalf("selected account/connections/closed = %s / %v / %v", account.ACI, fake.Connects(), fake.AllClosed())
			}
		})
	}
}
