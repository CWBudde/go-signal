//go:build cgo && !purego

package signal_test

import (
	"encoding/base64"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/go-signal/internal/store"
)

// The tests in this file replace signalmeow's global HTTP transport, so they don't run in
// parallel (parallel tests only start once they are done).

var errNoNetwork = errors.New("no network")

// handlerTransport answers requests with an http.Handler instead of the network.
type handlerTransport struct {
	handler http.Handler
	calls   atomic.Int32
}

func (h *handlerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	h.calls.Add(1)

	rec := httptest.NewRecorder()
	h.handler.ServeHTTP(rec, req)

	return rec.Result(), nil
}

type failingTransport struct{}

func (failingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errNoNetwork
}

// fakeServer routes signalmeow's REST requests to handler for the rest of the test.
func fakeServer(t *testing.T, handler http.HandlerFunc) *handlerTransport {
	t.Helper()

	transport := &handlerTransport{handler: handler}
	t.Cleanup(signal.SetSignalTransport(transport))

	return transport
}

// status answers every request with code, after checking method, path and our credentials.
func status(t *testing.T, method, path string, code int, body string) http.HandlerFunc {
	t.Helper()

	return func(resp http.ResponseWriter, req *http.Request) {
		if req.Method != method || req.URL.Path != path {
			t.Errorf("request %s %s, want %s %s", req.Method, req.URL.Path, method, path)
		}

		if user, _, ok := req.BasicAuth(); !ok || !strings.HasPrefix(user, seededACI) {
			t.Errorf("request without our credentials (user %q)", user)
		}

		resp.WriteHeader(code)
		_, _ = resp.Write([]byte(body))
	}
}

func openSeededClient(t *testing.T, dataDir string) signal.Client {
	t.Helper()

	client, err := signal.Open(t.Context(), signal.Options{
		DataDir: dataDir, Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	t.Cleanup(func() { _ = client.Close() })

	return client
}

func unlinkedAt(t *testing.T, dataDir string) time.Time {
	t.Helper()

	dir, err := store.OpenDir(dataDir, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}

	accounts, err := dir.Accounts()
	if err != nil || len(accounts) != 1 {
		t.Fatalf("accounts = %+v, %v", accounts, err)
	}

	return accounts[0].UnlinkedAt
}

func TestDevicesFromServer(t *testing.T) { //nolint:paralleltest // replaces the HTTP transport
	lastSeen := time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)
	body := `{"devices":[{"id":1,"lastSeen":` + itoa(lastSeen.UnixMilli()) + `},{"id":2}]}`
	fakeServer(t, status(t, http.MethodGet, "/v1/devices/", http.StatusOK, body))

	devices, err := openSeededClient(t, seedAccount(t)).Devices(t.Context())
	if err != nil {
		t.Fatalf("devices: %v", err)
	}

	if len(devices) != 2 || devices[0].ID != 1 || !devices[0].LastSeen.Equal(lastSeen) ||
		devices[0].Current || !devices[1].Current {
		t.Errorf("devices = %+v", devices)
	}
}

func TestDevicesUnlinkedByServer(t *testing.T) { //nolint:paralleltest // replaces the HTTP transport
	for _, code := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		fakeServer(t, status(t, http.MethodGet, "/v1/devices/", code, ""))

		dataDir := seedAccount(t)

		_, err := openSeededClient(t, dataDir).Devices(t.Context())
		if !errors.Is(err, signal.ErrDeviceUnlinked) {
			t.Errorf("HTTP %d: %v, want ErrDeviceUnlinked", code, err)
		}

		if unlinkedAt(t, dataDir).IsZero() {
			t.Errorf("HTTP %d: account not marked as unlinked", code)
		}
	}
}

func TestDevicesServerError(t *testing.T) { //nolint:paralleltest // replaces the HTTP transport
	fakeServer(t, status(t, http.MethodGet, "/v1/devices/", http.StatusInternalServerError, ""))

	dataDir := seedAccount(t)

	_, err := openSeededClient(t, dataDir).Devices(t.Context())
	if err == nil || !strings.Contains(err.Error(), "HTTP 500") || errors.Is(err, signal.ErrDeviceUnlinked) {
		t.Errorf("got %v, want an HTTP 500 error", err)
	}

	if !unlinkedAt(t, dataDir).IsZero() {
		t.Error("a server error marked the account as unlinked")
	}

	t.Cleanup(signal.SetSignalTransport(failingTransport{}))

	_, err = openSeededClient(t, seedAccount(t)).Devices(t.Context())
	if !errors.Is(err, errNoNetwork) {
		t.Errorf("without network: %v, want the transport's error", err)
	}
}

func TestDevicesOfUnlinkedAccount(t *testing.T) { //nolint:paralleltest // replaces the HTTP transport
	server := fakeServer(t, status(t, http.MethodGet, "/v1/devices/", http.StatusOK, `{}`))
	dataDir := seedAccounts(t, signal.Account{
		Number: seededNumber, ACI: seededACI, DeviceID: 2, UnlinkedAt: time.Now().UTC(),
	})

	_, err := openSeededClient(t, dataDir).Devices(t.Context())
	if !errors.Is(err, signal.ErrDeviceUnlinked) {
		t.Errorf("got %v, want ErrDeviceUnlinked", err)
	}

	if server.calls.Load() != 0 {
		t.Error("asked the server about an account known to be unlinked")
	}
}

func TestUnlinkRemovesDeviceOnServer(t *testing.T) { //nolint:paralleltest // replaces the HTTP transport
	for _, code := range []int{http.StatusNoContent, http.StatusForbidden} { // 403: already removed
		fakeServer(t, status(t, http.MethodDelete, "/v1/devices/2", code, ""))

		dataDir := seedAccount(t)

		acc, err := openSeededClient(t, dataDir).Unlink(t.Context(), signal.UnlinkOptions{})
		if err != nil || acc.ACI != seededACI {
			t.Fatalf("HTTP %d: unlink = %+v, %v", code, acc, err)
		}

		_, err = os.Stat(filepath.Join(dataDir, seededACI))
		if !errors.Is(err, os.ErrNotExist) {
			t.Errorf("HTTP %d: account dir still exists: %v", code, err)
		}
	}
}

func TestUnlinkServerError(t *testing.T) { //nolint:paralleltest // replaces the HTTP transport
	fakeServer(t, status(t, http.MethodDelete, "/v1/devices/2", http.StatusInternalServerError, ""))

	dataDir := seedAccount(t)

	_, err := openSeededClient(t, dataDir).Unlink(t.Context(), signal.UnlinkOptions{})
	if err == nil || !strings.Contains(err.Error(), "--local-only") {
		t.Errorf("got %v, want a hint at --local-only", err)
	}

	_, err = os.Stat(filepath.Join(dataDir, seededACI))
	if err != nil {
		t.Errorf("account data deleted although the server kept the device: %v", err)
	}
}

func TestResolveUsername(t *testing.T) { //nolint:paralleltest // replaces the HTTP transport
	const username = "alice.42"

	hash, err := signal.UsernameHash(username)
	if err != nil {
		t.Fatal(err)
	}

	path := "/v1/accounts/username_hash/" + base64.RawURLEncoding.EncodeToString(hash)

	fakeServer(t, func(resp http.ResponseWriter, req *http.Request) {
		if req.URL.Path != path {
			resp.WriteHeader(http.StatusNotFound)

			return
		}

		_, _ = resp.Write([]byte(`{"uuid":"` + aliceUser + `"}`))
	})

	client := openSeededClient(t, seedAccount(t))

	got, err := client.Resolve(t.Context(), []signal.Recipient{{Username: username}})
	if err != nil || len(got) != 1 || got[0].ACI != aliceUser || got[0].Username != username {
		t.Errorf("Resolve(%s) = %+v, %v", username, got, err)
	}

	_, err = client.Resolve(t.Context(), []signal.Recipient{{Username: "bob.42"}})
	if !errors.Is(err, signal.ErrNotOnSignal) {
		t.Errorf("unknown username: %v, want ErrNotOnSignal", err)
	}

	_, err = client.Resolve(t.Context(), []signal.Recipient{{Username: "no discriminator"}})
	if !errors.Is(err, signal.ErrInvalidUsername) {
		t.Errorf("invalid username: %v, want ErrInvalidUsername", err)
	}

	t.Cleanup(signal.SetSignalTransport(failingTransport{}))

	_, err = client.Resolve(t.Context(), []signal.Recipient{{Username: username}})
	if !errors.Is(err, errNoNetwork) {
		t.Errorf("without network: %v, want the transport's error", err)
	}
}

func itoa(n int64) string {
	return strconv.FormatInt(n, 10)
}
