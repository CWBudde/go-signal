package mcp_test

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/cwbudde/go-signal/internal/mcp"
	"github.com/cwbudde/go-signal/internal/output"
	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/go-signal/internal/signal/signaltest"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// syncBuffer is a bytes.Buffer that the server's goroutines can log to.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.Write(p) //nolint:wrapcheck // bytes.Buffer never fails
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.String()
}

// TestToolPanic checks that a panic in a tool fails that call, is logged with its stack and
// leaves the server running.
func TestToolPanic(t *testing.T) {
	t.Parallel()

	var logs syncBuffer

	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	fake := &signaltest.Fake{Linked: []signal.Account{testAccount()}}
	server := newServer(t, fake, mcp.Options{Logger: logger})

	sdk.AddTool(server.Server, &sdk.Tool{Name: "boom"},
		func(context.Context, *sdk.CallToolRequest, struct{}) (*sdk.CallToolResult, any, error) {
			panic("kaboom")
		})

	session := connectServer(t, server, testClient{})

	res, err := session.CallTool(t.Context(), &sdk.CallToolParams{Name: "boom"})
	if err == nil && (res == nil || !res.IsError) {
		t.Fatalf("boom = %+v, want an error", res)
	}

	if got := logs.String(); !strings.Contains(got, "handler panicked") || !strings.Contains(got, "kaboom") ||
		!strings.Contains(got, "goroutine") {
		t.Errorf("log lacks the panic and its stack:\n%s", got)
	}

	// The server still answers.
	var doc output.DoctorJSON

	call(t, session, doctorTool, nil, &doc)

	if got := logs.String(); !strings.Contains(got, "tool="+doctorTool) {
		t.Errorf("log lacks the doctor call:\n%s", got)
	}
}

// TestDoctorLocalTime checks the doctor tool without Options.Location: it formatted the
// connection times in a nil location and panicked, which ended the server.
func TestDoctorLocalTime(t *testing.T) {
	t.Parallel()

	fake := &signaltest.Fake{Linked: []signal.Account{testAccount()}}
	session := connectServer(t, newServer(t, fake, mcp.Options{Location: localTime()}), testClient{})

	if !fake.Push(&signal.Connection{State: signal.StateConnected}) {
		t.Fatal("push: not delivered")
	}

	waitForCheck(t, session, "connection", func(check output.CheckJSON) bool {
		return strings.HasPrefix(check.Detail, "connected since ")
	})
}
