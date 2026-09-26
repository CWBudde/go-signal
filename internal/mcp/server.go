// Package mcp exposes a linked account to MCP clients (Claude Code, Claude Desktop, other agents)
// as a Model Context Protocol server. The tools call internal/app, like the CLI commands do.
package mcp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/cwbudde/go-signal/internal/app"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Name is the server name reported to clients.
const Name = "go-signal"

const instructions = `go-signal gives access to one linked Signal account. ` +
	`Use account_show to see which account (number and ACI) this server acts for, ` +
	`contacts_list and contacts_show to find users, groups_list and groups_show for groups and their members, ` +
	`and identities_list for the users' identity keys (safety numbers).`

// Options configures the server.
type Options struct {
	// Version is the server version reported to clients (the `version` command's).
	Version string
	// Logger receives the SDK's logs, demoted to debug; nil means slog.Default().
	Logger *slog.Logger
	// Location is the time zone of the tools' text output (nil means time.Local); structured
	// output is in UTC.
	Location *time.Location
}

// NewServer returns an MCP server whose tools run on a.
func NewServer(a *app.App, opts Options) *sdk.Server {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}

	server := sdk.NewServer(&sdk.Implementation{Name: Name, Title: "Signal", Version: opts.Version}, &sdk.ServerOptions{
		Instructions: instructions,
		Logger:       slog.New(debugHandler{logger.Handler()}),
		// No "logging" capability: logs go to stderr, not to the client.
		Capabilities: &sdk.ServerCapabilities{},
	})

	addReadTools(server, &tools{app: a, loc: opts.Location, logger: logger})

	return server
}

// Serve runs an MCP server on a over newline-delimited JSON-RPC on in and out (stdin and stdout
// for `mcp serve`) until the client closes in or ctx is cancelled; both end it without error.
// Nothing but protocol messages is written to out.
func Serve(ctx context.Context, a *app.App, opts Options, in io.Reader, out io.Writer) error {
	transport := &sdk.IOTransport{Reader: io.NopCloser(in), Writer: nopWriteCloser{out}}

	err := NewServer(a, opts).Run(ctx, transport)

	switch {
	case err == nil, errors.Is(err, io.EOF), errors.Is(err, context.Canceled):
		return nil
	default:
		return fmt.Errorf("mcp server: %w", err)
	}
}

type nopWriteCloser struct {
	io.Writer
}

func (nopWriteCloser) Close() error { return nil }

// debugHandler logs every record at debug level: the SDK reports routine events (session start
// and end, a cancelled run) at info or error level.
type debugHandler struct {
	slog.Handler
}

func (h debugHandler) Enabled(ctx context.Context, _ slog.Level) bool {
	return h.Handler.Enabled(ctx, slog.LevelDebug)
}

func (h debugHandler) Handle(ctx context.Context, rec slog.Record) error {
	rec.Level = slog.LevelDebug

	return h.Handler.Handle(ctx, rec) //nolint:wrapcheck // a handler passes through
}

func (h debugHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return debugHandler{h.Handler.WithAttrs(attrs)}
}

func (h debugHandler) WithGroup(name string) slog.Handler {
	return debugHandler{h.Handler.WithGroup(name)}
}
