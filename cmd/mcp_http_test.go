package cmd_test

import (
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/go-signal/internal/signal/signaltest"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const httpToken = "0123456789abcdef0123456789abcdef"

// bearer adds a bearer token to every request.
type bearer struct {
	token string
}

func (b bearer) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Header.Set("Authorization", "Bearer "+b.token)

	return http.DefaultTransport.RoundTrip(req) //nolint:wrapcheck // a transport passes through
}

// freeAddr returns a loopback address with a port that was free a moment ago.
func freeAddr(t *testing.T) string {
	t.Helper()

	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	addr := listener.Addr().String()

	err = listener.Close()
	if err != nil {
		t.Fatal(err)
	}

	return addr
}

func TestMCPServeListenErrors(t *testing.T) {
	t.Parallel()

	short := filepath.Join(t.TempDir(), "token")

	err := os.WriteFile(short, []byte("short\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	for args, want := range map[string]string{
		"--listen=0.0.0.0:8765":                                  "loopback",
		"--listen=example.com:8765":                              "loopback",
		"--listen=8765":                                          "--listen",
		"--listen=127.0.0.1:8765":                                "bearer token",
		"--listen=127.0.0.1:8765 --token-file=" + short:          "at least 16",
		"--listen=[::1]:8765 --token-file=" + short + "-missing": "--token-file",
	} {
		session, wait := startMCP(t, t.Context(), &signaltest.Fake{Linked: []signal.Account{*testAccount()}},
			strings.Fields(args)...)

		err := wait()
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v, want an error about %s", args, err, want)
		}

		if _, ok := session.next(); ok {
			t.Errorf("%s: wrote to stdout", args)
		}
	}
}

func TestMCPServeHTTP(t *testing.T) {
	t.Parallel()

	addr := freeAddr(t)
	tokenFile := filepath.Join(t.TempDir(), "token")

	err := os.WriteFile(tokenFile, []byte(httpToken+"\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	fake := &signaltest.Fake{Linked: []signal.Account{*testAccount()}}
	session, wait := startMCP(t, ctx, fake, "--listen="+addr, "--token-file="+tokenFile, "--read-only")

	client := sdk.NewClient(&sdk.Implementation{Name: "test", Version: "0"}, nil)
	transport := &sdk.StreamableClientTransport{
		Endpoint: "http://" + addr + "/mcp", HTTPClient: &http.Client{Transport: bearer{httpToken}},
	}

	var mcpSession *sdk.ClientSession

	// The server listens once it has connected the account.
	for deadline := time.Now().Add(5 * time.Second); ; {
		mcpSession, err = client.Connect(ctx, transport, nil)
		if err == nil || time.Now().After(deadline) {
			break
		}

		time.Sleep(20 * time.Millisecond)
	}

	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	tools, err := mcpSession.ListTools(ctx, nil)
	if err != nil || len(tools.Tools) == 0 {
		t.Errorf("list tools: %v, %d tools", err, len(tools.Tools))
	}

	_ = mcpSession.Close()

	// stdout stays unused, and SIGINT (a cancelled context) ends the server normally.
	cancel()

	err = wait()
	if err != nil {
		t.Fatalf("mcp serve: %v", err)
	}

	if _, ok := session.next(); ok {
		t.Error("wrote to stdout")
	}
}
