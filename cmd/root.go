// Package cmd wires the go-signal command tree (Cobra) and its configuration (Viper).
package cmd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

const envPrefix = "GOSIGNAL"

var errInvalidLogFormat = errors.New("invalid log format (want text or json)")

// Execute builds the command tree and runs it.
func Execute() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := NewRootCmd().ExecuteContext(ctx)

	stop()

	if err != nil {
		slog.Error("command failed", "error", err)
		os.Exit(1)
	}
}

// NewRootCmd returns the root command with all subcommands attached.
// Settings resolve with precedence flag > GOSIGNAL_* env > config file > default.
func NewRootCmd() *cobra.Command {
	cfg := viper.New()

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

			return setupLogging(cfg)
		},
	}

	flags := root.PersistentFlags()
	flags.StringVar(&cfgFile, "config", "", "config file (default is $XDG_CONFIG_HOME/go-signal/config.yaml)")
	flags.String("data-dir", defaultDataDir(), "directory holding account data and keys")
	flags.StringP("account", "a", "", "account (phone number) to operate on")
	flags.StringP("output", "o", "plain", "output format: plain or json")
	flags.BoolP("verbose", "v", false, "enable debug logging")
	flags.String("log-format", "text", "log format: text or json")

	for _, name := range []string{"data-dir", "account", "output", "verbose", "log-format"} {
		cobra.CheckErr(cfg.BindPFlag(name, flags.Lookup(name)))
	}

	root.AddCommand(
		newLinkCmd(cfg),
		newReceiveCmd(cfg),
		newVersionCmd(),
	)

	return root
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

func defaultDataDir() string {
	if dir := os.Getenv("XDG_DATA_HOME"); dir != "" {
		return filepath.Join(dir, "go-signal")
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "go-signal-data"
	}

	return filepath.Join(home, ".local", "share", "go-signal")
}
