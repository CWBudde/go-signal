package cmd_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cwbudde/go-signal/cmd"
	"github.com/cwbudde/go-signal/internal/signal"
)

const (
	daemonCmd         = "daemon"
	daemonListenFlag  = "--listen"
	daemonLoopback    = "loopback"
	daemonNegative    = "must not be negative"
	daemonShortToken  = "at least 16"
	daemonTestListen  = "--listen=127.0.0.1:8765"
	daemonConfigCase  = "config"
	daemonEnvCase     = "environment"
	daemonInvalidChat = "group:invalid-base64!"
)

func daemonConfigFile(t *testing.T, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "config.yaml")

	err := os.WriteFile(path, []byte(content), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	return path
}

func TestDaemonHelp(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{{daemonCmd, "--help"}, {daemonCmd, serveCmd, "--help"}} {
		var out bytes.Buffer

		root := cmd.NewRootCmd(cmd.WithClientFactory(func(context.Context, signal.Options) (signal.Client, error) {
			t.Error("help opened the account")

			return nil, signal.ErrNotLinked
		}))
		root.SetOut(&out)
		root.SetArgs(args)

		err := root.ExecuteContext(t.Context())
		if err != nil {
			t.Fatalf("help: %v", err)
		}

		if !strings.Contains(out.String(), "serve") || !strings.Contains(out.String(), "HTTP") {
			t.Errorf("missing daemon help: %s", out.String())
		}
	}

	assertDaemonFlags(t)
}

func assertDaemonFlags(t *testing.T) {
	t.Helper()

	root := cmd.NewRootCmd()

	serve, _, err := root.Find([]string{daemonCmd, serveCmd})
	if err != nil || serve.CommandPath() != "go-signal daemon serve" {
		t.Fatalf("find daemon serve: %v", err)
	}

	for _, flag := range []string{
		"listen", "token-file", "read-only", "allow-recipient", "inbox-max-age", "inbox-max-count",
	} {
		if serve.Flags().Lookup(flag) == nil {
			t.Errorf("missing flag %s", flag)
		}
	}

	for _, flag := range []string{"token", "confirm", "attach-dir", "download-dir", "on-message", "hook-from"} {
		if serve.Flags().Lookup(flag) != nil {
			t.Errorf("unexpected daemon flag %s", flag)
		}
	}
}

func TestDaemonValidationBeforeOpen(t *testing.T) {
	t.Parallel()

	short := daemonConfigFile(t, "short\n")

	for _, test := range []struct {
		name   string
		args   []string
		config string
		want   string
	}{
		{name: "missing listen", want: daemonListenFlag},
		{name: "empty listen", args: []string{"--listen="}, want: daemonListenFlag},
		{name: "wildcard", args: []string{"--listen=0.0.0.0:8765"}, want: daemonLoopback},
		{name: "hostname", args: []string{"--listen=example.com:8765"}, want: daemonLoopback},
		{name: "missing host", args: []string{"--listen=:8765"}, want: daemonLoopback},
		{name: "malformed", args: []string{"--listen=8765"}, want: daemonListenFlag},
		{name: "missing token", args: []string{daemonTestListen}, want: "bearer token"},
		{name: "short token", args: []string{daemonTestListen, "--token-file=" + short}, want: daemonShortToken},
		{
			name: "missing token file", args: []string{"--listen=[::1]:8765", "--token-file=" + short + "-missing"},
			want: "--token-file",
		},
		{name: "invalid allowlist", args: []string{"--allow-recipient=" + daemonInvalidChat}, want: allowRecipientFlag},
		{name: "negative age flag", args: []string{"--inbox-max-age=-1h"}, want: daemonNegative},
		{name: "negative count flag", args: []string{"--inbox-max-count=-1"}, want: daemonNegative},
		{name: "negative age config", config: "daemon:\n  inbox-max-age: -1h\n", want: daemonNegative},
		{name: "negative count config", config: "daemon:\n  inbox-max-count: -1\n", want: daemonNegative},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			var out bytes.Buffer

			root := cmd.NewRootCmd(cmd.WithClientFactory(func(context.Context, signal.Options) (signal.Client, error) {
				t.Error("invalid configuration opened the account")

				return nil, signal.ErrNotLinked
			}))
			root.SetOut(&out)
			root.SetArgs(append([]string{"--config=" + daemonConfigFile(t, test.config), daemonCmd, serveCmd}, test.args...))

			err := root.ExecuteContext(t.Context())
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Errorf("got %v, want error containing %q", err, test.want)
			}

			if out.Len() != 0 {
				t.Errorf("startup wrote stdout: %q", out.String())
			}
		})
	}
}
