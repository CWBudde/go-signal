package cmd

import (
	"fmt"
	"log/slog"
	"net"
	"os"
	"slices"
	"strings"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/daemon"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

const daemonServeLong = `Serve an authenticated HTTP JSON and event stream API for local scripts and bots.

The required --listen address must use a loopback IP or localhost, for example
127.0.0.1:8765. Every request needs an Authorization: Bearer token of at least
16 characters. Read the token from --token-file, GOSIGNAL_DAEMON_TOKEN or
daemon.token in the config file; the trimmed token file takes priority.

The server connects once and holds the account lock until SIGINT/SIGTERM.
Messages received while it runs are stored in the account's inbox. Retention
defaults to 30 days and 10,000 entries; zero disables the respective bound.
Logs go to stderr; stdout remains empty. Remote unlink ends with exit code 3.

Sending text requires --allow-recipient (repeatable: a number, ACI, @username,
group:<id> or self; '*' allows everyone). The default allows nobody to send.
Read receipts require an explicit mark-read request. --read-only blocks both
sending and mark-read. These settings, the listen address, token file and inbox
limits also use daemon.* config keys or GOSIGNAL_DAEMON_* environment variables.
See docs/daemon.md for the API and reconnect behavior.`

func newDaemonCmd(clients *clientOpener, appOpts []app.Option) *cobra.Command {
	cmd := &cobra.Command{
		Use: "daemon", Short: "Local HTTP API for scripts and bots", Args: cobra.NoArgs,
	}
	cmd.AddCommand(newDaemonServeCmd(clients, appOpts))

	return cmd
}

func newDaemonServeCmd(clients *clientOpener, appOpts []app.Option) *cobra.Command {
	cmd := &cobra.Command{
		Use: "serve", Short: "Serve the account over authenticated loopback HTTP", Long: daemonServeLong,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			bindDaemonFlags(clients.cfg, cmd)

			opts := daemon.Options{
				Version: Version, Logger: slog.Default(), ReadOnly: clients.cfg.GetBool("daemon.read-only"),
				InboxMaxAge:   clients.cfg.GetDuration("daemon.inbox-max-age"),
				InboxMaxCount: clients.cfg.GetInt("daemon.inbox-max-count"),
			}
			if opts.InboxMaxAge < 0 || opts.InboxMaxCount < 0 {
				return errInboxLimits
			}

			allow, err := app.ParseAllowlist(configList(clients.cfg, "daemon.allow-recipient"))
			if err != nil {
				return fmt.Errorf("--allow-recipient: %w", err)
			}

			listen, err := loadDaemonListen(clients.cfg)
			if err != nil {
				return err
			}

			return serveDaemon(cmd, clients, listen, append(slices.Clone(appOpts), app.WithAllowlist(allow)), opts)
		},
	}
	flags := cmd.Flags()
	flags.String("listen", "", "serve HTTP on this required loopback address (e.g. 127.0.0.1:8765)")
	flags.String("token-file", "", "file with the bearer token (overrides daemon.token and GOSIGNAL_DAEMON_TOKEN)")
	flags.Bool("read-only", false, "reject sending and mark-read requests")
	flags.StringSlice("allow-recipient", nil,
		"user or group:<id> allowed to receive sends (repeatable; '*' allows everyone; default: nobody)")
	flags.Duration("inbox-max-age", defaultInboxMaxAge, "delete inbox entries received longer ago than this (0: keep)")
	flags.Int("inbox-max-count", defaultInboxMaxCount, "keep at most this many inbox entries (0: no limit)")

	return cmd
}

func bindDaemonFlags(cfg *viper.Viper, cmd *cobra.Command) {
	for _, name := range []string{
		"listen", "token-file", "read-only", "allow-recipient", "inbox-max-age", "inbox-max-count",
	} {
		cobra.CheckErr(cfg.BindPFlag("daemon."+name, cmd.Flags().Lookup(name)))
	}
}

func loadDaemonListen(cfg *viper.Viper) (listenConfig, error) {
	addr := cfg.GetString("daemon.listen")
	if addr == "" {
		return listenConfig{}, fmt.Errorf("--listen is required: %w", errNotLoopback)
	}

	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return listenConfig{}, fmt.Errorf("--listen: %w", err)
	}

	if ip := net.ParseIP(host); host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return listenConfig{}, errNotLoopback
	}

	token := cfg.GetString("daemon.token")
	if file := cfg.GetString("daemon.token-file"); file != "" {
		data, err := os.ReadFile(file)
		if err != nil {
			return listenConfig{}, fmt.Errorf("--token-file: %w", err)
		}

		token = string(data)
	}

	token = strings.TrimSpace(token)
	if len(token) < minTokenLength {
		return listenConfig{}, fmt.Errorf(
			"--listen needs --token-file, GOSIGNAL_DAEMON_TOKEN or daemon.token: %w", errShortToken)
	}

	return listenConfig{addr: addr, token: token}, nil
}

func serveDaemon(
	cmd *cobra.Command, clients *clientOpener, listen listenConfig, appOpts []app.Option, opts daemon.Options,
) error {
	ctx := cmd.Context()

	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", listen.addr)
	if err != nil {
		return fmt.Errorf("--listen: %w", err)
	}

	defer func() {
		// ServeHTTP also owns the listener; this defer covers startup failures.
		_ = listener.Close()
	}()

	address, ok := listener.Addr().(*net.TCPAddr)
	if !ok || !address.IP.IsLoopback() {
		return errNotLoopback
	}

	client, err := clients.open(ctx)
	if err != nil {
		return err
	}
	defer closeClient(client)

	err = client.Connect(ctx)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}

	slog.Info("daemon server listening", "url", "http://"+listener.Addr().String(), "read-only", opts.ReadOnly)

	//nolint:wrapcheck // daemon wraps errors
	return daemon.ServeHTTP(ctx, app.New(client, appOpts...), client.Events(), opts, listener, listen.token)
}
