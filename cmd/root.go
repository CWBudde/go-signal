// Package cmd wires the go-signal command tree (Cobra) and its configuration (Viper).
package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	ossignal "os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/output"
	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/go-signal/internal/store"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

const envPrefix = "GOSIGNAL"

var errInvalidLogFormat = errors.New("invalid log format (want text or json)")

// Exit codes of the go-signal binary.
const (
	// ExitOK means the command succeeded.
	ExitOK = 0
	// ExitFailure is any error without a more specific code.
	ExitFailure = 1
	// ExitUnlinked means the device was unlinked from the account (signal.ErrDeviceUnlinked):
	// retrying won't help; delete the local data and link again.
	ExitUnlinked = 3
	// exitSignalBase + the signal number is the exit code after a forced exit (second signal).
	exitSignalBase = 128
)

// Execute builds the command tree, runs it and exits with ExitCode of its error.
// SIGINT/SIGTERM cancel the command's context so that it can shut down gracefully; a second
// signal exits right away.
func Execute() {
	sigs := make(chan os.Signal, 1)
	ossignal.Notify(sigs, os.Interrupt, syscall.SIGTERM)

	ctx, cancel := SignalContext(context.Background(), sigs, func(sig os.Signal) {
		slog.Warn("forced exit", "signal", sig.String())
		os.Exit(signalExitCode(sig))
	})
	err := NewRootCmd().ExecuteContext(ctx)

	ossignal.Stop(sigs)
	cancel()

	if err != nil {
		slog.Error("command failed", "error", err)
		os.Exit(ExitCode(err))
	}
}

// SignalContext returns a context that is cancelled by the first signal on sigs. The second
// signal calls force, which is meant to exit the process when the graceful shutdown hangs. The
// returned stop function cancels the context and stops watching sigs.
func SignalContext(
	parent context.Context, sigs <-chan os.Signal, force func(os.Signal),
) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	stopped := make(chan struct{})

	go func() {
		select {
		case sig := <-sigs:
			slog.Debug("shutting down; signal again to force", "signal", sig.String())
			cancel()
		case <-stopped:
			return
		}

		select {
		case sig := <-sigs:
			force(sig)
		case <-stopped:
		}
	}()

	var once sync.Once

	return ctx, func() {
		once.Do(func() {
			cancel()
			close(stopped)
		})
	}
}

// signalExitCode is the conventional exit code of a process killed by sig (130 for SIGINT).
func signalExitCode(sig os.Signal) int {
	if num, ok := sig.(syscall.Signal); ok {
		return exitSignalBase + int(num)
	}

	return ExitFailure
}

// ExitCode maps the error of a command to the process exit code.
func ExitCode(err error) int {
	switch {
	case err == nil:
		return ExitOK
	case errors.Is(err, signal.ErrDeviceUnlinked):
		return ExitUnlinked
	default:
		return ExitFailure
	}
}

// Option customises the command tree built by NewRootCmd.
type Option func(*rootOptions)

type rootOptions struct {
	newClient signal.Factory
	loc       *time.Location
	appOpts   []app.Option
}

// WithClientFactory replaces the signalmeow-backed client, e.g. with signaltest.Fake in tests.
func WithClientFactory(factory signal.Factory) Option {
	return func(o *rootOptions) {
		o.newClient = factory
	}
}

// WithLocation sets the time zone of plain output (default: local time), e.g. UTC in tests.
func WithLocation(loc *time.Location) Option {
	return func(o *rootOptions) {
		o.loc = loc
	}
}

// WithClock replaces time.Now for the use cases, e.g. for fixed message timestamps in tests.
func WithClock(now func() time.Time) Option {
	return func(o *rootOptions) {
		o.appOpts = append(o.appOpts, app.WithClock(now))
	}
}

// NewRootCmd returns the root command with all subcommands attached.
// Settings resolve with precedence flag > GOSIGNAL_* env > config file > default.
func NewRootCmd(opts ...Option) *cobra.Command {
	cfg := viper.New()

	rootOpts := rootOptions{newClient: signal.Open}
	for _, opt := range opts {
		opt(&rootOpts)
	}

	clients := &clientOpener{cfg: cfg, factory: rootOpts.newClient}
	printers := &printerFactory{cfg: cfg, loc: rootOpts.loc}

	var cfgFile string

	root := &cobra.Command{
		Use:   "go-signal",
		Short: "Command-line client for the Signal messenger",
		Long: `go-signal is a command-line client for the Signal messenger, written in Go.

It is a Java-free alternative to signal-cli (https://github.com/AsamK/signal-cli) with its
own command set: link it as a secondary device next to your phone, send and receive
messages, manage contacts and groups, and serve your account to AI assistants over MCP.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(_ *cobra.Command, _ []string) error {
			err := loadConfig(cfg, cfgFile)
			if err != nil {
				return err
			}

			_, err = output.ParseFormat(cfg.GetString("output"))
			if err != nil {
				return fmt.Errorf("output: %w", err)
			}

			return setupLogging(cfg)
		},
	}

	addGlobalFlags(root, cfg, &cfgFile)

	root.AddCommand(
		newAccountCmd(clients, printers),
		newContactsCmd(clients, printers, rootOpts.appOpts),
		newDeleteCmd(clients, printers, rootOpts.appOpts),
		newDevicesCmd(clients, printers),
		newGroupsCmd(clients, printers),
		newIdentitiesCmd(clients, printers),
		newLinkCmd(clients),
		newMCPCmd(clients, printers, rootOpts.loc, rootOpts.appOpts),
		newReactCmd(clients, printers, rootOpts.appOpts),
		newReceiveCmd(clients, printers),
		newSendCmd(clients, printers, rootOpts.appOpts),
		newVersionCmd(),
	)

	return root
}

// addGlobalFlags adds the persistent flags to root and binds all but --config to cfg.
func addGlobalFlags(root *cobra.Command, cfg *viper.Viper, cfgFile *string) {
	flags := root.PersistentFlags()
	flags.StringVar(cfgFile, "config", "", "config file (default is $XDG_CONFIG_HOME/go-signal/config.yaml)")
	flags.String("data-dir", store.DefaultDir(), "directory holding account data and keys")
	flags.StringP("account", "a", "", "account to use: E.164 number or ACI (required when several are linked)")
	flags.StringP("output", "o", "plain", "output format: plain or json")
	flags.BoolP("verbose", "v", false, "enable debug logging")
	flags.String("log-format", "text", "log format: text or json")

	for _, name := range []string{"data-dir", "account", "output", "verbose", "log-format"} {
		cobra.CheckErr(cfg.BindPFlag(name, flags.Lookup(name)))
	}
}

// clientOpener opens a signal.Client configured from the global flags.
type clientOpener struct {
	cfg     *viper.Viper
	factory signal.Factory
}

func (o *clientOpener) open(ctx context.Context) (signal.Client, error) {
	client, err := o.factory(ctx, signal.Options{
		DataDir: o.cfg.GetString("data-dir"),
		Account: o.cfg.GetString("account"),
		Logger:  slog.Default(),
	})
	if err != nil {
		return nil, fmt.Errorf("open client: %w", err)
	}

	return client, nil
}

// printerFactory creates output printers for the -o/--output format.
type printerFactory struct {
	cfg *viper.Viper
	loc *time.Location
}

func (f *printerFactory) printer(out io.Writer) (*output.Printer, error) {
	format, err := output.ParseFormat(f.cfg.GetString("output"))
	if err != nil {
		return nil, fmt.Errorf("output: %w", err)
	}

	return output.New(out, format, f.loc), nil
}

// closeClient closes client and logs a failure; used in defers.
func closeClient(client signal.Client) {
	err := client.Close()
	if err != nil {
		slog.Warn("close client", "error", err)
	}
}

// loadConfig reads the config file (if any) and environment variables.
func loadConfig(cfg *viper.Viper, cfgFile string) error {
	if cfgFile != "" {
		cfg.SetConfigFile(cfgFile)
	} else {
		configDir, err := os.UserConfigDir()
		if err != nil {
			return fmt.Errorf("locate config dir: %w", err)
		}

		cfg.AddConfigPath(filepath.Join(configDir, "go-signal"))
		cfg.SetConfigType("yaml")
		cfg.SetConfigName("config")
	}

	cfg.SetEnvPrefix(envPrefix)
	cfg.SetEnvKeyReplacer(strings.NewReplacer("-", "_", ".", "_"))
	cfg.AutomaticEnv()

	err := cfg.ReadInConfig()
	if err != nil {
		var notFound viper.ConfigFileNotFoundError
		if cfgFile != "" || !errors.As(err, &notFound) {
			return fmt.Errorf("read config: %w", err)
		}
	}

	return nil
}

// setupLogging configures the default slog logger. Logs go to stderr so that
// stdout stays reserved for command output (plain, JSON or JSON-RPC).
func setupLogging(cfg *viper.Viper) error {
	level := slog.LevelInfo
	if cfg.GetBool("verbose") {
		level = slog.LevelDebug
	}

	opts := &slog.HandlerOptions{Level: level}

	var handler slog.Handler

	switch format := cfg.GetString("log-format"); format {
	case "text":
		handler = slog.NewTextHandler(os.Stderr, opts)
	case "json":
		handler = slog.NewJSONHandler(os.Stderr, opts)
	default:
		return fmt.Errorf("%w: %q", errInvalidLogFormat, format)
	}

	slog.SetDefault(slog.New(handler))

	if used := cfg.ConfigFileUsed(); used != "" {
		slog.Debug("using config file", "path", used)
	}

	return nil
}
