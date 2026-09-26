//go:build unix

package cmd_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/go-signal/internal/signal/signaltest"
)

const hookFromSelf = "--hook-from=self"

func TestMCPServeHookErrors(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	plain := filepath.Join(dir, "plain")
	script := filepath.Join(dir, "hook.sh")

	for path, mode := range map[string]os.FileMode{plain: 0o600, script: 0o700} {
		err := os.WriteFile(path, []byte("#!/bin/sh\n"), mode)
		if err != nil {
			t.Fatal(err)
		}
	}

	for _, test := range []struct {
		args []string
		want string
	}{
		{[]string{"--on-message=hook.sh", hookFromSelf}, "absolute path"},
		{[]string{"--on-message=" + filepath.Join(dir, "missing"), hookFromSelf}, "--on-message"},
		{[]string{"--on-message=" + plain, hookFromSelf}, "no executable file"},
		{[]string{"--on-message=" + dir, hookFromSelf}, "no executable file"},
		{[]string{"--on-message=" + script}, "needs --hook-from"},
		{[]string{"--on-message=" + script, "--hook-from=alice"}, "--hook-from"},
		{[]string{"--on-message=" + script, hookFromSelf, "--on-message-timeout=-1s"}, "must not be negative"},
	} {
		session, wait := startMCP(t, t.Context(), &signaltest.Fake{Linked: []signal.Account{*testAccount()}}, test.args...)

		err := wait()
		if err == nil || !strings.Contains(err.Error(), test.want) {
			t.Errorf("%q: %v, want an error about %s", test.args, err, test.want)
		}

		if _, ok := session.next(); ok {
			t.Errorf("%q: wrote to stdout", test.args)
		}
	}
}

// TestMCPServeHook sets the hook in the config file and checks that a message runs it.
func TestMCPServeHook(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	script := filepath.Join(dir, "hook.sh")

	body := "#!/bin/sh\ncat > " + filepath.Join(dir, "entry.json") + "\n"

	err := os.WriteFile(script, []byte(body), 0o700) //nolint:gosec // it must run
	if err != nil {
		t.Fatal(err)
	}

	alice := signal.Recipient{ACI: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"}
	fake := &signaltest.Fake{
		Linked: []signal.Account{*testAccount()},
		Incoming: []signal.Event{&signal.Message{
			Envelope: signal.Envelope{Sender: alice, Chat: signal.Chat{Recipient: alice}, Timestamp: 1000},
			Body:     "hello hook",
		}},
	}
	session, wait := startMCPConfig(t, t.Context(), fake,
		"mcp:\n  on-message: "+script+"\n  hook-from: [\""+alice.ACI+"\"]\n")

	deadline := time.Now().Add(5 * time.Second)

	for {
		data, _ := os.ReadFile(filepath.Join(dir, "entry.json"))
		if strings.Contains(string(data), "hello hook") {
			break
		}

		if time.Now().After(deadline) {
			t.Fatalf("hook got %q, want the message", data)
		}

		time.Sleep(10 * time.Millisecond)
	}

	err = session.stdin.Close()
	if err != nil {
		t.Fatal(err)
	}

	err = wait()
	if err != nil {
		t.Fatalf("mcp serve: %v", err)
	}
}
