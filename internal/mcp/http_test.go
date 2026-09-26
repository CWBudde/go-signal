package mcp_test

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/mcp"
	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/go-signal/internal/signal/signaltest"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const testToken = "0123456789abcdef0123456789abcdef"

// bearer adds a bearer token to every request.
type bearer struct {
	token string
}

func (b bearer) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Header.Set("Authorization", "Bearer "+b.token)

	return http.DefaultTransport.RoundTrip(req) //nolint:wrapcheck // a transport passes through
}

// serveHTTP runs ServeHTTP on a client of fake and returns the endpoint's URL and a function that
// stops the server and returns its error.
func serveHTTP(t *testing.T, fake *signaltest.Fake) (string, func() error) {
	t.Helper()

	client, err := fake.Factory(t.Context(), signal.Options{})
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = client.Close() })

	err = client.Connect(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	served := make(chan error, 1)

	go func() {
		served <- mcp.ServeHTTP(ctx, app.New(client), client.Events(), mcp.Options{}, listener, testToken)
	}()

	return "http://" + listener.Addr().String() + mcp.HTTPPath, func() error {
		cancel()

		select {
		case err := <-served:
			return err
		case <-time.After(10 * time.Second):
			t.Fatal("ServeHTTP did not end")

			return nil
		}
	}
}

func TestServeHTTP(t *testing.T) {
	t.Parallel()

	url, stop := serveHTTP(t, &signaltest.Fake{Linked: []signal.Account{testAccount()}})

	session, err := sdk.NewClient(&sdk.Implementation{Name: "test", Version: "0"}, nil).Connect(t.Context(),
		&sdk.StreamableClientTransport{Endpoint: url, HTTPClient: &http.Client{Transport: bearer{testToken}}}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	var acc struct {
		ACI string `json:"aci"`
	}

	call(t, session, accountShowTool, map[string]any{}, &acc)

	if acc.ACI != testAccount().ACI {
		t.Errorf("account %+v", acc)
	}

	// An open session doesn't hold up the shutdown.
	err = stop()
	if err != nil {
		t.Errorf("serve: %v", err)
	}

	_ = session.Close()
}

func TestServeHTTPToken(t *testing.T) {
	t.Parallel()

	url, stop := serveHTTP(t, &signaltest.Fake{Linked: []signal.Account{testAccount()}})

	for _, token := range []string{"", "wrong", testToken + "x"} {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, url,
			strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
		if err != nil {
			t.Fatal(err)
		}

		req.Header.Set("Content-Type", "application/json")

		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}

		_ = resp.Body.Close()

		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("token %q: status %d, want 401", token, resp.StatusCode)
		}
	}

	err := stop()
	if err != nil {
		t.Errorf("serve: %v", err)
	}
}

func TestServeHTTPNoToken(t *testing.T) {
	t.Parallel()

	err := mcp.ServeHTTP(t.Context(), nil, nil, mcp.Options{}, nil, "")
	if !errors.Is(err, mcp.ErrNoToken) {
		t.Errorf("got %v, want ErrNoToken", err)
	}
}
