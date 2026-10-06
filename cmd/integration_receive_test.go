//go:build integration && (cgo || libsignal_go)

// CLI-level integration tests against Signal's production servers (PLAN.md §11.1). They build
// the go-signal binary with the backend of the test itself and run it as a real process, so that
// signal handling and exit codes are tested as users see them. Like the suite in
// internal/signal/integration_test.go they are opt-in, never run in CI and use a dedicated test
// account (docs/dev.md, "Integration tests").
//
//	GOSIGNAL_IT_DATA_DIR       data dir with the linked test account (required; the tests skip without)
//	GOSIGNAL_IT_ACCOUNT        account in it (number or ACI); empty selects the only one
//	GOSIGNAL_IT_TIMEOUT        how long to wait for the server and for messages (default 2m)
//	GOSIGNAL_IT_RECEIVE        "1" to run TestIntegrationReceiveInterrupt
//	GOSIGNAL_IT_PEER_DATA_DIR  data dir of a second linked account (the peer) that sends the test
//	                           message; without it the test asks a human to send it (see below)
//	GOSIGNAL_IT_PEER_ACCOUNT   account in the peer's data dir (number or ACI); empty selects the only one
//	GOSIGNAL_IT_UNLINKED_DIR   data dir of an account already marked as unlinked, e.g. the one that
//	                           TestIntegrationRemoteUnlink logs with GOSIGNAL_IT_KEEP_UNLINKED=1;
//	                           runs TestIntegrationReceiveUnlinked
//
// Run with -v so that the prompt for a manual message shows up while the test waits.

package cmd_test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/cmd"
	"github.com/cwbudde/go-signal/internal/signal"
)

const (
	itDefaultTimeout = 2 * time.Minute
	// itInterruptLimit is how fast receive --follow must exit after SIGINT ("within ~1 s").
	itInterruptLimit = 1500 * time.Millisecond
	// itDrainTimeout is the inactivity timeout of the one-shot receive after the interrupt.
	itDrainTimeout = "10s"
	// itUnlinkHint is the part of signal.UnlinkedError that tells the user how to clean up.
	itUnlinkHint = "account unlink --yes --local-only"
	itPollEvery  = 50 * time.Millisecond
)

// TestIntegrationReceiveInterrupt checks that SIGINT ends receive --follow quickly and normally,
// and that the message it printed was acknowledged: a later receive doesn't get it again.
//
// receive holds the account lock, so the test account can't send to itself while it runs. The
// message comes from the peer account in GOSIGNAL_IT_PEER_DATA_DIR (sent with the same binary),
// or, without it, from a human: the test prints a token to stderr and waits for a message that
// contains it, sent to the test account from another phone or to Note to Self from the test
// account's phone.
func TestIntegrationReceiveInterrupt(t *testing.T) { //nolint:paralleltest // one account, real process
	dataDir := itDataDir(t)
	if os.Getenv("GOSIGNAL_IT_RECEIVE") != "1" {
		t.Skip("GOSIGNAL_IT_RECEIVE not set to 1")
	}

	timeout := itTimeout(t)
	bin := itBuild(t)
	args := itGlobalArgs(dataDir, os.Getenv("GOSIGNAL_IT_ACCOUNT"))
	acc := itAccount(t, bin, args)
	t.Logf("account %s (%s)", acc.Number, acc.ACI)

	follow := itStart(t, bin, append(args, "receive", "--follow", "--output", "json")...)

	follow.waitFor(t, timeout, "queueEmpty", func(evt itEvent) bool { return evt.Type == "queueEmpty" })

	token := fmt.Sprintf("go-signal IT %d", time.Now().UnixNano()%1_000_000)
	itProduceMessage(t, bin, acc, token)

	msg := follow.waitFor(t, timeout, "the test message", func(evt itEvent) bool {
		return evt.Type == "message" && strings.Contains(evt.Body, token)
	})
	t.Logf("received %q at %d", msg.Body, msg.Timestamp)

	elapsed, code := follow.interrupt(t)
	t.Logf("exited %v after SIGINT with code %d", elapsed, code)

	// The first signal ends receive normally (exit 0); 128+signal is only for the forced exit
	// of a second signal (cmd.SignalExitCode).
	if code != cmd.ExitOK {
		t.Errorf("exit code %d after SIGINT, want %d\nstderr:\n%s", code, cmd.ExitOK, follow.stderr.String())
	}

	if elapsed > itInterruptLimit {
		t.Errorf("receive --follow took %v to exit after SIGINT, want at most %v", elapsed, itInterruptLimit)
	}

	stdout, stderr, code := itRun(t, bin, append(args, "receive", "--output", "json", "--timeout", itDrainTimeout)...)
	if code != cmd.ExitOK {
		t.Fatalf("one-shot receive: exit code %d\nstderr:\n%s", code, stderr)
	}

	for _, evt := range itParseEvents(t, stdout) {
		if evt.Type == "message" && strings.Contains(evt.Body, token) {
			t.Errorf("the message printed before SIGINT was delivered again: %s", evt.raw)
		}
	}
}

// TestIntegrationReceiveUnlinked checks that receive on an account marked as unlinked exits with
// code 3 and tells the user how to clean up.
func TestIntegrationReceiveUnlinked(t *testing.T) { //nolint:paralleltest // real process on a live account
	dataDir := os.Getenv("GOSIGNAL_IT_UNLINKED_DIR")
	if dataDir == "" {
		t.Skip("GOSIGNAL_IT_UNLINKED_DIR not set")
	}

	bin := itBuild(t)

	_, stderr, code := itRun(t, bin, append(itGlobalArgs(dataDir, ""), "receive")...)
	if code != cmd.ExitUnlinked {
		t.Errorf("exit code %d, want %d", code, cmd.ExitUnlinked)
	}

	if !strings.Contains(stderr, itUnlinkHint) {
		t.Errorf("stderr lacks the cleanup hint %q:\n%s", itUnlinkHint, stderr)
	}
}

// itProduceMessage gets a message containing token sent to acc: by the peer account if
// GOSIGNAL_IT_PEER_DATA_DIR is set, else by a human.
func itProduceMessage(t *testing.T, bin string, acc itAccountInfo, token string) {
	t.Helper()

	peerDir := os.Getenv("GOSIGNAL_IT_PEER_DATA_DIR")
	if peerDir == "" {
		// Bypass t.Log, which go test only shows at the end without -v.
		_, _ = fmt.Fprintf(os.Stderr, "\n>>> GOSIGNAL_IT_PEER_DATA_DIR not set: send a message containing\n"+
			">>>     %s\n>>> to %s (from another phone, or to Note to Self from its phone)\n\n", token, acc.Number)
		t.Logf("waiting for a manual message containing %q to %s", token, acc.Number)

		return
	}

	args := append(itGlobalArgs(peerDir, os.Getenv("GOSIGNAL_IT_PEER_ACCOUNT")), "send", acc.ACI, "-m", token)

	_, stderr, code := itRun(t, bin, args...)
	if code != cmd.ExitOK {
		t.Fatalf("send from the peer: exit code %d\nstderr:\n%s", code, stderr)
	}
}

// itDataDir returns GOSIGNAL_IT_DATA_DIR and skips the test without it.
func itDataDir(t *testing.T) string {
	t.Helper()

	dataDir := os.Getenv("GOSIGNAL_IT_DATA_DIR")
	if dataDir == "" {
		t.Skip("GOSIGNAL_IT_DATA_DIR not set")
	}

	return dataDir
}

// itTimeout returns GOSIGNAL_IT_TIMEOUT or its default.
func itTimeout(t *testing.T) time.Duration {
	t.Helper()

	value := os.Getenv("GOSIGNAL_IT_TIMEOUT")
	if value == "" {
		return itDefaultTimeout
	}

	timeout, err := time.ParseDuration(value)
	if err != nil {
		t.Fatalf("GOSIGNAL_IT_TIMEOUT: %v", err)
	}

	return timeout
}

func itGlobalArgs(dataDir, account string) []string {
	args := []string{dataDirFlag, dataDir}
	if account != "" {
		args = append(args, "--account", account)
	}

	return args
}

// itBuild builds the go-signal binary with the backend of this test binary (signal.Backend): the
// libsignal_go tag for the pure-Go backend, else cgo, which inherits CGO_ENABLED and
// CGO_LDFLAGS from the environment of go test.
func itBuild(t *testing.T) string {
	t.Helper()

	bin := filepath.Join(t.TempDir(), "go-signal")
	args := []string{"build", "-o", bin}

	if signal.Backend == "libsignal_go" {
		args = append(args, "-tags", "libsignal_go")
	}

	build := exec.CommandContext(t.Context(), "go", append(args, "github.com/cwbudde/go-signal")...)

	out, err := build.CombinedOutput()
	if err != nil {
		t.Fatalf("go build (backend %s): %v\n%s", signal.Backend, err, out)
	}

	t.Logf("built go-signal with backend %s", signal.Backend)

	return bin
}

// itRun runs the binary to completion and returns its stdout, stderr and exit code.
func itRun(t *testing.T, bin string, args ...string) (string, string, int) {
	t.Helper()

	var stdout, stderr bytes.Buffer

	run := exec.CommandContext(t.Context(), bin, args...)
	run.Stdout, run.Stderr = &stdout, &stderr

	code := itExitCode(t, run.Run())

	return stdout.String(), stderr.String(), code
}

func itExitCode(t *testing.T, err error) int {
	t.Helper()

	if err == nil {
		return 0
	}

	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("run go-signal: %v", err)
	}

	return exitErr.ExitCode()
}

type itAccountInfo struct {
	Number string `json:"number"`
	ACI    string `json:"aci"`
}

// itAccount returns the test account as account show reports it.
func itAccount(t *testing.T, bin string, args []string) itAccountInfo {
	t.Helper()

	stdout, stderr, code := itRun(t, bin, append(args, "account", "show", "--output", "json")...)
	if code != cmd.ExitOK {
		t.Fatalf("account show: exit code %d\nstderr:\n%s", code, stderr)
	}

	var shown struct {
		Account itAccountInfo `json:"account"`
	}

	err := json.Unmarshal([]byte(stdout), &shown)
	if err != nil || shown.Account.ACI == "" {
		t.Fatalf("account show: %v\n%s", err, stdout)
	}

	return shown.Account
}

// itEvent is the part of a receive event (docs/json.md) that the tests look at.
type itEvent struct {
	Type      string `json:"type"`
	Body      string `json:"body"`
	Timestamp uint64 `json:"timestamp"`
	raw       string
}

func itParseEvent(t *testing.T, line string) itEvent {
	t.Helper()

	var evt itEvent

	err := json.Unmarshal([]byte(line), &evt)
	if err != nil {
		t.Errorf("receive printed a line that isn't JSON: %v\n%s", err, line)
	}

	evt.raw = line

	return evt
}

func itParseEvents(t *testing.T, stdout string) []itEvent {
	t.Helper()

	var events []itEvent

	for line := range strings.Lines(stdout) {
		if line = strings.TrimSpace(line); line != "" {
			events = append(events, itParseEvent(t, line))
		}
	}

	return events
}

// itProcess is a running go-signal process whose stdout lines are collected as they come.
type itProcess struct {
	proc   *exec.Cmd
	stderr *itBuffer
	exited chan struct{}
	err    error // of Wait, set before exited is closed

	mu    sync.Mutex
	lines []string
	eof   bool
}

// itStart starts the binary; the test's cleanup kills it if it still runs.
func itStart(t *testing.T, bin string, args ...string) *itProcess {
	t.Helper()

	// An os.Pipe instead of StdoutPipe, so that Wait (and with it the exit time) doesn't depend
	// on reading stdout.
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}

	proc := &itProcess{
		proc:   exec.CommandContext(t.Context(), bin, args...),
		stderr: &itBuffer{},
		exited: make(chan struct{}),
	}
	proc.proc.Stdout, proc.proc.Stderr = writer, proc.stderr

	err = proc.proc.Start()
	_ = writer.Close()

	if err != nil {
		_ = reader.Close()

		t.Fatalf("start go-signal: %v", err)
	}

	go proc.read(reader)

	go func() {
		proc.err = proc.proc.Wait()
		close(proc.exited)
	}()

	t.Cleanup(func() {
		select {
		case <-proc.exited:
		default:
			_ = proc.proc.Process.Kill()
			<-proc.exited
		}

		if t.Failed() {
			t.Logf("stderr of %v:\n%s", args, proc.stderr.String())
		}
	})

	return proc
}

func (p *itProcess) read(reader *os.File) {
	defer func() { _ = reader.Close() }()

	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)

	for scanner.Scan() {
		p.mu.Lock()
		p.lines = append(p.lines, scanner.Text())
		p.mu.Unlock()
	}

	p.mu.Lock()
	p.eof = true
	p.mu.Unlock()
}

// waitFor returns the first event that matches, checking the lines printed so far and then
// those still to come. It fails the test after timeout or when the process ends first.
func (p *itProcess) waitFor(t *testing.T, timeout time.Duration, what string, match func(itEvent) bool) itEvent {
	t.Helper()

	deadline := time.Now().Add(timeout)
	next := 0

	for {
		p.mu.Lock()
		lines, eof := p.lines[next:], p.eof
		next = len(p.lines)
		p.mu.Unlock()

		for _, line := range lines {
			evt := itParseEvent(t, line)
			if match(evt) {
				return evt
			}
		}

		switch {
		case eof:
			t.Fatalf("go-signal ended before %s\nstderr:\n%s", what, p.stderr.String())
		case time.Now().After(deadline):
			t.Fatalf("no %s within %v", what, timeout)
		}

		time.Sleep(itPollEvery)
	}
}

// interrupt sends SIGINT and returns how long the process took to exit, and its exit code.
func (p *itProcess) interrupt(t *testing.T) (time.Duration, int) {
	t.Helper()

	start := time.Now()

	err := p.proc.Process.Signal(os.Interrupt)
	if err != nil {
		t.Fatalf("SIGINT: %v", err)
	}

	select {
	case <-p.exited:
	case <-time.After(10 * itInterruptLimit):
		t.Fatalf("go-signal still runs %v after SIGINT", 10*itInterruptLimit)
	}

	return time.Since(start), itExitCode(t, p.err)
}

// itBuffer is a bytes.Buffer that is safe to read while exec writes into it.
type itBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *itBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.Write(data) //nolint:wrapcheck // bytes.Buffer
}

func (b *itBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.String()
}
