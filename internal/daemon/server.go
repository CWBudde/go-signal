// Package daemon serves an authenticated local HTTP API over one Signal account.
package daemon

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/signal"
)

const (
	minTokenLength  = 16
	shutdownTimeout = 5 * time.Second
	headerTimeout   = 10 * time.Second
)

// Options configures the daemon's output, inbox retention and write policy.
type Options struct {
	Version       string
	Logger        *slog.Logger
	InboxMaxAge   time.Duration
	InboxMaxCount int
	ReadOnly      bool
}

type server struct {
	app    *app.App
	inbox  *app.Inbox
	opts   Options
	logger *slog.Logger
}

var errConfiguration = errors.New("invalid daemon configuration")

// ServeHTTP serves the account API, owning listener until ctx or receiving ends.
// The caller owns the connected Signal client. Every route requires token.
func ServeHTTP(
	ctx context.Context, a *app.App, events <-chan signal.Event, opts Options, listener net.Listener, token string,
) error {
	if listener != nil {
		defer func() { _ = listener.Close() }()
	}

	err := validate(a, opts, listener, token)
	if err != nil {
		return err
	}

	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}

	daemon := &server{
		app: a, inbox: a.Inbox(app.InboxOptions{MaxAge: opts.InboxMaxAge, MaxCount: opts.InboxMaxCount}),
		opts: opts, logger: logger,
	}

	return daemon.run(ctx, events, listener, token)
}

func validate(a *app.App, opts Options, listener net.Listener, token string) error {
	if len(token) < minTokenLength {
		return fmt.Errorf("%w: bearer token needs at least %d characters", errConfiguration, minTokenLength)
	}

	if listener == nil {
		return fmt.Errorf("%w: listener is required", errConfiguration)
	}

	address, ok := listener.Addr().(*net.TCPAddr)
	if !ok || !address.IP.IsLoopback() {
		return fmt.Errorf("%w: listener must use a loopback TCP address", errConfiguration)
	}

	if a == nil || opts.InboxMaxAge < 0 || opts.InboxMaxCount < 0 {
		return fmt.Errorf("%w: app and nonnegative retention are required", errConfiguration)
	}

	return nil
}

func (s *server) run(ctx context.Context, events <-chan signal.Event, listener net.Listener, token string) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	httpServer := &http.Server{
		Handler: s.handler(token), ReadHeaderTimeout: headerTimeout,
		ErrorLog:    slog.NewLogLogger(s.logger.Handler(), slog.LevelDebug),
		BaseContext: func(net.Listener) context.Context { return ctx },
	}

	type receiveResult struct {
		err        error
		contextErr error
	}

	received := make(chan receiveResult, 1)
	served := make(chan error, 1)

	go func() {
		var err error
		if events != nil {
			err = s.inbox.Run(ctx, events)
		}

		received <- receiveResult{err: err, contextErr: ctx.Err()}

		cancel()
	}()
	go func() { served <- httpServer.Serve(listener); cancel() }()

	<-ctx.Done()
	s.shutdown(ctx, httpServer)

	receivedResult, httpErr := <-received, <-served

	receiveErr := receivedResult.err
	if receiveErr != nil && (receivedResult.contextErr == nil ||
		!errors.Is(receiveErr, receivedResult.contextErr) || errors.Is(receiveErr, signal.ErrDeviceUnlinked)) {
		return fmt.Errorf("daemon receive: %w", receiveErr)
	}

	if httpErr != nil && !errors.Is(httpErr, http.ErrServerClosed) {
		return fmt.Errorf("daemon HTTP: %w", httpErr)
	}

	return nil
}

func (s *server) shutdown(ctx context.Context, httpServer *http.Server) {
	shutdownCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
	defer stop()

	err := httpServer.Shutdown(shutdownCtx)
	if err == nil {
		return
	}

	s.logger.DebugContext(ctx, "daemon: graceful HTTP shutdown incomplete", "error", err)

	err = httpServer.Close()
	if err != nil {
		s.logger.DebugContext(ctx, "daemon: close HTTP server", "error", err)
	}
}
