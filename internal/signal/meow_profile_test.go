//go:build cgo || libsignal_go

//nolint:paralleltest // HTTP transport replacement requires serial parent and child tests.
package signal_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/go-signal/internal/store"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow/protobuf/signalpb"
	mstore "github.com/cwbudde/mautrix-signal/pkg/signalmeow/store"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow/types"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow/web"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow/wspb"
	"github.com/google/uuid"
	"github.com/rs/zerolog"
)

const (
	profileFetchFailure     = "fetch"
	profileInitialKey       = "initial-key"
	profilePreWriteKey      = "pre-write-key"
	profileReject           = "reject"
	profileUncertain        = "uncertain"
	profileNoop             = "noop"
	profileMismatch         = "mismatch"
	profileNotify           = "notify"
	profileSuccess          = "profile-success"
	profileCancel           = "cancel"
	profileAcceptedBody     = "accepted-body"
	profileCachePersist     = "cache-persist"
	profileVerificationRead = "verification-read"
)

func TestUpdateOwnProfileOnce(t *testing.T) { //nolint:cyclop,funlen,gocognit,gocyclo,maintidx // failure matrix
	t.Parallel()

	for _, mode := range []string{
		profileSuccess, profileNoop, profileFetchFailure, profileInitialKey, profilePreWriteKey, profileReject,
		profileUncertain, profileAcceptedBody, profileVerificationRead, profileMismatch, profileCachePersist,
		profileNotify, profileCancel,
	} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			raw := validRawProfile(t)

			current, err := signal.DecodeOwnProfile(raw, seededACI, profileTestKey())
			if err != nil {
				t.Fatal(err)
			}

			reads, writes, checks, refreshes, persists, notifications := 0, 0, 0, 0, 0, 0
			after := raw

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()

			update := signal.ProfileUpdate{About: new("new about")}
			if mode == profileNoop {
				update.About = new(current.About)
			}

			hooks := signal.ProfileUpdateHooks{
				Fetch: func(context.Context) (signal.RawOwnProfile, signal.Profile, error) {
					reads++
					if mode == profileFetchFailure || reads > 1 && mode == profileVerificationRead {
						return signal.RawOwnProfile{}, signal.Profile{}, io.ErrUnexpectedEOF
					}

					if reads == 1 {
						return raw, current, nil
					}

					profile, decodeErr := signal.DecodeOwnProfile(after, seededACI, profileTestKey())

					return after, profile, decodeErr //nolint:wrapcheck // test hook forwards facade error
				},
				CheckKey: func(context.Context) error {
					checks++
					if mode == profileInitialKey || mode == profilePreWriteKey && checks == 2 {
						return signal.ErrProfileKeyChanged
					}

					return nil
				},
				Write: func(_ context.Context, req signal.ProfileWriteRequest) (bool, error) {
					writes++
					after.Name = req.Name
					after.About = req.About

					after.AboutEmoji = req.AboutEmoji
					if mode == profileMismatch {
						after.Avatar = "different"
					}

					if mode == profileReject {
						return false, signal.ErrProfileRejected
					}

					if mode == profileUncertain {
						return false, io.ErrUnexpectedEOF
					}

					if mode == profileAcceptedBody {
						after.Credential = nil
						return true, io.ErrUnexpectedEOF
					}

					if mode == profileCancel {
						cancel()
					}

					return true, nil
				},
				Refresh: func(context.Context) error {
					refreshes++

					if mode == profileCachePersist {
						return io.ErrUnexpectedEOF
					}

					return nil
				},
				Persist: func(_ context.Context, p signal.Profile) error {
					persists++

					if p.About != "new about" {
						t.Errorf("persisted proposed/unverified profile: %+v", p)
					}

					if mode == profileCachePersist {
						return io.ErrClosedPipe
					}

					return nil
				},
				Notify: func(context.Context) error {
					notifications++

					if mode == profileNotify {
						return io.ErrClosedPipe
					}

					return nil
				},
			}
			result, err := signal.UpdateOwnProfileOnce(ctx, seededACI, profileTestKey(), update, hooks)

			switch mode {
			case profileFetchFailure, profileInitialKey, profilePreWriteKey:
				if err == nil || writes != 0 || notifications != 0 {
					t.Fatalf("preflight wrote: %+v %v calls=%d/%d", result, err, writes, notifications)
				}
			case profileNoop:
				if err != nil ||
					result.Changed ||
					result.Accepted ||
					!result.Verified ||
					writes != 0 ||
					notifications != 0 {
					t.Fatalf("no-op=%+v %v", result, err)
				}
			case profileReject, profileUncertain:
				if err == nil || result.Accepted || result.Profile.ACI != "" || writes != 1 || notifications != 0 {
					t.Fatalf("rejection=%+v %v", result, err)
				}
			case profileAcceptedBody, profileVerificationRead, profileMismatch, profileCancel:
				if err == nil ||
					!result.Accepted ||
					!result.Changed ||
					result.Verified ||
					result.Profile.ACI != seededACI ||
					result.Profile.GivenName != "" ||
					writes != 1 ||
					notifications != 1 ||
					refreshes != 0 ||
					persists != 0 {
					t.Fatalf("unverified=%+v err=%v calls=%d/%d/%d/%d", result, err, writes, notifications, refreshes, persists)
				}

				if mode == profileAcceptedBody && !errors.Is(err, io.ErrUnexpectedEOF) {
					t.Fatal(err)
				}
			default:
				if !result.Accepted ||
					!result.Verified ||
					!result.Changed ||
					result.Profile.About != "new about" ||
					writes != 1 ||
					refreshes != 1 ||
					persists != 1 ||
					notifications != 1 {
					t.Fatalf("verified=%+v err=%v calls=%d/%d/%d/%d", result, err, writes, refreshes, persists, notifications)
				}

				if mode == profileSuccess && err != nil {
					t.Fatal(err)
				}

				if mode == profileCachePersist && (!errors.Is(err, io.ErrUnexpectedEOF) ||
					!errors.Is(err, io.ErrClosedPipe)) {
					t.Fatalf("lost independent errors: %v", err)
				}

				if mode == profileNotify && !errors.Is(err, io.ErrClosedPipe) {
					t.Fatal(err)
				}
			}
		})
	}
}

func openProfileOffline(t *testing.T) (signal.Client, string) {
	t.Helper()
	dataDir := seedAccount(t)
	client := openOffline(t, dataDir, signal.SendOnly())
	withStore(t, dataDir, func(device *mstore.Device, _ *store.Store) {
		err := device.RecipientStore.StoreProfileKey(t.Context(), uuid.MustParse(seededACI), profileTestKey())
		if err != nil {
			t.Fatal(err)
		}
	})
	signal.InitializeProfileClient(t.Context(), client)

	return client, dataDir
}

func TestOwnProfileFreshnessAndLifecycle(t *testing.T) { //nolint:cyclop,funlen,gocognit // lifecycle scenarios
	t.Run("fresh and key changed during read", func(t *testing.T) {
		client, dataDir := openProfileOffline(t)
		reads := 0

		t.Cleanup(signal.SetSignalTransport(profileRoundTrip(func(r *http.Request) (*http.Response, error) {
			reads++

			if !strings.HasPrefix(r.URL.Path, "/v1/profile/"+seededACI+"/") ||
				r.URL.Query().Get("credentialType") != "expiringProfileKey" {
				t.Error("wrong credential request path")
			}

			raw := validRawProfile(t)
			if reads == 2 {
				withStore(t, dataDir, func(device *mstore.Device, _ *store.Store) {
					key := profileTestKey()

					key[0]++

					err := device.RecipientStore.StoreProfileKey(t.Context(), uuid.MustParse(seededACI), key)
					if err != nil {
						t.Fatal(err)
					}
				})
			}

			body, _ := json.Marshal(raw)

			return profileHTTPResponse(200, string(body)), nil
		})))

		profile, err := client.OwnProfile(t.Context())
		if err != nil || profile.GivenName != "Given Name" || profile.FamilyName != "Family Name" {
			t.Fatalf("fresh=%+v %v", profile, err)
		}

		_, err = client.OwnProfile(t.Context())
		if !errors.Is(err, signal.ErrProfileKeyChanged) || reads != 2 {
			t.Fatalf("read key guard=%v reads=%d", err, reads)
		}
	})

	for _, code := range []int{401, 403} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			client, dataDir := openProfileOffline(t)
			puts := 0

			t.Cleanup(signal.SetSignalTransport(profileRoundTrip(func(r *http.Request) (*http.Response, error) {
				if r.Method == http.MethodPut {
					puts++
					return profileHTTPResponse(code, ""), nil
				}

				body, _ := json.Marshal(validRawProfile(t))

				return profileHTTPResponse(200, string(body)), nil
			})))
			_, err := client.UpdateOwnProfile(t.Context(), signal.ProfileUpdate{About: new("new")})

			want := signal.ErrProfileRejected
			if code == 401 {
				want = signal.ErrDeviceUnlinked
			}

			if !errors.Is(err, want) || puts != 1 {
				t.Fatalf("PUT result=%v puts=%d", err, puts)
			}

			if unlinkedAt(t, dataDir).IsZero() != (code == 403) {
				t.Fatal("wrong unlink state")
			}
		})
	}

	t.Run("closed", func(t *testing.T) {
		client, _ := openProfileOffline(t)

		closeErr := client.Close()
		if closeErr != nil {
			t.Fatal(closeErr)
		}

		_, err := client.OwnProfile(t.Context())
		if !errors.Is(err, signal.ErrClosed) {
			t.Fatalf("closed=%v", err)
		}
	})
	t.Run("queued cancellation and close", func(t *testing.T) {
		client, _ := openProfileOffline(t)
		signal.ProfileDrainTimeout(client)

		entered := make(chan struct{})

		t.Cleanup(signal.SetSignalTransport(profileRoundTrip(func(r *http.Request) (*http.Response, error) {
			close(entered)
			<-r.Context().Done()

			return nil, r.Context().Err()
		})))

		first := make(chan error, 1)

		go func() { _, err := client.OwnProfile(t.Context()); first <- err }()

		<-entered

		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		_, err := client.OwnProfile(ctx)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("queued cancel=%v", err)
		}

		err = client.Close()
		if err != nil {
			t.Fatal(err)
		}

		err = <-first
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("close cancellation=%v", err)
		}
	})
}

func TestProfilePersistencePreservesRecipient(t *testing.T) { //nolint:cyclop // verifies all unrelated recipient fields
	t.Parallel()
	dataDir := seedAccount(t)
	withStore(t, dataDir, func(device *mstore.Device, _ *store.Store) {
		aci := uuid.MustParse(seededACI)

		_, err := device.RecipientStore.LoadAndUpdateRecipient(t.Context(), aci, uuid.Nil,
			func(recipient *types.Recipient) (bool, error) {
				recipient.ContactName = "Address Book"
				recipient.Nickname = "nickname"
				recipient.Blocked = true
				recipient.E164 = "+12345"
				recipient.Whitelisted = new(true)
				recipient.Profile.Key = profileTestKey()

				return true, nil
			})
		if err != nil {
			t.Fatal(err)
		}

		err = signal.PersistOwnProfile(t.Context(), device.RecipientStore, signal.Profile{
			ACI: seededACI, GivenName: " Given ", FamilyName: " Family ",
			About: "updated", AboutEmoji: "🌊", AvatarPath: "same-avatar",
		})
		if err != nil {
			t.Fatal(err)
		}

		recipient, err := device.RecipientStore.LoadAndUpdateRecipient(t.Context(), aci, uuid.Nil,
			func(*types.Recipient) (bool, error) {
				return false, nil
			})
		if err != nil {
			t.Fatal(err)
		}

		if recipient.Profile.Name != " Given   Family " ||
			recipient.Profile.About != "updated" ||
			recipient.Profile.Key != profileTestKey() ||
			recipient.ContactName != "Address Book" ||
			recipient.Nickname != "nickname" ||
			!recipient.Blocked ||
			recipient.E164 != "+12345" ||
			recipient.Whitelisted == nil ||
			!*recipient.Whitelisted {
			t.Fatalf("recipient was clobbered: %+v", recipient)
		}
	})
}

func TestProfileSyncFailureWithoutCause(t *testing.T) {
	t.Parallel()
	dataDir := seedAccount(t)
	client := openOffline(t, dataDir)
	cli := signal.InitializeProfileClient(t.Context(), client)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	err := signal.ProfileSync(ctx, cli)
	if !errors.Is(err, signal.ErrSendFailed) {
		t.Fatalf("sync fallback=%v", err)
	}
}

func TestProfileRefreshCancellationLifecycle(t *testing.T) { //nolint:funlen // callback lifetime scenarios
	t.Parallel()
	t.Run("finished refresh unregisters callback", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		called := make(chan struct{}, 1)

		err := signal.RefreshProfileWithCancel(ctx, func() { called <- struct{}{} }, func() error { return nil })
		if err != nil {
			t.Fatal(err)
		}

		cancel()

		select {
		case <-called:
			t.Fatal("callback outlived refresh")
		case <-time.After(time.Millisecond):
		}
	})
	t.Run("waits for active callback", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		interrupted := make(chan struct{})

		var interruptOnce sync.Once

		release := make(chan struct{})

		returned := make(chan error, 1)
		go func() {
			returned <- signal.RefreshProfileWithCancel(ctx, func() {
				interruptOnce.Do(func() { close(interrupted) })
				<-release
			}, func() error {
				cancel()
				<-interrupted

				return io.ErrUnexpectedEOF
			})
		}()

		<-interrupted

		select {
		case <-returned:
			t.Fatal("returned while callback running")
		default:
		}

		close(release)

		err := <-returned
		if !errors.Is(err, context.Canceled) || !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Fatalf("refresh errors=%v", err)
		}
	})
}

func TestOwnProfileBypassesFailedDisplayCache(t *testing.T) { //nolint:cyclop,funlen // cache failure and bypass
	client, _ := openProfileOffline(t)
	cli := signal.InitializeProfileClient(t.Context(), client)
	old := validRawProfile(t)

	oldBody, err := json.Marshal(old)
	if err != nil {
		t.Fatal(err)
	}

	fresh := validRawProfile(t)
	name := make([]byte, 53)
	copy(name, "Fresh\x00Split Name")
	fresh.Name = independentProfileCipher(t, name)

	freshBody, err := json.Marshal(fresh)
	if err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		conn, acceptErr := websocket.Accept(writer, request, nil)
		if acceptErr != nil {
			t.Error(acceptErr)
			return
		}
		defer func() { _ = conn.CloseNow() }()

		for count := 0; ; count++ {
			var message signalpb.WebSocketMessage

			readErr := wspb.Read(request.Context(), conn, &message)
			if readErr != nil {
				return
			}

			status := uint32(http.StatusOK)
			if count > 0 {
				status = http.StatusInternalServerError
			}

			reply := &signalpb.WebSocketMessage{
				Type:     signalpb.WebSocketMessage_RESPONSE.Enum(),
				Response: &signalpb.WebSocketResponseMessage{Id: message.GetRequest().Id, Status: &status, Body: oldBody},
			}

			writeErr := wspb.Write(request.Context(), conn, reply)
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
		if request.Header.Get("Upgrade") == "websocket" {
			routed := request.Clone(request.Context())
			routed.URL.Scheme = localURL.Scheme
			routed.URL.Host = localURL.Host

			return http.DefaultTransport.RoundTrip(routed)
		}

		return profileHTTPResponse(http.StatusOK, string(freshBody)), nil
	})))

	cli.UnauthedWS = web.NewSignalWebsocket(nil)

	socketCtx, cancel := context.WithCancel(t.Context())
	defer cancel()

	// The pinned dependency assigns its captured incomingRequestChan to nil in
	// connectLoop's defer before joining the request handler. Synchronize these
	// two known log points so this cache test does not trigger that upstream race.
	handlerExited := make(chan struct{})
	socketLog := zerolog.New(io.Discard).Hook(zerolog.HookFunc(func(_ *zerolog.Event, _ zerolog.Level, message string) {
		switch message {
		case "ctx done, stopping request loop":
			close(handlerExited)
		case "ctx done, stopping connection loop":
			<-handlerExited
		case "Finished websocket cleanup":
			<-handlerExited
		}
	}))
	socketCtx = socketLog.WithContext(socketCtx)

	statuses := cli.UnauthedWS.Connect(socketCtx, nil)
	select {
	case status := <-statuses:
		if status.Event != web.SignalWebsocketConnectionEventConnected {
			t.Fatalf("connect=%+v", status)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("websocket connect timeout")
	}

	t.Cleanup(func() { cancel(); _ = cli.UnauthedWS.Close() })

	cached, err := cli.RetrieveProfileByID(t.Context(), uuid.MustParse(seededACI), 0)
	if err != nil || cached.Name != "Given Name Family Name" {
		t.Fatalf("seed cache=%+v %v", cached, err)
	}

	_, err = cli.RetrieveProfileByID(t.Context(), uuid.MustParse(seededACI), 0)
	if !errors.Is(err, signalmeow.ErrProfileInternalError) {
		t.Fatalf("forced cache failure=%v", err)
	}

	cached, err = cli.RetrieveProfileByID(t.Context(), uuid.MustParse(seededACI), time.Hour)
	if err != nil || cached.Name != "Given Name Family Name" {
		t.Fatalf("expected retained old display cache=%+v %v", cached, err)
	}

	profile, err := client.OwnProfile(t.Context())
	if err != nil || profile.GivenName != "Fresh" || profile.FamilyName != "Split Name" {
		t.Fatalf("facade used stale cache=%+v %v", profile, err)
	}
}

func TestProfileNotificationCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	entered := make(chan struct{})
	interrupted := make(chan struct{})

	var interruptOnce sync.Once

	finished := make(chan error, 1)
	go func() {
		finished <- signal.ProfileFollowUpWithCancel(ctx,
			func() { interruptOnce.Do(func() { close(interrupted) }) }, func() error {
				close(entered)
				<-interrupted

				return signal.ErrSendFailed
			})
	}()

	<-entered
	cancel()

	err := <-finished
	if !errors.Is(err, context.Canceled) || !errors.Is(err, signal.ErrSendFailed) {
		t.Fatalf("notification cancellation lost errors: %v", err)
	}
}

// pausedProfileErr models a goroutine preempted after reading a nil context error,
// immediately before pushOutgoing selects between cancellation and a ready writer.
type pausedProfileErr struct {
	context.Context //nolint:containedctx // implements a context decorator for a precise scheduling barrier

	checked chan struct{}
	release chan struct{}
	first   atomic.Bool
}

func (ctx *pausedProfileErr) Err() error {
	err := ctx.Context.Err()
	if ctx.first.CompareAndSwap(false, true) {
		close(ctx.checked)
		<-ctx.release
	}

	return err //nolint:wrapcheck // context.Err must preserve the exact context sentinel
}

//nolint:cyclop,funlen // complete local websocket lifetime
func newProfileCancellationSocket(
	t *testing.T, received chan<- string,
) (*web.SignalWebsocket, <-chan struct{}) {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		conn, err := websocket.Accept(writer, request, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer func() { _ = conn.CloseNow() }()

		for {
			var message signalpb.WebSocketMessage

			err = wspb.Read(request.Context(), conn, &message)
			if err != nil {
				return
			}

			received <- message.GetRequest().GetPath()
			// Deliberately withhold the response; cancellation must interrupt the read.
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
	socketCtx, cancelSocket := context.WithCancel(t.Context())
	handlerExited := make(chan struct{})
	// Isolate only the dependency's final captured-channel shutdown race. Forced
	// reconnects remain fully active while this socket context is still alive.
	socketLog := zerolog.New(io.Discard).Hook(zerolog.HookFunc(func(_ *zerolog.Event, _ zerolog.Level, message string) {
		switch message {
		case "ctx done, stopping request loop":
			close(handlerExited)
		case "ctx done, stopping connection loop":
			<-handlerExited
		case "Finished websocket cleanup":
			if socketCtx.Err() != nil {
				<-handlerExited
			}
		}
	}))
	socket := web.NewSignalWebsocket(nil)
	statuses := socket.Connect(socketLog.WithContext(socketCtx), nil)
	connected := make(chan struct{}, 64)

	drained := make(chan struct{})
	go func() {
		defer close(drained)

		for status := range statuses {
			if status.Event == web.SignalWebsocketConnectionEventConnected {
				connected <- struct{}{}
			}
		}
	}()

	t.Cleanup(func() { cancelSocket(); _ = socket.Close(); <-drained })
	waitProfileSocket(t, connected)

	return socket, connected
}

func waitProfileSocket(t *testing.T, connected <-chan struct{}) {
	t.Helper()

	select {
	case <-connected:
	case <-time.After(3 * time.Second):
		t.Fatal("websocket reconnect timeout")
	}
}

//nolint:cyclop,contextcheck,funlen // actual transport and an intentional context decorator
func TestProfileCancellationInterruptsLateWebsocketRequest(t *testing.T) {
	received := make(chan string, 64)
	socket, connected := newProfileCancellationSocket(t, received)
	completed := make(map[string]bool)

	for attempt := range 64 {
		ctx, cancel := context.WithCancel(t.Context())
		late := &pausedProfileErr{Context: ctx, checked: make(chan struct{}), release: make(chan struct{})}
		firstInterrupt := make(chan struct{}, 1)
		finished := make(chan error, 1)
		path := "/late-profile/" + strconv.Itoa(attempt)

		go func() {
			finished <- signal.ProfileFollowUpWithCancel(ctx, func() {
				socket.ForceReconnect()

				select {
				case firstInterrupt <- struct{}{}:
				default:
				}
			}, func() error {
				_, err := socket.SendRequest(late, http.MethodGet, path, nil, nil)
				return fmt.Errorf("late websocket request: %w", err)
			})
		}()

		<-late.checked
		cancel()
		<-firstInterrupt
		waitProfileSocket(t, connected)
		close(late.release)

		select {
		case err := <-finished:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation=%v", err)
			}
			// Completion can race server observation. Retain proof that any delayed
			// observation belongs to an attempt that has already returned canceled.
			completed[path] = true

			select {
			case actual := <-received:
				if !completed[actual] {
					t.Fatalf("observation without canceled completion: %q", actual)
				}

				return
			default:
				continue
			}
		case actual := <-received:
			if actual != path && !completed[actual] {
				t.Fatalf("received unexpected late path %q", actual)
			}
		}

		select {
		case err := <-finished:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("late request cancellation=%v", err)
			}

			return
		case <-time.After(time.Second):
			// Release the one-shot implementation before failing so no request is leaked.
			socket.ForceReconnect()
			<-finished
			t.Fatal("late websocket request survived the first cancellation interrupt")
		}
	}

	t.Fatal("did not observe the dependency's ready-writer selection")
}
