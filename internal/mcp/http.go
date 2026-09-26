package mcp

import (
	"context"
	"crypto/subtle"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/modelcontextprotocol/go-sdk/auth"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// ErrNoToken means that ServeHTTP got no bearer token to require.
var ErrNoToken = errors.New("the HTTP transport needs a bearer token")

// HTTPPath is the path of the MCP endpoint that ServeHTTP serves.
const HTTPPath = "/mcp"

const (
	// httpSessionTimeout closes the sessions of clients that sent no request for this long.
	httpSessionTimeout = time.Hour
	// httpShutdownTimeout is how long ServeHTTP waits for open requests when ctx ends.
	httpShutdownTimeout = 5 * time.Second
	// httpHeaderTimeout bounds how long a client may take to send a request's headers.
	httpHeaderTimeout = 10 * time.Second
)

// ServeHTTP is Serve over MCP's streamable HTTP transport on listener, at HTTPPath. Every request must
// carry token as its bearer token ("Authorization: Bearer <token>"); others get 401. It serves any
// number of client sessions until ctx is cancelled, which ends it without error, and ends with
// the error of a lost connection or failing inbox like Serve.
func ServeHTTP(
	ctx context.Context, a *app.App, events <-chan signal.Event, opts Options, listener net.Listener, token string,
) error {
	if token == "" {
		return ErrNoToken
	}

	return serve(ctx, a, events, opts, func(ctx context.Context, server *Server) error {
		handler := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return server.Server },
			&sdk.StreamableHTTPOptions{
				Logger: slog.New(debugHandler{server.logger.Handler()}), SessionTimeout: httpSessionTimeout,
			})

		mux := http.NewServeMux()
		mux.Handle(HTTPPath, http.NewCrossOriginProtection().Handler(
			auth.RequireBearerToken(verifyToken(token), &auth.RequireBearerTokenOptions{AllowMissingExpiration: true})(
				handler)))

		httpServer := &http.Server{
			Handler:           mux,
			ReadHeaderTimeout: httpHeaderTimeout,
			ErrorLog:          slog.NewLogLogger(server.logger.Handler(), slog.LevelDebug),
			// Requests end with ctx, so that long polls and event streams don't hold up the shutdown.
			BaseContext: func(net.Listener) context.Context { return ctx },
		}

		served := make(chan error, 1)

		go func() { served <- httpServer.Serve(listener) }()

		select {
		case err := <-served:
			return err
		case <-ctx.Done():
		}

		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), httpShutdownTimeout)
		defer cancel()

		err := httpServer.Shutdown(shutdownCtx)
		if err != nil {
			_ = httpServer.Close()
		}

		<-served

		return nil
	})
}

// verifyToken accepts only token.
func verifyToken(token string) auth.TokenVerifier {
	want := []byte(token)

	return func(_ context.Context, got string, _ *http.Request) (*auth.TokenInfo, error) {
		if subtle.ConstantTimeCompare([]byte(got), want) != 1 {
			return nil, auth.ErrInvalidToken
		}

		return &auth.TokenInfo{}, nil
	}
}
