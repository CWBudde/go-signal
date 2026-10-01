package mcp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// errPanic is the error of a request whose handler panicked.
var errPanic = errors.New("internal error")

// logCalls logs the tool calls with their duration and error, and turns a panic in any handler
// into an error for that request: one faulty tool must not take down the server and with it the
// connection to Signal.
func logCalls(logger *slog.Logger) sdk.Middleware {
	return func(next sdk.MethodHandler) sdk.MethodHandler {
		return func(ctx context.Context, method string, req sdk.Request) (sdk.Result, error) {
			tool := ""
			if call, ok := req.(*sdk.CallToolRequest); ok && call.Params != nil {
				tool = call.Params.Name
			}

			start := time.Now()
			res, err := recovered(ctx, logger, next, method, tool, req)

			if tool != "" {
				attrs := []any{"tool", tool, "duration", time.Since(start).Round(time.Millisecond)}

				if call, ok := res.(*sdk.CallToolResult); err == nil && ok && call.IsError {
					logger.WarnContext(ctx, "mcp: tool returned an error", attrs...)
				} else if err != nil {
					logger.WarnContext(ctx, "mcp: tool call failed", append(attrs, "error", err)...)
				} else {
					logger.DebugContext(ctx, "mcp: tool call", attrs...)
				}
			}

			return res, err
		}
	}
}

// recovered calls next and turns a panic into errPanic, logged with its stack.
func recovered(
	ctx context.Context, logger *slog.Logger, next sdk.MethodHandler, method, tool string, req sdk.Request,
) (sdk.Result, error) {
	var (
		res sdk.Result
		err error
	)

	func() {
		defer func() {
			cause := recover()
			if cause == nil {
				return
			}

			logger.ErrorContext(ctx, "mcp: handler panicked",
				"method", method, "tool", tool, "panic", cause, "stack", string(debug.Stack()))

			res, err = nil, fmt.Errorf("%w in %s: %v", errPanic, method, cause)
		}()

		res, err = next(ctx, method, req)
	}()

	return res, err
}
