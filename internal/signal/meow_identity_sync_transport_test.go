//go:build cgo || libsignal_go

package signal_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/mautrix-signal/pkg/libsignalgo"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow/protobuf/signalpb"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow/web"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow/wspb"
)

//nolint:paralleltest,funlen,gocognit,cyclop,gocyclo // serial transport, encrypted retry and cancellation scenario
func TestIdentityVerificationConnectedTransport(t *testing.T) {
	env := newTrustEnv(t)
	env.saveAlice(t, newIdentityKey(t))
	signal.ConnectOffline(t.Context(), env.client, signal.SendOnly())
	cli := signal.InitializeProfileClient(t.Context(), env.client)

	local, err := cli.Store.ACIServiceID().Address(uint(cli.Store.DeviceID))
	if err != nil {
		t.Fatal(err)
	}

	remote, err := cli.Store.ACIServiceID().Address(98)
	if err != nil {
		t.Fatal(err)
	}

	err = libsignalgo.ProcessPreKeyBundle(t.Context(), preKeyBundle(t, cli.Store.ACIIdentityKeyPair, 98),
		remote, local,
		cli.Store.ACISessionStore, cli.Store.ACIIdentityStore)
	if err != nil {
		t.Fatal(err)
	}

	var (
		sends  atomic.Int32
		status atomic.Uint32
	)
	status.Store(http.StatusBadRequest)

	withheld := make(chan struct{}, 1)

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		conn, acceptErr := websocket.Accept(writer, request, nil)
		if acceptErr != nil {
			t.Error(acceptErr)
			return
		}
		defer func() { _ = conn.CloseNow() }()

		for {
			var msg signalpb.WebSocketMessage

			readErr := wspb.Read(request.Context(), conn, &msg)
			if readErr != nil {
				return
			}

			req := msg.GetRequest()
			if req.GetVerb() != http.MethodPut || req.GetPath() != "/v1/messages/"+seededACI {
				t.Errorf("wrong sync target: %s %s", req.GetVerb(), req.GetPath())
			}

			var outgoing signalmeow.MyMessages

			decodeErr := json.Unmarshal(req.GetBody(), &outgoing)
			if decodeErr != nil {
				t.Error(decodeErr)
			}

			if outgoing.Urgent || len(outgoing.Messages) != 1 || outgoing.Messages[0].DestinationDeviceID != 98 ||
				len(outgoing.Messages[0].Content) == 0 {
				t.Errorf("sync envelopes=%+v", outgoing)
			}

			sends.Add(1)

			if status.Load() == 0 {
				select {
				case withheld <- struct{}{}:
				default:
				}

				continue
			}

			response := &signalpb.WebSocketMessage{
				Type:     signalpb.WebSocketMessage_RESPONSE.Enum(),
				Response: &signalpb.WebSocketResponseMessage{Id: req.Id, Status: new(status.Load()), Body: []byte(`{}`)},
			}

			writeErr := wspb.Write(request.Context(), conn, response)
			if writeErr != nil {
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
		routed.URL.Scheme, routed.URL.Host = localURL.Scheme, localURL.Host

		return http.DefaultTransport.RoundTrip(routed)
	})))
	socketCtx, cancel := context.WithCancel(t.Context())
	socket := web.NewSignalWebsocket(nil)
	statuses := socket.Connect(socketCtx, nil)
	drained := make(chan struct{})
	connected := make(chan struct{}, 64)

	go func() {
		defer close(drained)

		for update := range statuses {
			if update.Event == web.SignalWebsocketConnectionEventConnected {
				connected <- struct{}{}
			}
		}
	}()

	t.Cleanup(func() { cancel(); _ = socket.Close(); <-drained })
	waitProfileSocket(t, connected)

	cli.AuthedWS = socket

	identity, err := env.client.TrustIdentity(t.Context(), signal.Recipient{ACI: aliceACI}, "")
	if !errors.Is(err, signal.ErrSendFailed) || identity.Trust != signal.TrustUnverified || queuedCount(t, env) != 1 ||
		sends.Load() != 1 {
		t.Fatalf("partial connected trust=%+v,%v sends=%d", identity, err, sends.Load())
	}

	status.Store(http.StatusOK)

	identity, err = env.client.TrustIdentity(t.Context(), signal.Recipient{ACI: aliceACI}, "")
	if err != nil || identity.Trust != signal.TrustUnverified || queuedCount(t, env) != 0 || sends.Load() != 2 {
		t.Fatalf("connected retry=%+v,%v sends=%d", identity, err, sends.Load())
	}
	// Cancellation after request submission must report partial success without losing retry.
	status.Store(0)

	syncCtx, cancelSync := context.WithCancel(t.Context())
	defer cancelSync()

	finished := make(chan error, 1)

	go func() {
		_, trustErr := env.client.TrustIdentity(syncCtx, signal.Recipient{ACI: aliceACI}, "")
		finished <- trustErr
	}()

	select {
	case <-withheld:
	case <-time.After(3 * time.Second):
		t.Fatal("cancellation request missing")
	}

	cancelSync()

	select {
	case err = <-finished:
		if !errors.Is(err, context.Canceled) || queuedCount(t, env) != 1 {
			t.Fatalf("canceled sync lost partial error/queue: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("canceled sync did not return")
	}
}
