//go:build cgo || libsignal_go

//nolint:paralleltest // The local websocket fixture replaces signalmeow's global HTTP transport.
package signal_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/cwbudde/go-signal/internal/signal"
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
