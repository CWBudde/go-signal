package daemon_test

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/daemon"
	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/go-signal/internal/signal/signaltest"
)

const (
	testToken = "0123456789abcdef0123456789abcdef"
	ownACI    = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	aliceACI  = "11111111-1111-4111-8111-111111111111"
	bobACI    = "22222222-2222-4222-8222-222222222222"
)

const (
	authorizationHeader = "Authorization"
	contentTypeHeader   = "Content-Type"
	messagesPath        = "/v1/messages"
	markReadPath        = "/v1/mark-read"
	nullBody            = "null"
)

const (
	unauthorizedCode     = "unauthorized"
	invalidRequestCode   = "invalid_request"
	unsupportedMediaCode = "unsupported_media_type"
	selfSendBody         = `{"recipients":["self"],"text":"hi"}`
)

type testResponse struct{ *http.Response }

var (
	errNetworkProbe  = errors.New("network probe must not run")
	errTemporaryLoss = errors.New("temporary loss")
	errDiskFull      = errors.New("disk full")
	errDelivery      = errors.New("delivery failed")
	errReceipt       = errors.New("receipt failed")
	errDatabase      = errors.New("database unreadable")
	errLaterDatabase = errors.New("database became unreadable")
)

func testFake() *signaltest.Fake {
	return &signaltest.Fake{Linked: []signal.Account{{ACI: ownACI, Number: "+49123456789"}}}
}

type runningServer struct {
	url    string
	fake   *signaltest.Fake
	client signal.Client
	cancel context.CancelFunc
	done   <-chan error
	once   sync.Once
	err    error
}

func openClient(t *testing.T, fake *signaltest.Fake) signal.Client {
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

	return client
}

func listenLoopback(t *testing.T) net.Listener {
	t.Helper()

	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	return listener
}

func startServer(t *testing.T, fake *signaltest.Fake, opts daemon.Options, options ...app.Option) *runningServer {
	t.Helper()
	return serveClient(t, fake, openClient(t, fake), listenLoopback(t), opts, options...)
}

func serveClient(
	t *testing.T, fake *signaltest.Fake, client signal.Client, listener net.Listener,
	opts daemon.Options, options ...app.Option,
) *runningServer {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())

	done := make(chan error, 1)
	go func() {
		done <- daemon.ServeHTTP(ctx, app.New(client, options...), client.Events(), opts, listener, testToken)
	}()

	server := &runningServer{
		url: "http://" + listener.Addr().String(), fake: fake, client: client, cancel: cancel, done: done,
	}

	t.Cleanup(func() {
		err := server.stop(t)
		if err != nil {
			t.Errorf("ServeHTTP: %v", err)
		}
	})

	return server
}

func (s *runningServer) stop(t *testing.T) error {
	t.Helper()
	s.once.Do(func() {
		s.cancel()

		select {
		case s.err = <-s.done:
		case <-time.After(8 * time.Second):
			t.Error("server did not stop")
		}
	})

	return s.err
}

func (s *runningServer) request(t *testing.T, method, path, body string, headers map[string]string) *testResponse {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	t.Cleanup(cancel)

	req, err := http.NewRequestWithContext(ctx, method, s.url+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}

	req.Header.Set(authorizationHeader, "Bearer "+testToken)

	if body != "" {
		req.Header.Set(contentTypeHeader, "application/json")
	}

	for key, value := range headers {
		req.Header.Set(key, value)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = resp.Body.Close() })

	return &testResponse{Response: resp}
}

func decodeResponse(t *testing.T, resp *testResponse, out any) {
	t.Helper()

	err := json.NewDecoder(resp.Body).Decode(out)
	if err != nil {
		t.Fatal(err)
	}
}

func assertError(t *testing.T, resp *testResponse, status int, code string) {
	t.Helper()

	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	decodeResponse(t, resp, &body)

	if resp.StatusCode != status || body.Error.Code != code || body.Error.Message == "" {
		t.Errorf("status %d error %+v, want %d %s", resp.StatusCode, body.Error, status, code)
	}

	if !strings.HasPrefix(resp.Header.Get(contentTypeHeader), "application/json") {
		t.Errorf("content type %q", resp.Header.Get(contentTypeHeader))
	}
}

func TestAuthenticationAndRouting(t *testing.T) {
	t.Parallel()
	server := startServer(t, testFake(), daemon.Options{})

	tests := []struct {
		name, method, path string
		headers            map[string]string
		status             int
		code               string
	}{
		{"missing token", http.MethodGet, "/v1/health", map[string]string{authorizationHeader: ""}, 401, unauthorizedCode},
		{
			"query token", http.MethodGet, "/v1/health?token=" + testToken,
			map[string]string{authorizationHeader: ""},
			401, unauthorizedCode,
		},
		{
			"wrong token", http.MethodPost, "/missing",
			map[string]string{authorizationHeader: "Bearer wrong"},
			401, unauthorizedCode,
		},
		{"unknown route", http.MethodGet, "/missing", nil, 404, "not_found"},
		{"wrong method", http.MethodDelete, messagesPath, nil, 405, "method_not_allowed"},
		{
			"cross site", http.MethodPost, messagesPath,
			map[string]string{"Origin": "https://example.org", "Sec-Fetch-Site": "cross-site"},
			403, "forbidden",
		},
		{"cross origin", http.MethodPost, messagesPath, map[string]string{"Origin": "https://example.org"}, 403, "forbidden"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			response := server.request(t, testCase.method, testCase.path, `{}`, testCase.headers)
			assertError(t, response, testCase.status, testCase.code)
		})
	}

	t.Cleanup(func() {
		if len(server.fake.Sent()) != 0 || len(server.fake.Receipts()) != 0 {
			t.Error("rejected requests performed writes")
		}
	})
}

func TestHealthDoesNotProbe(t *testing.T) {
	t.Parallel()

	fake := testFake()
	fake.DevicesErr = errNetworkProbe
	server := startServer(t, fake, daemon.Options{Version: "test-version"})

	var body struct {
		Version, Account string
		Connection       struct {
			State            string
			Since, LastEvent time.Time
			Error            string
		}
	}

	resp := server.request(t, http.MethodGet, "/v1/health", "", nil)
	decodeResponse(t, resp, &body)

	if body.Version != "test-version" || body.Account != ownACI {
		t.Fatalf("health %+v status %d", body, resp.StatusCode)
	}

	if !fake.Push(&signal.Connection{State: signal.StateError, Err: errTemporaryLoss}) {
		t.Fatal("push failed")
	}

	deadline := time.Now().Add(2 * time.Second)

	for {
		decodeResponse(t, server.request(t, http.MethodGet, "/v1/health", "", nil), &body)

		if body.Connection.State == "error" {
			break
		}

		if time.Now().After(deadline) {
			t.Fatal("connection event not observed")
		}
	}

	if body.Connection.Error != "temporary loss" || body.Connection.Since.IsZero() || body.Connection.LastEvent.IsZero() {
		t.Errorf("connection %+v", body.Connection)
	}
}

func TestServeValidationAndReceiverTermination(t *testing.T) {
	t.Parallel()

	for _, token := range []string{"", "short"} {
		err := daemon.ServeHTTP(t.Context(), nil, nil, daemon.Options{}, nil, token)
		if err == nil {
			t.Errorf("accepted token %q", token)
		}
	}

	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}

	err = daemon.ServeHTTP(t.Context(), nil, nil, daemon.Options{}, listener, testToken)
	if err == nil {
		t.Error("accepted wildcard listener")
	}
}

func TestReceiverNormalTermination(t *testing.T) {
	t.Parallel()

	for _, closed := range []bool{false, true} {
		fake := testFake()

		client, openErr := fake.Factory(t.Context(), signal.Options{})
		if openErr != nil {
			t.Fatal(openErr)
		}

		t.Cleanup(func() { _ = client.Close() })

		listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}

		var events chan signal.Event
		if closed {
			events = make(chan signal.Event)
			close(events)
		}

		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		started := time.Now()
		err = daemon.ServeHTTP(ctx, app.New(client), events, daemon.Options{}, listener, testToken)

		cancel()

		if err != nil || time.Since(started) >= time.Second {
			t.Errorf("closed %v: %v, elapsed %v", closed, err, time.Since(started))
		}
	}
}

func TestReceiverFatalErrors(t *testing.T) {
	t.Parallel()

	storageErr := errDiskFull
	for _, testCase := range []struct {
		name     string
		inboxErr error
		event    signal.Event
		want     error
	}{
		{"storage", storageErr, message(aliceACI, 1), storageErr},
		{"storage deadline", context.DeadlineExceeded, message(aliceACI, 1), context.DeadlineExceeded},
		{"unlink", nil, &signal.Connection{State: signal.StateLoggedOut}, signal.ErrDeviceUnlinked},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			fake := testFake()
			fake.InboxErr = testCase.inboxErr

			client, err := fake.Factory(t.Context(), signal.Options{})
			if err != nil {
				t.Fatal(err)
			}

			t.Cleanup(func() { _ = client.Close() })

			listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}

			events := make(chan signal.Event, 1)
			events <- testCase.event

			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
			defer cancel()

			err = daemon.ServeHTTP(ctx, app.New(client), events, daemon.Options{}, listener, testToken)
			if !errors.Is(err, testCase.want) {
				t.Errorf("got %v, want %v", err, testCase.want)
			}
		})
	}
}
