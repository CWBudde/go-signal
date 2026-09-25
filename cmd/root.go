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
	"syscall"
	"time"

	"github.com/cwbudde/go-signal/internal/output"
	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/go-signal/internal/store"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

const envPrefix = "GOSIGNAL"

var errInvalidLogFormat = errors.New("invalid log format (want text or json)")

// Execute builds the command tree and runs it.
func Execute() {
	ctx, stop := ossignal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := NewRootCmd().ExecuteContext(ctx)

	stop()

	if err != nil {
		slog.Error("command failed", "error", err)
		os.Exit(1)
	}
}

// Option customises the command tree built by NewRootCmd.
type Option func(*rootOptions)

type rootOptions struct {
	newClient signal.Factory
	loc       *time.Location
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

It aims to be a drop-in alternative to signal-cli (https://github.com/AsamK/signal-cli)
without requiring a Java runtime: link it as a secondary device, send and receive
messages, and run it as a JSON-RPC daemon for scripts and bots.`,
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

	flags := root.PersistentFlags()
	flags.StringVar(&cfgFile, "config", "", "config file (default is $XDG_CONFIG_HOME/go-signal/config.yaml)")
	flags.String("data-dir", store.DefaultDir(), "directory holding account data and keys")
	flags.StringP("account", "a", "", "account to use: E.164 number or ACI (required when several are linked)")
	flags.StringP("output", "o", "plain", "output format: plain or json")
	flags.BoolP("verbose", "v", false, "enable debug logging")
	flags.String("log-format", "text", "log format: text or json")

	for _, name := range []string{"data-dir", "account", "output", "verbose", "log-format"} {
		cobra.CheckErr(cfg.BindPFlag(name, flags.Lookup(name)))
	}

	root.AddCommand(
		newAccountCmd(clients, printers),
		newDevicesCmd(clients, printers),
		newLinkCmd(clients),
		newReceiveCmd(clients),
		newVersionCmd(),
	)

	return root
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
	cfg.SetEnvKeyReplacer(strings.NewReplacer("-", "_"))
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
