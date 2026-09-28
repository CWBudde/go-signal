//go:build unix

package mcp_test

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/mcp"
	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/go-signal/internal/signal/signaltest"
)

// hookScript writes an executable shell script with body to a new directory and returns its
// path and the directory. "$DIR" in body is replaced with the directory.
func hookScript(t *testing.T, body string) (string, string) {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, "hook.sh")

	script := "#!/bin/sh\n" + strings.ReplaceAll(body, "$DIR", dir)

	err := os.WriteFile(path, []byte(script), 0o700) //nolint:gosec // it must run
	if err != nil {
		t.Fatal(err)
	}

	return path, dir
}

// hookFrom parses the --hook-from entries.
func hookFrom(t *testing.T, entries ...string) *app.Allowlist {
	t.Helper()

	list, err := app.ParseAllowlist(entries)
	if err != nil {
		t.Fatal(err)
	}

	return list
}

// waitForLines waits until the file has n lines and returns them.
func waitForLines(t *testing.T, path string, n int) []string {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)

	for {
		data, _ := os.ReadFile(path)
		lines := strings.Split(strings.TrimSpace(string(data)), "\n")

		if len(data) > 0 && len(lines) >= n {
			return lines
		}

		if time.Now().After(deadline) {
			t.Fatalf("%s: %q, want %d lines", path, data, n)
		}

		time.Sleep(10 * time.Millisecond)
	}
}

func textMessage(sender signal.Recipient, chat signal.Chat, ts uint64, body string) *signal.Message {
	return &signal.Message{Envelope: signal.Envelope{Sender: sender, Chat: chat, Timestamp: ts}, Body: body}
}

// TestHook pushes messages that must not run the hook and then two that must, and checks that
// only those two ran it, in order, with the entry on stdin and the environment set.
func TestHook(t *testing.T) {
	t.Parallel()

	script, dir := hookScript(t, `cat > "$DIR/$GOSIGNAL_ENTRY_ID.json"
echo "$GOSIGNAL_ENTRY_ID $GOSIGNAL_CHAT $GOSIGNAL_SENDER" >> "$DIR/runs"
echo "ran $GOSIGNAL_ENTRY_ID"
`)

	fake := toolsFake()
	connectWith(t, fake, mcp.Options{OnMessage: script, HookFrom: hookFrom(t, aliceNumber)}, testClient{})

	own := signal.Recipient{ACI: testAccount().ACI}
	aliceRcpt := signal.Recipient{ACI: aliceACI, Number: aliceNumber}
	aliceChat := signal.Chat{Recipient: aliceRcpt}
	bobRcpt := signal.Recipient{ACI: bobACI}

	sync := textMessage(own, aliceChat, 2, "our own")
	sync.Sync = true

	for _, evt := range []signal.Event{
		textMessage(bobRcpt, signal.Chat{Recipient: bobRcpt}, 1, "not allowed"),
		sync,
		&signal.Reaction{
			Envelope: signal.Envelope{Sender: aliceRcpt, Chat: aliceChat, Timestamp: 3},
			Emoji:    "👍", TargetAuthor: own, TargetTimestamp: 2,
		},
		textMessage(aliceRcpt, signal.Chat{GroupID: familyID}, 4, "in a group"),
		textMessage(aliceRcpt, aliceChat, 5, "hi"),
		textMessage(aliceRcpt, aliceChat, 6, "again"),
	} {
		if !fake.Push(evt) {
			t.Fatal("push failed")
		}
	}

	runs := waitForLines(t, filepath.Join(dir, "runs"), 2)
	want := []string{"5 " + aliceACI + " " + aliceNumber, "6 " + aliceACI + " " + aliceNumber}

	if strings.Join(runs, "|") != strings.Join(want, "|") {
		t.Errorf("runs %q, want %q", runs, want)
	}

	data, err := os.ReadFile(filepath.Join(dir, "5.json"))
	if err != nil {
		t.Fatal(err)
	}

	var entry struct {
		ID     string         `json:"id"`
		Unread bool           `json:"unread"`
		Event  map[string]any `json:"event"`
	}

	err = json.Unmarshal(data, &entry)
	if err != nil || entry.ID != "5" || !entry.Unread || entry.Event["body"] != "hi" {
		t.Errorf("stdin %s (%v), want entry 5 with body hi", data, err)
	}
}

// TestHookQueueOverflow holds the first run until the queue fills, then checks that overflow
// leaves messages in the inbox, warns, and does not block receiving or displace queued runs.
func TestHookQueueOverflow(t *testing.T) {
	t.Parallel()

	script, dir := hookScript(t, `set -eu
if [ "$GOSIGNAL_ENTRY_ID" = 1 ]; then
    echo started > "$DIR/started"
    while [ ! -f "$DIR/release" ]; do sleep 0.01; done
fi
cat > /dev/null
echo "$GOSIGNAL_ENTRY_ID" >> "$DIR/runs"
`)

	logPath := filepath.Join(dir, "warnings")
	logger := hookWarningLogger(t, logPath)

	fake := toolsFake()
	session := connectWith(t, fake, mcp.Options{
		OnMessage: script, HookFrom: hookFrom(t, aliceNumber),
		Logger: logger,
	}, testClient{})

	// Release the child before receive cleanup even if an assertion fails while it is held.
	release := func() {
		err := os.WriteFile(filepath.Join(dir, "release"), nil, 0o600)
		if err != nil {
			t.Error(err)
		}
	}
	t.Cleanup(release)

	pushHookMessages(t, fake, 1, 1)
	waitForLines(t, filepath.Join(dir, "started"), 1)

	// The documented capacity is 64 waiting messages, besides the running hook. Two more
	// must be dropped from the hook queue. Bound the wait so a blocking offer fails cleanly.
	const queued, overflow = 64, 2

	const total = 1 + queued + overflow

	pushHookMessages(t, fake, 2, total)
	checkHookOverflowWarnings(t, logPath, queued+2, overflow)

	var got messages

	call(t, session, messagesList, map[string]any{"limit": total}, &got)
	checkHookInbox(t, got, total)

	release()
	waitForLines(t, filepath.Join(dir, "runs"), queued+1)

	// A fresh message after draining is a barrier: preceding accepted entries must have run
	// exactly once, in order, and neither overflow entry may have been retried.
	pushHookMessages(t, fake, total+1, total+1)

	runs := waitForLines(t, filepath.Join(dir, "runs"), queued+2)
	want := make([]string, 0, queued+2)

	for id := range queued + 1 {
		want = append(want, strconv.Itoa(id+1))
	}

	want = append(want, strconv.Itoa(total+1))
	if !slices.Equal(runs, want) {
		t.Errorf("runs %q, want %q", runs, want)
	}
}

func hookWarningLogger(t *testing.T, path string) *slog.Logger {
	t.Helper()

	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = file.Close() })

	return slog.New(slog.NewJSONHandler(file, &slog.HandlerOptions{Level: slog.LevelWarn}))
}

// pushHookMessages bounds the wait for receive so a blocking queue offer fails the test.
func pushHookMessages(t *testing.T, fake *signaltest.Fake, first, last uint64) {
	t.Helper()

	pushed := make(chan bool, 1)

	go func() {
		for id := first; id <= last; id++ {
			if !fake.Push(photoMessage(id)) {
				pushed <- false

				return
			}
		}

		pushed <- true
	}()

	select {
	case ok := <-pushed:
		if !ok {
			t.Fatal("push failed")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("receiving blocked while pushing hook messages")
	}
}

func checkHookOverflowWarnings(t *testing.T, path string, first, count int) {
	t.Helper()

	warnings := waitForLines(t, path, count)
	if len(warnings) != count {
		t.Fatalf("warnings %q, want %d", warnings, count)
	}

	for i, line := range warnings {
		var warning struct {
			Level string `json:"level"`
			Msg   string `json:"msg"`
			Entry string `json:"entry"`
		}

		err := json.Unmarshal([]byte(line), &warning)
		if err != nil {
			t.Fatal(err)
		}

		if warning.Level != "WARN" || warning.Msg != "hook: queue full, message left out" ||
			warning.Entry != strconv.Itoa(first+i) {
			t.Errorf("overflow warning: %s", line)
		}
	}
}

func checkHookInbox(t *testing.T, got messages, total int) {
	t.Helper()

	if len(got.Messages) != total || got.Cursor != strconv.Itoa(total) || got.More {
		t.Fatalf("inbox: %+v, want all %d messages", got, total)
	}

	for i, msg := range got.Messages {
		if msg.ID != strconv.Itoa(i+1) || !msg.Unread || msg.Event["body"] != photoMessage(1).Body {
			t.Errorf("inbox message %d: %+v", i+1, msg)
		}
	}
}

// TestHookTimeout checks that a run that takes too long is killed, with what it started, and
// that the next message still runs the hook.
func TestHookTimeout(t *testing.T) {
	t.Parallel()

	script, dir := hookScript(t, `echo "$GOSIGNAL_ENTRY_ID" >> "$DIR/runs"
sleep 30
echo "$GOSIGNAL_ENTRY_ID" >> "$DIR/finished"
`)

	fake := &signaltest.Fake{Linked: []signal.Account{testAccount()}}
	connectWith(t, fake, mcp.Options{
		OnMessage: script, HookFrom: hookFrom(t, app.AllowAll), HookTimeout: 100 * time.Millisecond,
	}, testClient{})

	aliceRcpt := signal.Recipient{ACI: aliceACI}
	start := time.Now()

	for ts := range uint64(2) {
		if !fake.Push(textMessage(aliceRcpt, signal.Chat{Recipient: aliceRcpt}, ts+1, "slow")) {
			t.Fatal("push failed")
		}
	}

	waitForLines(t, filepath.Join(dir, "runs"), 2)

	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("two runs took %v, want them killed after 100ms", elapsed)
	}

	_, err := os.Stat(filepath.Join(dir, "finished"))
	if !os.IsNotExist(err) {
		t.Errorf("a run finished (%v), want it killed", err)
	}
}

// TestHookStalledLookup checks that the server stops while resolving --hook-from hangs.
func TestHookStalledLookup(t *testing.T) {
	t.Parallel()

	script, _ := hookScript(t, "exit 0\n")
	fake := toolsFake()
	fake.ResolveHangs = true

	client, err := fake.Factory(t.Context(), signal.Options{})
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = client.Close() })

	err = client.Connect(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	server := mcp.NewServer(app.New(client), mcp.Options{OnMessage: script, HookFrom: hookFrom(t, aliceNumber)})
	ctx, cancel := context.WithCancel(t.Context())
	received := make(chan error, 1)

	go func() { received <- server.Receive(ctx, client.Events()) }()

	aliceRcpt := signal.Recipient{ACI: aliceACI}
	if !fake.Push(textMessage(aliceRcpt, signal.Chat{Recipient: aliceRcpt}, 1, "hi")) {
		t.Fatal("push failed")
	}

	cancel()

	select {
	case err := <-received:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("receive: %v, want it cancelled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("receive did not end while resolving --hook-from hangs")
	}
}
