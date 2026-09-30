package cmd_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/cmd"
	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/go-signal/internal/signal/signaltest"
)

type daemonSession struct {
	url    string
	client *http.Client
	wait   func() error
	stop   func()
}

func startDaemon(t *testing.T, fake *signaltest.Fake, config, addr string, args ...string) *daemonSession {
	t.Helper()

	ctx, cancel := context.WithCancel(t.Context())

	var stdout bytes.Buffer

	root := cmd.NewRootCmd(cmd.WithClientFactory(fake.Factory))
	root.SetOut(&stdout)
	root.SetArgs(append([]string{
		"--config=" + daemonConfigFile(t, config), dataDirFlag, t.TempDir(), daemonCmd, serveCmd,
	}, args...))

	done := make(chan error, 1)

	go func() { done <- root.ExecuteContext(ctx) }()

	transport, _ := http.DefaultTransport.(*http.Transport)
	transport = transport.Clone()

	var (
		once sync.Once
		err  error
	)

	wait := func() error {
		once.Do(func() {
			select {
			case err = <-done:
			case <-time.After(10 * time.Second):
				t.Error("daemon serve did not stop")
			}

			if !fake.AllClosed() {
				t.Error("daemon did not close its client")
			}

			if stdout.Len() != 0 {
				t.Errorf("daemon wrote stdout: %q", stdout.String())
			}
		})

		return err
	}
	stop := func() {
		transport.CloseIdleConnections()
		cancel()
	}
	session := &daemonSession{
		url: "http://" + addr, client: &http.Client{Transport: transport, Timeout: 5 * time.Second}, wait: wait, stop: stop,
	}

	t.Cleanup(func() {
		stop()

		_ = wait()
	})

	return session
}

func (s *daemonSession) request(t *testing.T, method, path, token, body string) (int, string) {
	t.Helper()

	request, err := http.NewRequestWithContext(t.Context(), method, s.url+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}

	request.Header.Set("Authorization", "Bearer "+token)

	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}

	response, err := s.client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()

	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}

	return response.StatusCode, string(data)
}

func (s *daemonSession) ready(t *testing.T) string {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, s.url+"/v1/health", nil)
	if err != nil {
		t.Fatal(err)
	}

	request.Header.Set("Authorization", "Bearer "+httpToken)

	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	for {
		response, err := s.client.Do(request)
		if err == nil {
			data, readErr := io.ReadAll(response.Body)

			_ = response.Body.Close()

			if readErr != nil || response.StatusCode != http.StatusOK {
				t.Fatalf("health: HTTP %d %s, %v", response.StatusCode, data, readErr)
			}

			return string(data)
		}

		select {
		case <-ctx.Done():
			t.Fatalf("daemon never became ready: %v", err)
		case <-ticker.C:
		}
	}
}

func TestDaemonHTTPAccountAndLock(t *testing.T) {
	t.Parallel()

	first := *testAccount()
	second := first
	second.ACI, second.Number = bobACI, "+15550202"
	fake := &signaltest.Fake{Linked: []signal.Account{first, second}}
	addr := freeAddr(t)
	session := startDaemon(t, fake, "daemon:\n  token: "+httpToken+"\n", addr,
		"--listen="+addr, "--account="+second.Number)

	health := session.ready(t)
	if !strings.Contains(health, second.ACI) {
		t.Errorf("health selected wrong account: %s", health)
	}

	if got := fake.Connects(); len(got) != 1 || got[0] != second.ACI {
		t.Errorf("connected %v, want selected account once", got)
	}

	if status, _ := session.request(t, http.MethodGet, "/v1/health", "wrong", ""); status != http.StatusUnauthorized {
		t.Errorf("unauthenticated health: HTTP %d", status)
	}

	other, err := fake.Factory(t.Context(), signal.Options{Account: second.ACI})
	if err != nil {
		t.Fatal(err)
	}

	err = other.Connect(t.Context())
	if !errors.Is(err, signal.ErrAccountInUse) {
		t.Errorf("concurrent connect: %v, want account lock", err)
	}

	err = other.Close()
	if err != nil {
		t.Fatal(err)
	}
}

func TestDaemonStartupErrors(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name string
		fake *signaltest.Fake
		want error
	}{
		{
			name: "unlinked", fake: &signaltest.Fake{Linked: []signal.Account{unlinkedAccount()}},
			want: signal.ErrDeviceUnlinked,
		},
		{
			name: "lock held", fake: &signaltest.Fake{Linked: []signal.Account{*testAccount()}, InUse: true},
			want: signal.ErrAccountInUse,
		},
		{
			name: "connect failed", fake: &signaltest.Fake{Linked: []signal.Account{*testAccount()}, ConnectErr: errUnreachable},
			want: errUnreachable,
		},
		{name: "no account", fake: &signaltest.Fake{}, want: signal.ErrNotLinked},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			addr := freeAddr(t)
			session := startDaemon(t, test.fake, "daemon:\n  token: "+httpToken+"\n", addr, "--listen="+addr)

			err := session.wait()
			if !errors.Is(err, test.want) {
				t.Errorf("startup: %v, want %v", err, test.want)
			}
		})
	}
}

func TestDaemonUnlinkWhileRunning(t *testing.T) {
	t.Parallel()

	fake := &signaltest.Fake{Linked: []signal.Account{*testAccount()}}
	addr := freeAddr(t)
	session := startDaemon(t, fake, "daemon:\n  token: "+httpToken+"\n", addr, "--listen="+addr)
	session.ready(t)

	if !fake.Push(&signal.Connection{State: signal.StateLoggedOut}) {
		t.Fatal("could not inject remote unlink")
	}

	err := session.wait()
	if !errors.Is(err, signal.ErrDeviceUnlinked) || cmd.ExitCode(err) != cmd.ExitUnlinked {
		t.Errorf("remote unlink: %v, exit %d; want unlink and exit 3", err, cmd.ExitCode(err))
	}
}

func TestDaemonCancellationReleasesLock(t *testing.T) {
	t.Parallel()

	fake := &signaltest.Fake{Linked: []signal.Account{*testAccount()}}
	addr := freeAddr(t)
	session := startDaemon(t, fake, "daemon:\n  token: "+httpToken+"\n", addr, "--listen="+addr)
	session.ready(t)

	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, session.url+"/v1/events?cursor=0", nil)
	if err != nil {
		t.Fatal(err)
	}

	request.Header.Set("Authorization", "Bearer "+httpToken)

	stream, err := session.client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Body.Close()

	line, err := bufio.NewReader(stream.Body).ReadString('\n')
	if err != nil || line != "event: ready\n" {
		t.Fatalf("SSE ready: %q, %v", line, err)
	}

	session.stop()

	err = session.wait()
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}

	client, err := fake.Factory(t.Context(), signal.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	err = client.Connect(t.Context())
	if err != nil {
		t.Errorf("account lock was not released: %v", err)
	}
}

func TestDaemonReceivesWithoutReadReceipts(t *testing.T) {
	t.Parallel()

	fake := sendFake()
	fake.Incoming = []signal.Event{
		&signal.Message{Envelope: signal.Envelope{
			Sender: signal.Recipient{ACI: aliceACI, Number: aliceNumber},
			Chat:   signal.Chat{Recipient: signal.Recipient{ACI: aliceACI}}, Timestamp: sentAt,
		}, Body: "hello inbox"},
		&signal.QueueEmpty{},
	}
	addr := freeAddr(t)
	session := startDaemon(t, fake, "daemon:\n  token: "+httpToken+"\n", addr, "--listen="+addr)
	session.ready(t)

	messages := daemonMessagesAfterQueue(t, session)
	if len(messages) != 1 || !strings.Contains(string(messages[0]), "hello inbox") {
		t.Errorf("inbox response: %s", messages)
	}

	if receipts := fake.Receipts(); len(receipts) != 0 {
		t.Errorf("listing sent read receipts: %+v", receipts)
	}

	status, body := session.request(t, http.MethodPost, "/v1/mark-read", httpToken, "{}")
	if status != http.StatusOK || !strings.Contains(body, `"messages":1`) || !strings.Contains(body, `"senders":1`) {
		t.Errorf("explicit mark-read with empty send allowlist: HTTP %d %s", status, body)
	}

	if receipts := fake.Receipts(); len(receipts) != 1 || receipts[0].Type != signal.ReceiptRead {
		t.Errorf("explicit mark-read receipts: %+v", receipts)
	}
}

func TestDaemonWritePolicy(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name string
		args []string
		want int
	}{
		{name: "default denies", want: http.StatusForbidden},
		{name: "allowlisted", args: []string{"--allow-recipient=" + aliceNumber}, want: http.StatusOK},
		{name: "wildcard", args: []string{"--allow-recipient=*"}, want: http.StatusOK},
		{name: "read only", args: []string{"--allow-recipient=*", "--read-only"}, want: http.StatusForbidden},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			fake := sendFake()
			addr := freeAddr(t)
			session := startDaemon(t, fake, "daemon:\n  token: "+httpToken+"\n", addr,
				append([]string{"--listen=" + addr}, test.args...)...)
			session.ready(t)

			status, body := session.request(t, http.MethodPost, "/v1/messages", httpToken,
				`{"recipients":["`+aliceNumber+`"],"text":"hello bot"}`)
			if status != test.want {
				t.Errorf("send: HTTP %d %s, want %d", status, body, test.want)
			}

			assertDaemonSend(t, fake, body, test.want)

			wantMarkRead := http.StatusOK
			if test.name == "read only" {
				wantMarkRead = http.StatusForbidden
			}

			if status, body := session.request(t, http.MethodPost, "/v1/mark-read", httpToken, "{}"); status != wantMarkRead {
				t.Errorf("mark-read: HTTP %d %s, want %d", status, body, wantMarkRead)
			}
		})
	}
}

func assertDaemonSend(t *testing.T, fake *signaltest.Fake, body string, status int) {
	t.Helper()

	if status != http.StatusOK {
		if sent := fake.Sent(); len(sent) != 0 {
			t.Errorf("policy rejected send but sent %+v", sent)
		}

		return
	}

	var result struct {
		OK bool `json:"ok"`
	}

	err := json.Unmarshal([]byte(body), &result)
	if err != nil || !result.OK {
		t.Errorf("send response: %s, %v", body, err)
	}

	if sent := fake.Sent(); len(sent) != 1 || sent[0].Body != "hello bot" {
		t.Errorf("sent %+v, want one text send", sent)
	}
}
