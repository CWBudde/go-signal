package cmd_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/cmd"
	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/go-signal/internal/signal/signaltest"
)

const (
	mcpCmd   = "mcp"
	serveCmd = "serve"
	// mcpChildEnv makes TestMCPServeChild run `mcp serve` on the real stdin/stdout.
	mcpChildEnv = "GOSIGNAL_TEST_MCP_CHILD"
)

// rpcFrame is the part of a JSON-RPC 2.0 message the tests look at.
type rpcFrame struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   json.RawMessage `json:"error"`
}

// mcpSession talks to an `mcp serve` over its stdin and stdout and fails the test on any stdout
// line that isn't a JSON-RPC 2.0 message.
type mcpSession struct {
	t     *testing.T
	stdin io.WriteCloser
	lines *bufio.Scanner
}

func (s *mcpSession) send(msg string) {
	s.t.Helper()

	_, err := io.WriteString(s.stdin, msg+"\n")
	if err != nil {
		s.t.Fatalf("write %s: %v", msg, err)
	}
}

// next reads the next stdout frame; ok is false at EOF.
func (s *mcpSession) next() (rpcFrame, bool) {
	s.t.Helper()

	if !s.lines.Scan() {
		err := s.lines.Err()
		if err != nil {
			s.t.Fatalf("read stdout: %v", err)
		}

		return rpcFrame{}, false
	}

	line := s.lines.Bytes()

	var frame rpcFrame

	err := json.Unmarshal(line, &frame)
	if err != nil || frame.JSONRPC != "2.0" {
		s.t.Fatalf("stdout line is not a JSON-RPC message: %q", line)
	}

	return frame, true
}

// call sends a request and returns its result.
func (s *mcpSession) call(id int, method, params string) json.RawMessage {
	s.t.Helper()

	s.send(`{"jsonrpc":"2.0","id":` + strconv.Itoa(id) + `,"method":"` + method + `","params":` + params + `}`)

	frame, ok := s.next()
	if !ok {
		s.t.Fatalf("%s: stdout closed", method)
	}

	if frame.Error != nil {
		s.t.Fatalf("%s: %s", method, frame.Error)
	}

	return frame.Result
}

// exchange initializes the session, lists the tools and closes stdin; it returns the tool names.
// Every line on stdout until EOF must be a JSON-RPC message.
func (s *mcpSession) exchange() []string {
	s.t.Helper()

	s.call(1, "initialize",
		`{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test","version":"0"}}`)
	s.send(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)

	var tools struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}

	err := json.Unmarshal(s.call(2, "tools/list", "{}"), &tools)
	if err != nil {
		s.t.Fatalf("tools/list result: %v", err)
	}

	err = s.stdin.Close()
	if err != nil {
		s.t.Fatal(err)
	}

	for {
		_, ok := s.next()
		if !ok {
			break
		}
	}

	names := make([]string, 0, len(tools.Tools))
	for _, tool := range tools.Tools {
		names = append(names, tool.Name)
	}

	return names
}

// startMCP runs `mcp serve` with args in process with piped stdin/stdout. wait returns the
// command's error once it ends.
func startMCP(
	t *testing.T, ctx context.Context, fake *signaltest.Fake, args ...string,
) (*mcpSession, func() error) {
	t.Helper()

	cfgFile := filepath.Join(t.TempDir(), "config.yaml")

	err := os.WriteFile(cfgFile, nil, 0o600)
	if err != nil {
		t.Fatal(err)
	}

	stdinR, stdinW := io.Pipe()
	stdoutR, stdoutW := io.Pipe()

	//nolint:contextcheck // the command gets ctx through ExecuteContext
	root := cmd.NewRootCmd(cmd.WithClientFactory(fake.Factory))
	root.SetIn(stdinR)
	root.SetOut(stdoutW)
	root.SetArgs(append([]string{"--config=" + cfgFile, dataDirFlag, t.TempDir(), mcpCmd, serveCmd}, args...))

	done := make(chan error, 1)

	go func() {
		err := root.ExecuteContext(ctx)
		_ = stdoutW.Close()
		_ = stdinR.Close()

		done <- err
	}()

	session := &mcpSession{t: t, stdin: stdinW, lines: bufio.NewScanner(stdoutR)}

	return session, func() error {
		select {
		case err := <-done:
			if !fake.AllClosed() {
				t.Error("client was not closed")
			}

			return err
		case <-time.After(5 * time.Second):
			t.Fatal("mcp serve did not end")

			return nil
		}
	}
}

func TestMCPServe(t *testing.T) {
	t.Parallel()

	fake := &signaltest.Fake{Linked: []signal.Account{*testAccount()}}
	session, wait := startMCP(t, t.Context(), fake)

	tools := session.exchange()
	if len(tools) == 0 {
		t.Error("no tools listed")
	}

	// stdin EOF ends the server normally.
	err := wait()
	if err != nil {
		t.Fatalf("mcp serve: %v", err)
	}

	if got := fake.Connects(); len(got) != 1 || got[0] != testAccount().ACI {
		t.Errorf("connects %v, want one to %s", got, testAccount().ACI)
	}
}

func TestMCPServeInterrupted(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	session, wait := startMCP(t, ctx, &signaltest.Fake{Linked: []signal.Account{*testAccount()}})
	session.call(1, "initialize",
		`{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test","version":"0"}}`)

	// SIGINT/SIGTERM cancel the context; that ends the server normally, with stdin still open.
	cancel()

	err := wait()
	if err != nil {
		t.Fatalf("mcp serve: %v", err)
	}
}

func TestMCPServeStartupErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		fake *signaltest.Fake
		want error
	}{
		{"not linked", &signaltest.Fake{}, signal.ErrNotLinked},
		{"in use", &signaltest.Fake{Linked: []signal.Account{*testAccount()}, InUse: true}, signal.ErrAccountInUse},
		{"unlinked", &signaltest.Fake{Linked: []signal.Account{unlinkedAccount()}}, signal.ErrDeviceUnlinked},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// The server fails before it reads stdin or writes anything to stdout.
			session, wait := startMCP(t, t.Context(), test.fake)

			err := wait()
			if !errors.Is(err, test.want) {
				t.Fatalf("got %v, want %v", err, test.want)
			}

			if _, ok := session.next(); ok {
				t.Error("wrote to stdout")
			}
		})
	}

	t.Run("unlinked exit code", func(t *testing.T) {
		t.Parallel()

		_, wait := startMCP(t, t.Context(), &signaltest.Fake{Linked: []signal.Account{unlinkedAccount()}})
		wantUnlinked(t, wait())
	})
}

// TestMCPServeInbox reads a message that was waiting on the server through messages_list.
func TestMCPServeInbox(t *testing.T) {
	t.Parallel()

	alice := signal.Recipient{ACI: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"}
	fake := &signaltest.Fake{
		Linked: []signal.Account{*testAccount()},
		Incoming: []signal.Event{&signal.Message{
			Envelope: signal.Envelope{Sender: alice, Chat: signal.Chat{Recipient: alice}, Timestamp: 1000},
			Body:     "hello agent",
		}},
	}
	session, wait := startMCP(t, t.Context(), fake)

	session.call(1, "initialize",
		`{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test","version":"0"}}`)
	session.send(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)

	// The receive loop runs next to the server: wait until the message is in the inbox.
	result := session.call(2, "tools/call", `{"name":"messages_wait","arguments":{"cursor":"0","timeout":5}}`)
	if !strings.Contains(string(result), "hello agent") {
		t.Errorf("messages_wait: %s", result)
	}

	err := session.stdin.Close()
	if err != nil {
		t.Fatal(err)
	}

	err = wait()
	if err != nil {
		t.Fatalf("mcp serve: %v", err)
	}

	if fake.Delivered() != 1 {
		t.Errorf("delivered %d events, want the message", fake.Delivered())
	}
}

// TestMCPServeUnlinkedWhileRunning checks that the server ends with exit code 3 when the device
// is unlinked while it runs, although the client keeps stdin open.
func TestMCPServeUnlinkedWhileRunning(t *testing.T) {
	t.Parallel()

	fake := &signaltest.Fake{
		Linked:   []signal.Account{*testAccount()},
		Incoming: []signal.Event{&signal.Connection{State: signal.StateLoggedOut}},
	}
	session, wait := startMCP(t, t.Context(), fake)

	defer session.stdin.Close()

	wantUnlinked(t, wait())
}

func TestMCPServeInboxLimits(t *testing.T) {
	t.Parallel()

	for _, arg := range []string{"--inbox-max-age=-1h", "--inbox-max-count=-1"} {
		_, wait := startMCP(t, t.Context(), &signaltest.Fake{Linked: []signal.Account{*testAccount()}}, arg)

		err := wait()
		if err == nil || !strings.Contains(err.Error(), "must not be negative") {
			t.Errorf("%s: %v, want an error", arg, err)
		}
	}
}

// TestMCPServeStdout runs `mcp serve` in a child process (TestMCPServeChild) with debug logging
// and checks that its real stdout carries nothing but JSON-RPC messages, while the logs go to
// stderr.
func TestMCPServeStdout(t *testing.T) {
	t.Parallel()

	child := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestMCPServeChild$")
	child.Env = append(os.Environ(), mcpChildEnv+"=1", "XDG_CONFIG_HOME="+t.TempDir())

	var stderr bytes.Buffer

	child.Stderr = &stderr

	stdin, err := child.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}

	stdout, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}

	err = child.Start()
	if err != nil {
		t.Fatal(err)
	}

	session := &mcpSession{t: t, stdin: stdin, lines: bufio.NewScanner(stdout)}
	session.exchange()

	err = child.Wait()
	if err != nil {
		t.Fatalf("child: %v\nstderr:\n%s", err, stderr.String())
	}

	if !strings.Contains(stderr.String(), "mcp server ready") {
		t.Errorf("debug log missing on stderr:\n%s", stderr.String())
	}
}

// TestMCPServeChild is the process TestMCPServeStdout starts. It exits right after the command,
// before the test framework prints its summary to stdout.
func TestMCPServeChild(t *testing.T) {
	t.Parallel()

	if os.Getenv(mcpChildEnv) == "" {
		t.Skip("only runs as the child of TestMCPServeStdout")
	}

	fake := &signaltest.Fake{Linked: []signal.Account{*testAccount()}}

	root := cmd.NewRootCmd(cmd.WithClientFactory(fake.Factory))
	root.SetArgs([]string{"-v", dataDirFlag, t.TempDir(), mcpCmd, serveCmd})

	err := root.Execute()
	if err != nil {
		os.Exit(cmd.ExitCode(err))
	}

	os.Exit(cmd.ExitOK)
}
