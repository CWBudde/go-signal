package cmd_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/cmd"
	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/go-signal/internal/signal/signaltest"
)

func TestDaemonEnvironmentValidationBeforeOpen(t *testing.T) {
	for _, test := range []struct {
		key, value, want string
	}{
		{key: "INBOX_MAX_AGE", value: "-1h", want: daemonNegative},
		{key: "INBOX_MAX_COUNT", value: "-1", want: daemonNegative},
		{key: "ALLOW_RECIPIENT", value: daemonInvalidChat, want: allowRecipientFlag},
		{key: "LISTEN", value: "0.0.0.0:8765", want: daemonLoopback},
		{key: "TOKEN", value: "short", want: daemonShortToken},
	} {
		t.Run(test.key, func(t *testing.T) {
			t.Setenv("GOSIGNAL_DAEMON_"+test.key, test.value)

			root := cmd.NewRootCmd(cmd.WithClientFactory(func(context.Context, signal.Options) (signal.Client, error) {
				t.Error("invalid environment opened the account")

				return nil, signal.ErrNotLinked
			}))
			file := daemonConfigFile(t, "daemon:\n  listen: 127.0.0.1:8765\n  token: "+httpToken+"\n")
			root.SetArgs([]string{"--config=" + file, daemonCmd, serveCmd})

			err := root.ExecuteContext(t.Context())
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Errorf("got %v, want %q", err, test.want)
			}
		})
	}
}

func TestDaemonListenTokenPrecedence(t *testing.T) {
	const otherToken = "other-bearer-token-with-enough-length"

	for _, name := range []string{
		daemonConfigCase, daemonEnvCase, "listen flag", "config token file", "environment token file", "token file flag",
	} {
		t.Run(name, func(t *testing.T) {
			addr := freeAddr(t)
			tokenFile := daemonConfigFile(t, " \n"+httpToken+"\n\t")
			config := "daemon:\n  listen: " + addr + "\n  token: " + httpToken + "\n"

			var args []string

			switch name {
			case daemonConfigCase:
			case daemonEnvCase:
				config = "daemon:\n  listen: 0.0.0.0:8765\n  token: short\n"

				t.Setenv("GOSIGNAL_DAEMON_LISTEN", addr)
				t.Setenv("GOSIGNAL_DAEMON_TOKEN", httpToken)
			case "listen flag":
				t.Setenv("GOSIGNAL_DAEMON_LISTEN", "0.0.0.0:8765")

				args = []string{"--listen=" + addr}
			case "config token file":
				config += "  token-file: " + tokenFile + "\n"

				t.Setenv("GOSIGNAL_DAEMON_TOKEN", otherToken)
			case "environment token file":
				config += "  token-file: missing\n"

				t.Setenv("GOSIGNAL_DAEMON_TOKEN", otherToken)
				t.Setenv("GOSIGNAL_DAEMON_TOKEN_FILE", tokenFile)
			case "token file flag":
				t.Setenv("GOSIGNAL_DAEMON_TOKEN", otherToken)
				t.Setenv("GOSIGNAL_DAEMON_TOKEN_FILE", "missing")

				args = []string{"--token-file=" + tokenFile}
			}

			session := startDaemon(t, &signaltest.Fake{Linked: []signal.Account{*testAccount()}}, config, addr, args...)
			session.ready(t)

			if status, _ := session.request(t, http.MethodGet, "/v1/health", otherToken, ""); status != http.StatusUnauthorized {
				t.Errorf("lower priority token accepted: HTTP %d", status)
			}
		})
	}
}

// seedDaemonInbox stores entries without connecting; daemon startup must apply its configured
// retention before it receives the queue-empty marker.
func seedDaemonInbox(t *testing.T, fake *signaltest.Fake) {
	t.Helper()

	client, err := fake.Factory(t.Context(), signal.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	for index, age := range []time.Duration{31 * 24 * time.Hour, 2 * time.Hour, 30 * time.Minute} {
		received := time.Now().Add(-age)

		_, err := client.InboxAdd(t.Context(), signal.InboxEntry{
			ReceivedAt: received, Time: received,
			Event: &signal.Message{Body: "retained", Envelope: signal.Envelope{Timestamp: uint64(index + 1)}},
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func daemonMessagesAfterQueue(t *testing.T, session *daemonSession) []json.RawMessage {
	t.Helper()

	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()

	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()

	for {
		_, body := session.request(t, http.MethodGet, "/v1/health", httpToken, "")

		var health struct {
			Connection struct {
				LastEvent time.Time `json:"lastEvent"`
			} `json:"connection"`
		}

		err := json.Unmarshal([]byte(body), &health)
		if err != nil {
			t.Fatal(err)
		}

		if !health.Connection.LastEvent.IsZero() {
			break
		}

		select {
		case <-ticker.C:
		case <-deadline.C:
			t.Fatal("queue marker never received")
		}
	}

	status, body := session.request(t, http.MethodGet, "/v1/messages?cursor=0", httpToken, "")

	var page struct {
		Messages []json.RawMessage `json:"messages"`
	}

	err := json.Unmarshal([]byte(body), &page)
	if err != nil || status != http.StatusOK {
		t.Fatalf("messages: HTTP %d %s, %v", status, body, err)
	}

	return page.Messages
}

func TestDaemonPolicyRetentionPrecedence(t *testing.T) {
	for _, test := range []struct {
		name string
		want int
	}{
		{name: "defaults", want: 2},
		{name: daemonConfigCase, want: 1},
		{name: daemonEnvCase, want: 2},
		{name: "flags", want: 3},
	} {
		t.Run(test.name, func(t *testing.T) {
			addr := freeAddr(t)
			config := "daemon:\n  listen: " + addr + "\n  token: " + httpToken + "\n"

			var args []string

			switch test.name {
			case "defaults":
				args = []string{"--allow-recipient=" + aliceNumber}
			case daemonConfigCase:
				config += "  read-only: false\n  allow-recipient: [\"" + aliceNumber + "\"]\n" +
					"  inbox-max-age: 0\n  inbox-max-count: 1\n"
			case daemonEnvCase:
				config += "  read-only: true\n  allow-recipient: [\"" + bobACI + "\"]\n  inbox-max-age: 1ns\n  inbox-max-count: 1\n"

				t.Setenv("GOSIGNAL_DAEMON_READ_ONLY", "false")
				t.Setenv("GOSIGNAL_DAEMON_ALLOW_RECIPIENT", aliceNumber+",self")
				t.Setenv("GOSIGNAL_DAEMON_INBOX_MAX_AGE", "0")
				t.Setenv("GOSIGNAL_DAEMON_INBOX_MAX_COUNT", "2")
			case "flags":
				config += "  read-only: true\n  allow-recipient: [\"" + bobACI + "\"]\n  inbox-max-age: 1ns\n  inbox-max-count: 1\n"

				t.Setenv("GOSIGNAL_DAEMON_READ_ONLY", "true")
				t.Setenv("GOSIGNAL_DAEMON_ALLOW_RECIPIENT", daemonInvalidChat)
				t.Setenv("GOSIGNAL_DAEMON_INBOX_MAX_AGE", "-1h")
				t.Setenv("GOSIGNAL_DAEMON_INBOX_MAX_COUNT", "-1")

				args = []string{"--read-only=false", "--allow-recipient=" + aliceNumber, "--inbox-max-age=0", "--inbox-max-count=0"}
			}

			fake := sendFake()
			fake.Incoming = []signal.Event{&signal.QueueEmpty{}}
			seedDaemonInbox(t, fake)
			session := startDaemon(t, fake, config, addr, args...)
			session.ready(t)

			if messages := daemonMessagesAfterQueue(t, session); len(messages) != test.want {
				t.Errorf("retained %d messages, want %d", len(messages), test.want)
			}

			status, body := session.request(t, http.MethodPost, "/v1/messages", httpToken,
				`{"recipients":["`+aliceNumber+`"],"text":"configured"}`)
			if status != http.StatusOK || !strings.Contains(body, `"ok":true`) {
				t.Errorf("policy precedence: HTTP %d %s", status, body)
			}
		})
	}
}
