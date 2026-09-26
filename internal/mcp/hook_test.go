//go:build unix

package mcp_test

import (
	"encoding/json"
	"os"
	"path/filepath"
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
