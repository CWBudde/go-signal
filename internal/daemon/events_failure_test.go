package daemon_test

import (
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/internal/daemon"
	"github.com/cwbudde/go-signal/internal/signal"
)

func TestEventsBacklogCrossesWaitPageBoundary(t *testing.T) {
	t.Parallel()

	server := startServer(t, testFake(), daemon.Options{})
	for n := range 53 {
		seed(t, server, aliceACI, uint64(n+1))
	}

	scanner, _ := openEvents(t, server, "/v1/events?cursor=0", nil)
	ready(t, scanner, "0")

	for n := range 53 {
		inboxEvent(t, scanner, strconv.Itoa(n+1))
	}
}

type laterInboxFailure struct {
	signal.Client

	reads atomic.Int32
}

func (c *laterInboxFailure) InboxList(ctx context.Context, query signal.InboxQuery) ([]signal.InboxEntry, error) {
	if c.reads.Add(1) > 1 {
		return nil, errLaterDatabase
	}

	return c.Client.InboxList(ctx, query) //nolint:wrapcheck // test wrapper preserves the client error
}

func TestEventsStorageFailureAfterReadyClosesStream(t *testing.T) {
	t.Parallel()

	fake := testFake()

	client := openClient(t, fake)
	server := serveClient(t, fake, &laterInboxFailure{Client: client}, listenLoopback(t), daemon.Options{})

	scanner, _ := openEvents(t, server, "/v1/events?cursor=0", nil)
	ready(t, scanner, "0")

	if scanner.Scan() || scanner.Err() != nil {
		t.Errorf("stream after failure %q, %v", scanner.Text(), scanner.Err())
	}

	var health struct{ Account string }
	decodeResponse(t, server.request(t, http.MethodGet, "/v1/health", "", nil), &health)

	if health.Account != ownACI {
		t.Errorf("healthy endpoint failed: %+v", health)
	}
}

// slowConnection models a peer that stops reading. Its first response blocks until
// the server's write deadline, while other accepted sockets retain normal I/O.
type slowConnection struct {
	net.Conn

	mu         sync.Mutex
	deadline   time.Time
	blocked    chan struct{}
	expired    chan struct{}
	closed     chan struct{}
	blockOnce  sync.Once
	expireOnce sync.Once
	closeOnce  sync.Once
}

func (c *slowConnection) SetWriteDeadline(deadline time.Time) error {
	c.mu.Lock()
	c.deadline = deadline
	c.mu.Unlock()

	return c.Conn.SetWriteDeadline(deadline) //nolint:wrapcheck // test connection forwards the network error
}

func (c *slowConnection) Write([]byte) (int, error) {
	c.blockOnce.Do(func() { close(c.blocked) })
	c.mu.Lock()
	deadline := c.deadline
	c.mu.Unlock()

	if deadline.IsZero() {
		<-c.closed
		return 0, net.ErrClosed
	}

	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()

	select {
	case <-timer.C:
		c.expireOnce.Do(func() { close(c.expired) })
		return 0, os.ErrDeadlineExceeded
	case <-c.closed:
		return 0, net.ErrClosed
	}
}

func (c *slowConnection) Close() error {
	c.closeOnce.Do(func() { close(c.closed) })
	return c.Conn.Close() //nolint:wrapcheck // test connection forwards the network error
}

type slowListener struct {
	net.Listener

	accepted atomic.Int32
	slow     *slowConnection
}

func (l *slowListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err //nolint:wrapcheck // test listener forwards the network error
	}

	if l.accepted.Add(1) == 1 {
		l.slow.Conn = conn
		return l.slow, nil
	}

	return conn, nil
}

func TestEventsSlowWriterDeadlineAndReaderIsolation(t *testing.T) {
	t.Parallel()

	fake := testFake()

	client := openClient(t, fake)
	slow := &slowConnection{blocked: make(chan struct{}), expired: make(chan struct{}), closed: make(chan struct{})}
	listener := &slowListener{Listener: listenLoopback(t), slow: slow}
	server := serveClient(t, fake, client, listener, daemon.Options{})
	ctx := t.Context()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.url+"/v1/events?cursor=0", nil)
	if err != nil {
		t.Fatal(err)
	}

	req.Header.Set(authorizationHeader, "Bearer "+testToken)

	completed := make(chan error, 1)

	go func() {
		resp, requestErr := http.DefaultClient.Do(req)
		if resp != nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}

		completed <- requestErr
	}()

	select {
	case <-slow.blocked:
	case <-time.After(2 * time.Second):
		t.Fatal("slow writer never blocked")
	}

	scanner, _ := openEvents(t, server, "/v1/events?cursor=0", nil)
	ready(t, scanner, "0")

	pushMessage(t, server, 1000)

	inboxEvent(t, scanner, "1")
	// The blocked subscriber's write has a finite deadline and never stalls receiving.
	select {
	case <-slow.expired:
	case <-time.After(12 * time.Second):
		t.Fatal("SSE write deadline did not expire")
	}

	select {
	case <-completed:
	case <-time.After(time.Second):
		t.Fatal("slow request did not terminate")
	}

	pushMessage(t, server, 2000)

	inboxEvent(t, scanner, "2")
}

func pushMessage(t *testing.T, server *runningServer, timestamp uint64) {
	t.Helper()

	if !server.fake.Push(message(aliceACI, timestamp)) {
		t.Fatal("push failed")
	}
}
