package daemon_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/internal/daemon"
)

type eventFrame struct{ name, id, data string }

func openEvents(
	t *testing.T, server *runningServer, path string, headers map[string]string,
) (*bufio.Scanner, func() error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	t.Cleanup(cancel)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.url+path, nil)
	if err != nil {
		t.Fatal(err)
	}

	req.Header.Set(authorizationHeader, "Bearer "+testToken)

	for key, value := range headers {
		req.Header.Set(key, value)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = resp.Body.Close() })

	if resp.StatusCode != http.StatusOK || resp.Header.Get(contentTypeHeader) != "text/event-stream" ||
		resp.Header.Get("Cache-Control") != "no-cache" {
		t.Fatalf("SSE status %d headers %v", resp.StatusCode, resp.Header)
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 4096), 2<<20)

	return scanner, resp.Body.Close
}

func nextFrame(t *testing.T, scanner *bufio.Scanner) eventFrame {
	t.Helper()

	var frame eventFrame

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			return frame
		}

		switch {
		case strings.HasPrefix(line, "event: "):
			frame.name = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "id: "):
			frame.id = strings.TrimPrefix(line, "id: ")
		case strings.HasPrefix(line, "data: "):
			frame.data = strings.TrimPrefix(line, "data: ")
		case strings.HasPrefix(line, ":"):
			frame.name = "heartbeat"
		}
	}

	t.Fatalf("SSE ended: %v", scanner.Err())

	return frame
}

func ready(t *testing.T, scanner *bufio.Scanner, cursor string) {
	t.Helper()
	frame := nextFrame(t, scanner)

	var body struct{ Cursor string }

	err := json.Unmarshal([]byte(frame.data), &body)
	if err != nil {
		t.Fatal(err)
	}

	if frame.name != "ready" || frame.id != cursor || body.Cursor != cursor {
		t.Errorf("ready %+v data %+v, want %s", frame, body, cursor)
	}
}

func inboxEvent(t *testing.T, scanner *bufio.Scanner, entryID string) {
	t.Helper()
	frame := nextFrame(t, scanner)

	var body struct {
		ID     string
		Unread bool
		Event  struct{ Type, Body string }
	}

	err := json.Unmarshal([]byte(frame.data), &body)
	if err != nil {
		t.Fatal(err)
	}

	if frame.name != "inbox" || frame.id != entryID || body.ID != entryID ||
		!body.Unread || body.Event.Type != "message" || body.Event.Body != "hello" {
		t.Errorf("frame %+v data %+v", frame, body)
	}
}

func TestEventsBacklogAndReconnect(t *testing.T) {
	t.Parallel()
	server := startServer(t, testFake(), daemon.Options{})
	seed(t, server, aliceACI, 1000)
	seed(t, server, bobACI, 2000)
	seed(t, server, aliceACI, 3000)
	scanner, closeStream := openEvents(t, server, "/v1/events?cursor=0", nil)
	ready(t, scanner, "0")
	inboxEvent(t, scanner, "1")
	inboxEvent(t, scanner, "2")
	inboxEvent(t, scanner, "3")

	err := closeStream()
	if err != nil {
		t.Fatal(err)
	}

	scanner, _ = openEvents(t, server, "/v1/events?cursor=0", map[string]string{"Last-Event-ID": "2"})
	ready(t, scanner, "2")
	inboxEvent(t, scanner, "3")
	filtered, _ := openEvents(t, server, "/v1/events?chat="+aliceACI+"&cursor=0", nil)
	ready(t, filtered, "0")
	inboxEvent(t, filtered, "1")
	inboxEvent(t, filtered, "3")
	// A header cursor overrides an invalid query cursor, too.
	override, _ := openEvents(t, server, "/v1/events?cursor=invalid", map[string]string{"Last-Event-ID": "3"})
	ready(t, override, "3")

	if len(server.fake.Receipts()) != 0 {
		t.Error("streaming sent receipts")
	}
}

func TestEventsMissingCursorSnapshotsUnfilteredTail(t *testing.T) {
	t.Parallel()
	server := startServer(t, testFake(), daemon.Options{})
	seed(t, server, aliceACI, 1000)
	seed(t, server, bobACI, 2000)
	scanner, _ := openEvents(t, server, "/v1/events?chat="+aliceACI, nil)
	ready(t, scanner, "2")

	if !server.fake.Push(message(aliceACI, 3000)) {
		t.Fatal("push failed")
	}

	inboxEvent(t, scanner, "3")
}

func TestEventsIndependentReadersAndCancellation(t *testing.T) {
	t.Parallel()
	server := startServer(t, testFake(), daemon.Options{})
	one, closeFirst := openEvents(t, server, "/v1/events?cursor=0", nil)
	two, _ := openEvents(t, server, "/v1/events?cursor=0", nil)
	ready(t, one, "0")
	ready(t, two, "0")

	if !server.fake.Push(message(aliceACI, 1000)) {
		t.Fatal("push failed")
	}

	inboxEvent(t, one, "1")
	inboxEvent(t, two, "1")

	err := closeFirst()
	if err != nil {
		t.Fatal(err)
	}

	if !server.fake.Push(message(aliceACI, 2000)) {
		t.Fatal("push failed")
	}

	inboxEvent(t, two, "2")

	started := time.Now()

	err = server.stop(t)
	if err != nil {
		t.Fatal(err)
	}

	if time.Since(started) > time.Second {
		t.Errorf("open SSE delayed cancellation: %v", time.Since(started))
	}

	if two.Scan() || (two.Err() != nil && !errors.Is(two.Err(), io.EOF)) {
		t.Errorf("SSE after stop: %q, %v", two.Text(), two.Err())
	}
	// ServeHTTP leaves ownership of the client to its caller.
	_, err = server.client.Account(t.Context())
	if err != nil {
		t.Errorf("server closed Signal client: %v", err)
	}
}

func TestEventsHeartbeatKeepsIdleStreamOpen(t *testing.T) {
	t.Parallel()
	server := startServer(t, testFake(), daemon.Options{})
	scanner, _ := openEvents(t, server, "/v1/events", nil)
	ready(t, scanner, "0")

	started := time.Now()

	frame := nextFrame(t, scanner)
	if frame.name != "heartbeat" || time.Since(started) < 14*time.Second || time.Since(started) > 20*time.Second {
		t.Errorf("heartbeat %+v after %v", frame, time.Since(started))
	}

	if !server.fake.Push(message(aliceACI, 1000)) {
		t.Fatal("push failed")
	}

	inboxEvent(t, scanner, "1")
}

func TestEventsValidateStorageBeforeHeaders(t *testing.T) {
	t.Parallel()

	fake := testFake()
	fake.InboxErr = errDatabase
	server := startServer(t, fake, daemon.Options{})
	assertError(t, server.request(t, http.MethodGet, "/v1/events?cursor=0", "", nil), 500, "internal_error")
}
