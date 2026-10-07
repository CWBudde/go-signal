//go:build cgo || libsignal_go

//nolint:paralleltest // The local websocket fixture replaces signalmeow's global HTTP transport.
package signal_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow/events"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow/protobuf/signalpb"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow/web"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow/wspb"
)

// Close must preserve account resources for upstream processing after our event callback,
// which has already left the facade's handling wait group.
//
//nolint:cyclop,funlen // The fixture lifetime and the resource-release assertions belong together.
func TestCloseWaitsForWebsocketHandlerBeforeReleasingStore(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	client := openOffline(t, seedAccount(t))
	cli := signal.InitializeProfileClient(ctx, client)
	recipients := cli.Store.RecipientStore

	_, err := recipients.LoadAllContacts(ctx)
	if err != nil {
		t.Fatalf("read open account store: %v", err)
	}

	entered := make(chan struct{})
	releaseHandler := make(chan struct{})
	handlerDone := make(chan struct{})

	var handlerReadErr error

	socket := web.NewSignalWebsocket(nil)
	closing := signal.TimerPersistenceContext(client, nil)
	socketCtx, cancelSocket := context.WithCancel(ctx)

	peer := make(chan *websocket.Conn, 1)
	peerDone := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		defer close(peerDone)

		conn, err := websocket.Accept(writer, request, nil)
		if err != nil {
			t.Errorf("accept websocket: %v", err)
			return
		}
		defer func() { _ = conn.CloseNow() }()

		peer <- conn

		id, method, path := uint64(1), http.MethodPut, "/test/facade-handler"

		message := &signalpb.WebSocketMessage{
			Type: signalpb.WebSocketMessage_REQUEST.Enum(),
			Request: &signalpb.WebSocketRequestMessage{
				Id: &id, Verb: &method, Path: &path,
			},
		}

		err = wspb.Write(ctx, conn, message)
		if err != nil {
			t.Errorf("send incoming websocket request: %v", err)
			return
		}

		// Read until shutdown, servicing the websocket close handshake.
		for {
			_, _, err = conn.Read(ctx)
			if err != nil {
				return
			}
		}
	}))
	t.Cleanup(server.Close)

	localURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(signal.SetSignalTransport(profileRoundTrip(func(request *http.Request) (*http.Response, error) {
		routed := request.Clone(request.Context())
		routed.URL.Scheme = localURL.Scheme
		routed.URL.Host = localURL.Host

		return http.DefaultTransport.RoundTrip(routed)
	})))

	closed := make(chan struct{})

	var closeErr error

	var closeOnce, releaseOnce sync.Once

	startClose := func() {
		closeOnce.Do(func() {
			go func() {
				closeErr = client.Close()

				close(closed)
			}()
		})
	}
	statusDone := make(chan struct{})
	// Register last: release and join all workers before restoring transport or closing fixtures.
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(releaseHandler) })
		cancelSocket()
		startClose()

		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()

		waitWebsocketLifecycle(t, cleanupCtx, closed, "facade Close")

		socketCloseErr := socket.Close()
		if socketCloseErr != nil {
			t.Errorf("close fixture websocket: %v", socketCloseErr)
		}

		waitWebsocketLifecycle(t, cleanupCtx, statusDone, "websocket status drain")

		select {
		case conn := <-peer:
			_ = conn.CloseNow()

			waitWebsocketLifecycle(t, cleanupCtx, peerDone, "websocket peer")
		default:
		}

		select {
		case <-entered:
			waitWebsocketLifecycle(t, cleanupCtx, handlerDone, "upstream handler")
		default:
		}
	})

	cli.AuthedWS = socket
	statuses := socket.Connect(socketCtx, func(
		context.Context, *signalpb.WebSocketRequestMessage,
	) (*web.SimpleResponse, error) {
		// Ignored events complete the facade callback without setting its ack-flush flag.
		if !cli.EventHandler(&events.ACIFound{}) {
			t.Error("facade callback refused an event before Close")
		}

		close(entered)
		<-releaseHandler
		// The socket context is canceled during Close; independently scoped processing must
		// still have access to the account store until the entire handler returns.
		_, handlerReadErr = recipients.LoadAllContacts(ctx)

		close(handlerDone)

		return &web.SimpleResponse{Status: http.StatusOK}, nil
	})

	go func() {
		defer close(statusDone)

		for range statuses {
		}
	}()

	waitWebsocketLifecycle(t, ctx, entered, "request handler entry")
	startClose()
	waitWebsocketLifecycle(t, ctx, closing, "facade shutdown start")

	select {
	case <-closed:
		t.Errorf("Close returned before the upstream websocket handler completed: %v", closeErr)
	case <-time.After(150 * time.Millisecond):
	case <-ctx.Done():
		t.Fatal("waiting for Close ordering:", ctx.Err())
	}

	releaseOnce.Do(func() { close(releaseHandler) })
	waitWebsocketLifecycle(t, ctx, handlerDone, "upstream store read")

	if handlerReadErr != nil {
		t.Errorf("account store closed under upstream websocket handler: %v", handlerReadErr)
	}

	waitWebsocketLifecycle(t, ctx, closed, "facade Close")

	if closeErr != nil {
		t.Errorf("Close: %v", closeErr)
	}

	if _, ok := <-client.Events(); ok {
		t.Error("Events remains open after Close")
	}

	_, err = recipients.LoadAllContacts(ctx)
	if err == nil {
		t.Error("account store remains open after Close")
	}
}

func waitWebsocketLifecycle(t *testing.T, ctx context.Context, done <-chan struct{}, operation string) {
	t.Helper()

	select {
	case <-done:
	case <-ctx.Done():
		t.Fatalf("waiting for %s: %v", operation, ctx.Err())
	}
}

// ackPeer is the server side of the ack-flush fixture: it records the order in which the
// response to the delivered request and the keepalive arrive, and answers keepalives.
type ackPeer struct {
	mu        sync.Mutex
	order     []string
	keepalive chan struct{}
	once      sync.Once
}

func (p *ackPeer) record(what string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.order = append(p.order, what)
}

func (p *ackPeer) seen() []string {
	p.mu.Lock()
	defer p.mu.Unlock()

	return append([]string(nil), p.order...)
}

// serve reads until shutdown. Responses are recorded by request ID; keepalives are recorded
// and answered.
func (p *ackPeer) serve(ctx context.Context, conn *websocket.Conn) {
	for {
		msg := &signalpb.WebSocketMessage{}

		err := wspb.Read(ctx, conn, msg)
		if err != nil {
			return
		}

		switch msg.GetType() {
		case signalpb.WebSocketMessage_RESPONSE:
			p.record(fmt.Sprintf("ack %d", msg.GetResponse().GetId()))
		case signalpb.WebSocketMessage_REQUEST:
			p.record("keepalive")
			p.once.Do(func() { close(p.keepalive) })

			status := uint32(http.StatusOK)

			err = wspb.Write(ctx, conn, &signalpb.WebSocketMessage{
				Type:     signalpb.WebSocketMessage_RESPONSE.Enum(),
				Response: &signalpb.WebSocketResponseMessage{Id: msg.GetRequest().Id, Status: &status},
			})
			if err != nil {
				return
			}
		case signalpb.WebSocketMessage_UNKNOWN:
		}
	}
}

// Close must flush the ack of an event the consumer has read even when signalmeow's request
// handler is still working after our callback returned (it sends the delivery receipt and
// clears the decryption buffer before it queues the response). Live, the keepalive flush
// overtook the handler, the shutdown canceled it, and the message was delivered again.
// A request whose event was not read stays unacknowledged.
//
//nolint:cyclop,funlen,gocognit,maintidx // The fixture lifetime and the ordering assertions belong together.
func TestCloseFlushesAckOfReadEventAfterUpstreamHandler(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	client := openOffline(t, seedAccount(t))
	cli := signal.InitializeProfileClient(ctx, client)
	closing := signal.TimerPersistenceContext(client, nil)

	const delivered, unread = uint64(7), uint64(8)

	peer := &ackPeer{keepalive: make(chan struct{})}
	conns := make(chan *websocket.Conn, 1)
	peerDone := make(chan struct{})

	var accepted atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		conn, err := websocket.Accept(writer, request, nil)
		if err != nil {
			t.Errorf("accept websocket: %v", err)
			return
		}
		defer func() { _ = conn.CloseNow() }()

		// The unauthenticated websocket only needs to stay connected.
		if accepted.Add(1) > 1 {
			for {
				_, _, err = conn.Read(ctx)
				if err != nil {
					return
				}
			}
		}

		defer close(peerDone)

		conns <- conn

		for _, id := range []uint64{delivered, unread} {
			method, path := http.MethodPut, "/api/v1/message"

			err = wspb.Write(ctx, conn, &signalpb.WebSocketMessage{
				Type:    signalpb.WebSocketMessage_REQUEST.Enum(),
				Request: &signalpb.WebSocketRequestMessage{Id: &id, Verb: &method, Path: &path},
			})
			if err != nil {
				t.Errorf("send incoming websocket request: %v", err)
				return
			}
		}

		peer.serve(ctx, conn)
	}))
	t.Cleanup(server.Close)

	localURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(signal.SetSignalTransport(profileRoundTrip(func(request *http.Request) (*http.Response, error) {
		routed := request.Clone(request.Context())
		routed.URL.Scheme = localURL.Scheme
		routed.URL.Host = localURL.Host

		return http.DefaultTransport.RoundTrip(routed)
	})))

	release := make(chan struct{})
	handlerDone := make(chan struct{})
	closed := make(chan struct{})
	statusDone := make(chan struct{})

	var releaseOnce, closeOnce, handlerOnce sync.Once

	var closeErr error

	startClose := func() {
		closeOnce.Do(func() {
			go func() {
				closeErr = client.Close()

				close(closed)
			}()
		})
	}

	socket := web.NewSignalWebsocket(nil)
	unauthed := web.NewSignalWebsocket(nil)
	socketCtx, cancelSocket := context.WithCancel(ctx)
	unauthedDone := make(chan struct{})

	// Register last: release and join all workers before restoring transport or closing fixtures.
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(release) })
		cancelSocket()
		startClose()

		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()

		waitWebsocketLifecycle(t, cleanupCtx, closed, "facade Close")

		_ = socket.Close()
		_ = unauthed.Close()

		waitWebsocketLifecycle(t, cleanupCtx, statusDone, "websocket status drain")
		waitWebsocketLifecycle(t, cleanupCtx, unauthedDone, "unauthenticated status drain")

		select {
		case conn := <-conns:
			_ = conn.CloseNow()

			waitWebsocketLifecycle(t, cleanupCtx, peerDone, "websocket peer")
		default:
		}
	})

	cli.AuthedWS = socket
	statuses := socket.Connect(socketCtx, func(
		handlerCtx context.Context, request *signalpb.WebSocketRequestMessage,
	) (*web.SimpleResponse, error) {
		if !cli.EventHandler(receiptEvent(signalpb.ReceiptMessage_READ)) {
			// What signalmeow does for a refused event: no response, no ack.
			return nil, fmt.Errorf("request %d: %w", request.GetId(), signalmeow.ErrHandlerFailed)
		}

		// signalmeow's work after our callback, e.g. the delivery receipt's network round trip.
		select {
		case <-release:
		case <-handlerCtx.Done():
		}

		handlerOnce.Do(func() { close(handlerDone) })

		return &web.SimpleResponse{Status: http.StatusOK}, nil
	})

	go func() {
		defer close(statusDone)

		for range statuses {
		}
	}()

	// Connect the unauthenticated websocket only once the peer above holds the authenticated one.
	select {
	case conn := <-conns:
		conns <- conn
	case <-ctx.Done():
		t.Fatal("waiting for the authenticated websocket:", ctx.Err())
	}

	cli.UnauthedWS = unauthed
	unauthedStatuses := unauthed.Connect(socketCtx, func(
		context.Context, *signalpb.WebSocketRequestMessage,
	) (*web.SimpleResponse, error) {
		t.Error("request on the unauthenticated websocket")

		return nil, signalmeow.ErrHandlerFailed
	})

	go func() {
		defer close(unauthedDone)

		for range unauthedStatuses {
		}
	}()

	for !cli.IsConnected() {
		select {
		case <-ctx.Done():
			t.Fatal("waiting for both websockets:", ctx.Err())
		case <-time.After(time.Millisecond):
		}
	}

	select {
	case evt := <-client.Events():
		if _, ok := evt.(*signal.Receipt); !ok {
			t.Fatalf("event = %T, want *signal.Receipt", evt)
		}
	case <-ctx.Done():
		t.Fatal("waiting for the delivered event:", ctx.Err())
	}

	start := time.Now()

	startClose()
	waitWebsocketLifecycle(t, ctx, closing, "facade shutdown start")

	// Hold the upstream handler until Close has either flushed without it (the bug) or
	// demonstrably waits for it.
	select {
	case <-peer.keepalive:
	case <-time.After(150 * time.Millisecond):
	case <-ctx.Done():
		t.Fatal("waiting for Close ordering:", ctx.Err())
	}

	releaseOnce.Do(func() { close(release) })
	waitWebsocketLifecycle(t, ctx, handlerDone, "upstream handler")
	waitWebsocketLifecycle(t, ctx, closed, "facade Close")

	elapsed := time.Since(start)

	if closeErr != nil {
		t.Errorf("Close: %v", closeErr)
	}

	// The fixture's websocket close handshake makes the peer see everything Close flushed.
	cancelSocket()

	if conn, ok := <-conns; ok {
		_ = conn.CloseNow()
	}

	waitWebsocketLifecycle(t, ctx, peerDone, "websocket peer")

	want := []string{fmt.Sprintf("ack %d", delivered), "keepalive"}
	if got := peer.seen(); !slices.Equal(got, want) {
		t.Errorf("server saw %q, want %q: the read event's ack must be queued before the flush and "+
			"the unread event must stay unacknowledged", got, want)
	}

	if elapsed > 2*time.Second {
		t.Errorf("Close took %v", elapsed)
	}
}
