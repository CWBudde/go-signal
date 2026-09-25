package cmd_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/cmd"
	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/go-signal/internal/signal/signaltest"
)

const (
	dataDirFlag = "--data-dir"
	sendCmd     = "send"
	sentAt      = 1790000000000
	aliceNumber = "+15550101"
	carolACI    = "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
)

var errUnreachable = errors.New("recipient unreachable")

// sendFake returns a fake with alice (by number) and bob (by username) on Signal and a group of
// us, alice and carol.
func sendFake() *signaltest.Fake {
	return &signaltest.Fake{
		Linked: []signal.Account{*testAccount()},
		Directory: []signal.Recipient{
			{ACI: aliceACI, Number: aliceNumber},
			{ACI: bobACI, Username: "bob.42"},
		},
		Groups: map[string][]signal.Recipient{
			groupID: {{ACI: testAccount().ACI}, {ACI: aliceACI}, {ACI: carolACI}},
		},
	}
}

// runSend is run with a fixed clock and stdin.
func runSend(t *testing.T, fake *signaltest.Fake, stdin string, args ...string) (string, error) {
	t.Helper()

	cfgFile := filepath.Join(t.TempDir(), "config.yaml")

	err := os.WriteFile(cfgFile, nil, 0o600)
	if err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer

	root := cmd.NewRootCmd(
		cmd.WithClientFactory(fake.Factory),
		cmd.WithLocation(time.UTC),
		cmd.WithClock(func() time.Time { return time.UnixMilli(sentAt) }),
	)
	root.SetOut(&out)
	root.SetIn(strings.NewReader(stdin))
	root.SetArgs(append([]string{"--config", cfgFile, dataDirFlag, t.TempDir()}, args...))

	err = root.ExecuteContext(t.Context())

	if !fake.AllClosed() {
		t.Error("client was not closed")
	}

	return out.String(), err
}

func TestSend(t *testing.T) {
	t.Parallel()

	args := []string{sendCmd, aliceNumber, "@bob.42", app.SelfRecipient, "-m", "hello", "--group", groupID}

	for _, format := range []string{"plain", "json"} {
		t.Run(format, func(t *testing.T) {
			t.Parallel()

			fake := sendFake()

			out, err := runSend(t, fake, "", append([]string{"-o", format}, args...)...)
			if err != nil {
				t.Fatalf("send: %v", err)
			}

			golden(t, "send_"+format, out)

			sent := fake.Sent()
			if len(sent) != 2 || sent[0].Body != "hello" || sent[1].GroupID != groupID {
				t.Errorf("sent %+v", sent)
			}
		})
	}
}

func TestSendPartialFailure(t *testing.T) {
	t.Parallel()

	for _, format := range []string{"plain", "json"} {
		t.Run(format, func(t *testing.T) {
			t.Parallel()

			fake := sendFake()
			fake.SendFailures = map[string]error{bobACI: errUnreachable, carolACI: errUnreachable}

			unknown := app.GroupPrefix + strings.Repeat("A", 43) + "="

			out, err := runSend(t, fake, "", "-o", format, sendCmd, "-m", "hello",
				aliceNumber, "@bob.42", app.GroupPrefix+groupID, unknown)
			if !errors.Is(err, app.ErrSendFailed) {
				t.Fatalf("got %v, want ErrSendFailed", err)
			}

			if code := cmd.ExitCode(err); code != cmd.ExitFailure {
				t.Errorf("exit code %d, want %d", code, cmd.ExitFailure)
			}

			golden(t, "send_failed_"+format, out)
		})
	}
}

func TestSendStdin(t *testing.T) {
	t.Parallel()

	fake := sendFake()

	_, err := runSend(t, fake, "line one\nline two\n", sendCmd, "--stdin", app.SelfRecipient)
	if err != nil {
		t.Fatalf("send: %v", err)
	}

	sent := fake.Sent()
	if len(sent) != 1 || sent[0].Body != "line one\nline two" {
		t.Errorf("sent %+v, want the body from stdin without the final newline", sent)
	}
}

func TestSendRichContent(t *testing.T) {
	t.Parallel()

	fake := sendFake()
	file := filepath.Join(t.TempDir(), "report.pdf")

	err := os.WriteFile(file, []byte("%PDF-1.7\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	_, err = runSend(t, fake, "", sendCmd, aliceNumber, "--attach", file,
		"--quote", aliceNumber+":1789999999000", "--quote-text", "the report?", "-m", "here, @{@bob.42}")
	if err != nil {
		t.Fatalf("send: %v", err)
	}

	sent := fake.Sent()
	if len(sent) != 1 {
		t.Fatalf("sent %+v", sent)
	}

	req := sent[0]
	if req.Body != "here, \uFFFC" || len(req.Mentions) != 1 || req.Mentions[0].Recipient.ACI != bobACI {
		t.Errorf("body %q, mentions %+v", req.Body, req.Mentions)
	}

	if req.Quote == nil || req.Quote.Author.ACI != aliceACI || req.Quote.Timestamp != 1789999999000 ||
		req.Quote.Text != "the report?" {
		t.Errorf("quote %+v", req.Quote)
	}

	if len(req.Attachments) != 1 || req.Attachments[0].ContentType != "application/pdf" ||
		req.Attachments[0].Filename != "report.pdf" {
		t.Errorf("attachments %+v", req.Attachments)
	}

	// An attachment needs no text.
	fake = sendFake()

	_, err = runSend(t, fake, "", sendCmd, app.SelfRecipient, "--attach", file)
	if err != nil || len(fake.Sent()) != 1 || fake.Sent()[0].Body != "" {
		t.Errorf("attachment only: sent %+v, %v", fake.Sent(), err)
	}
}

func TestSendUnlinked(t *testing.T) {
	t.Parallel()

	fake := sendFake()
	fake.Incoming = []signal.Event{&signal.Connection{State: signal.StateLoggedOut}}

	_, err := runSend(t, fake, "", sendCmd, "-m", "hello", app.SelfRecipient)
	if code := cmd.ExitCode(err); code != cmd.ExitUnlinked {
		t.Fatalf("got %v (exit code %d), want exit code %d", err, code, cmd.ExitUnlinked)
	}
}

func TestSendUsage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
		want error
	}{
		{"no body", []string{sendCmd, app.SelfRecipient}, nil},
		{"message and stdin", []string{sendCmd, app.SelfRecipient, "-m", "hi", "--stdin"}, nil},
		{"empty message", []string{sendCmd, app.SelfRecipient, "-m", ""}, app.ErrEmptyMessage},
		{"no recipient", []string{sendCmd, "-m", "hi"}, app.ErrNoRecipients},
		{"invalid recipient", []string{sendCmd, "alice", "-m", "hi"}, app.ErrInvalidRecipient},
		{"not on Signal", []string{sendCmd, "+15550199", "-m", "hi"}, signal.ErrNotOnSignal},
		{"invalid quote", []string{sendCmd, app.SelfRecipient, "-m", "hi", "--quote", "self"}, app.ErrInvalidQuote},
		{"missing attachment", []string{sendCmd, app.SelfRecipient, "--attach", "/nonexistent/file"}, os.ErrNotExist},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			fake := sendFake()

			out, err := runSend(t, fake, "", test.args...)
			if err == nil || (test.want != nil && !errors.Is(err, test.want)) {
				t.Fatalf("got %v, want %v", err, test.want)
			}

			if out != "" || len(fake.Sent()) != 0 {
				t.Errorf("printed %q and sent %+v, want nothing", out, fake.Sent())
			}
		})
	}
}
