package web_test

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow/protobuf/signalpb"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow/web"
)

func TestLifecycleLateResponseRegistration(t *testing.T) {
	ctx := lifecycleContext(t)
	accepted, joining, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	var pending <-chan *signalpb.WebSocketResponseMessage
	var remaining func() int
	var drainOrphans func()
	reset := web.SetLifecycleRegistrationHooksForTest(func(channel <-chan *signalpb.WebSocketResponseMessage, count func() int, drain func()) {
		pending, remaining, drainOrphans = channel, count, drain
		close(accepted)
		select {
		case <-release:
		case <-ctx.Done():
		}
	}, func() { close(joining) })
	defer reset()
	peerReady := make(chan *websocket.Conn, 1)
	original := web.SignalHTTPClient
	web.SignalHTTPClient = &http.Client{Transport: websocketFixtureTransport(func(req *http.Request) (*http.Response, error) {
		clientPipe, serverPipe := net.Pipe()
		writer := &websocketUpgradeRecorder{ResponseRecorder: httptest.NewRecorder(), connection: serverPipe}
		peer, err := websocket.Accept(writer, req, nil)
		if err != nil {
			_ = clientPipe.Close()
			_ = serverPipe.Close()
			return nil, err
		}
		peerReady <- peer
		return &http.Response{StatusCode: writer.Code, Header: writer.Header(), Body: clientPipe}, nil
	})}
	defer func() { web.SignalHTTPClient = original }()
	loopCtx, cancelLoop := context.WithCancel(ctx)
	callerCtx, cancelCaller := context.WithCancel(ctx)
	socket := web.NewSignalWebsocket(nil)
	status := socket.Connect(loopCtx, nil)
	statusDone := make(chan struct{})
	go func() {
		defer close(statusDone)
		for range status {
		}
	}()
	callerDone := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		defer close(callerDone)
		// Self-delete avoids the ordinary retry when disconnect closes a response.
		_, err := socket.SendRequest(callerCtx, http.MethodDelete, "/v1/devices/fixture", nil, nil)
		result <- err
	}()
	defer func() {
		cancelLoop()
		cancelCaller()
		releaseOnce.Do(func() { close(release) })
		_ = socket.Close()
		if drainOrphans != nil {
			drainOrphans()
		} // Fixture-only recovery on the old ordering.
		lifecycleWait(t, ctx, statusDone)
		lifecycleWait(t, ctx, callerDone)
	}()
	var peer *websocket.Conn
	select {
	case peer = <-peerReady:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	defer peer.CloseNow()
	lifecycleWait(t, ctx, accepted)
	cancelLoop()
	_ = peer.CloseNow()
	lifecycleWait(t, ctx, joining)
	// On the old coordinator the drain has already run. On the repaired
	// coordinator it cannot run until this accepted writer request finishes.
	releaseOnce.Do(func() { close(release) })
	lifecycleWait(t, ctx, statusDone)
	_ = socket.Close()
	select {
	case _, ok := <-pending:
		if ok {
			t.Error("pending channel delivered an unexpected response")
		}
	default:
		t.Error("accepted response channel remained open after worker cleanup")
	}
	if got := remaining(); got != 0 {
		t.Errorf("cleanup left %d pending responses", got)
	}
	select {
	case err := <-result:
		if err != nil {
			t.Errorf("self-delete returned %v", err)
		}
	case <-time.After(lifecycleWatchdog):
		t.Error("caller remained blocked after connection cleanup")
	}
}
