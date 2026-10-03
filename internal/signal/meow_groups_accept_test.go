//go:build cgo || libsignal_go

//nolint:funlen,lll,cyclop,goconst // Behavioral matrices cover independent stages and partial outcomes.
package signal_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/mautrix-signal/pkg/libsignalgo"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow/protobuf/signalpb"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow/types"
	"github.com/google/uuid"
	"github.com/rs/zerolog"
)

const acceptFreshTitle = "fresh acceptance title"

type acceptHarness struct {
	ops                                                 signal.GroupAcceptOperations
	calls                                               []string
	gid                                                 types.GroupIdentifier
	key                                                 types.SerializedGroupMasterKey
	before, after                                       *signalmeow.Group
	outcome                                             signalmeow.GroupInvitationAcceptOutcome
	resolveErr, fetchErr, patchErr, cacheErr, notifyErr error
	notification                                        *signalmeow.GroupMessageSendResult
	invited                                             libsignalgo.ServiceID
}

func newAcceptHarness(t *testing.T) *acceptHarness {
	t.Helper()

	key := types.SerializedGroupMasterKey(base64.StdEncoding.EncodeToString([]byte(strings.Repeat("K", 32))))

	gid, err := signal.GroupIDFromMasterKey([]byte(strings.Repeat("K", 32)))
	if err != nil {
		t.Fatal(err)
	}

	harness := &acceptHarness{
		gid: types.GroupIdentifier(gid), key: key,
		before:  &signalmeow.Group{GroupIdentifier: types.GroupIdentifier(gid), GroupMasterKey: key, Revision: 7, Title: "old title", PendingMembers: []*signalmeow.PendingMember{{ServiceID: libsignalgo.NewACIServiceID(uuid.MustParse(seededACI)), Role: signalmeow.GroupMember_ADMINISTRATOR}}},
		after:   &signalmeow.Group{GroupIdentifier: types.GroupIdentifier(gid), GroupMasterKey: key, Revision: 9, Title: acceptFreshTitle, Members: []*signalmeow.GroupMember{{ACI: uuid.MustParse(seededACI), Role: signalmeow.GroupMember_DEFAULT}}},
		outcome: signalmeow.GroupInvitationAcceptOutcome{Attempted: true, Accepted: true, Verified: true, Revision: 8, GroupContext: &signalpb.GroupContextV2{GroupChange: []byte("signed")}, Change: &signalmeow.GroupChange{Revision: 8}}, notification: &signalmeow.GroupMessageSendResult{},
	}

	harness.ops = signal.GroupAcceptOperations{
		Resolve: func(context.Context, string) (types.GroupIdentifier, types.SerializedGroupMasterKey, error) {
			harness.calls = append(harness.calls, "resolve acceptance key")

			return harness.gid, harness.key, harness.resolveErr
		},
		Invalidate: func(types.GroupIdentifier) { harness.calls = append(harness.calls, "invalidate") },
		Fetch: func(_ context.Context, key types.SerializedGroupMasterKey) (*signalmeow.Group, error) {
			if key != harness.key {
				t.Fatal("wrong fetch key")
			}

			harness.calls = append(harness.calls, "fetch")

			if harness.fetchErr != nil {
				return nil, harness.fetchErr
			}

			if harness.invited.UUID == uuid.Nil {
				return harness.before, nil
			}

			return harness.after, nil
		},
		Accept: func(_ context.Context, key types.SerializedGroupMasterKey, revision uint32, invited libsignalgo.ServiceID) (signalmeow.GroupInvitationAcceptOutcome, error) {
			if key != harness.key || revision != 7 {
				t.Fatal("wrong acceptance input")
			}

			harness.calls = append(harness.calls, "patch")

			harness.invited = invited

			return harness.outcome, harness.patchErr
		},
		Cache: func(_ context.Context, group signal.Group) error {
			harness.calls = append(harness.calls, "cache acceptance membership")

			if group.Title != acceptFreshTitle || !group.LeftAt.IsZero() {
				t.Fatal("unverified cache")
			}

			return harness.cacheErr
		},
		Notify: func(_ context.Context, raw *signalmeow.Group, gctx *signalpb.GroupContextV2, change *signalmeow.GroupChange) (*signalmeow.GroupMessageSendResult, error) {
			harness.calls = append(harness.calls, "notify")

			if raw != harness.after || !bytes.Equal(gctx.GetGroupChange(), []byte("signed")) || change.Revision != 8 {
				t.Fatal("notification evidence mismatch")
			}

			return harness.notification, harness.notifyErr
		},
	}

	return harness
}

//nolint:wrapcheck // Adapter preserves the tested partial outcome and identity.
func (harness *acceptHarness) run(ctx context.Context) (signal.GroupAcceptResult, error) {
	return signal.AcceptGroupWithOperations(ctx, harness.ops, signal.Recipient{ACI: seededACI, PNI: bobACI}, string(harness.gid))
}

func TestGroupAcceptStages(t *testing.T) {
	t.Parallel()

	harness := newAcceptHarness(t)

	got, err := harness.run(t.Context())

	if err != nil || !got.Accepted || !got.Changed || !got.Verified || got.ID != string(harness.gid) || got.Revision != 9 || got.Title != acceptFreshTitle {
		t.Fatal(got, err)
	}

	want := []string{"resolve acceptance key", "invalidate", "fetch", "patch", "invalidate", "fetch", "cache acceptance membership", "notify", "invalidate"}

	if !reflect.DeepEqual(harness.calls, want) {
		t.Fatal(harness.calls)
	}

	if harness.invited != libsignalgo.NewACIServiceID(uuid.MustParse(seededACI)) {
		t.Fatal("wrong invited identity")
	}
}

func TestGroupAcceptNoop(t *testing.T) {
	t.Parallel()

	harness := newAcceptHarness(t)

	harness.before = harness.after

	harness.cacheErr = io.ErrClosedPipe

	got, err := harness.run(t.Context())

	if !errors.Is(err, io.ErrClosedPipe) || !got.Verified || got.Accepted || got.Changed || got.Revision != 9 || got.Title != acceptFreshTitle {
		t.Fatal(got, err)
	}

	if !reflect.DeepEqual(harness.calls, []string{"resolve acceptance key", "invalidate", "fetch", "cache acceptance membership", "invalidate"}) {
		t.Fatal(harness.calls)
	}
}

func TestGroupAcceptFailures(t *testing.T) {
	t.Parallel()

	secret := &url.Error{Op: http.MethodPatch, URL: "https://acceptance-private-material/key", Err: io.ErrClosedPipe}

	tests := []struct {
		name               string
		setup              func(*acceptHarness)
		want               error
		accepted, verified bool
		patches, fetches   int
	}{
		{"no invitation", func(harness *acceptHarness) { harness.before.PendingMembers = nil }, signal.ErrGroupInvitationNotFound, false, false, 0, 1},
		{"read auth", func(harness *acceptHarness) { harness.fetchErr = signalmeow.AuthorizationFailedError }, signal.ErrNotAMember, false, false, 0, 1},
		{"read unknown", func(harness *acceptHarness) { harness.fetchErr = signalmeow.NotFoundError }, signal.ErrUnknownGroup, false, false, 0, 1},
		{"acceptance terminated", func(harness *acceptHarness) { harness.fetchErr = signalmeow.ErrGroupAcceptanceTerminated }, signal.ErrGroupTerminated, false, false, 0, 1},
		{"acceptance conflict", func(harness *acceptHarness) {
			harness.patchErr = signalmeow.ConflictError

			harness.outcome.Accepted = false
		}, signal.ErrGroupChanged, false, false, 1, 1},
		{"acceptance uncertainty", func(harness *acceptHarness) {
			harness.patchErr = errors.Join(signalmeow.ErrGroupAcceptanceUncertain, secret)

			harness.outcome.Accepted = false
		}, signal.ErrGroupUpdateUncertain, false, false, 1, 1},
		{"accepted invalid", func(harness *acceptHarness) {
			harness.patchErr = errors.Join(signalmeow.ErrGroupAcceptanceInvalid, secret)

			harness.outcome.Verified = false
		}, io.ErrClosedPipe, true, false, 1, 1},
		{"unsigned", func(harness *acceptHarness) { harness.outcome.Verified = false }, nil, true, false, 1, 1},
		{"nil context", func(harness *acceptHarness) { harness.outcome.GroupContext = nil }, nil, true, false, 1, 1},
		{"nil change", func(harness *acceptHarness) { harness.outcome.Change = nil }, nil, true, false, 1, 1},
		{"removed concurrently", func(harness *acceptHarness) { harness.after.Members = nil }, signal.ErrNotAMember, true, false, 1, 2},
		{"stale fresh revision", func(harness *acceptHarness) { harness.after.Revision = 7 }, nil, true, false, 1, 2},
		{"cache acceptance membership", func(harness *acceptHarness) { harness.cacheErr = secret }, io.ErrClosedPipe, true, true, 1, 2},
		{"notify", func(harness *acceptHarness) { harness.notifyErr = secret }, io.ErrClosedPipe, true, true, 1, 2},
		{"recipient", func(harness *acceptHarness) {
			harness.notification.FailedToSendTo = []signalmeow.FailedSendResult{{Error: secret}}
		}, io.ErrClosedPipe, true, true, 1, 2},
		{"nil notification", func(harness *acceptHarness) { harness.notification = nil }, nil, true, true, 1, 2},
		{"nil pending record", func(harness *acceptHarness) {
			harness.before.PendingMembers = append(harness.before.PendingMembers, nil)
		}, nil, false, false, 0, 1},
		{"nil member record", func(harness *acceptHarness) { harness.before.Members = []*signalmeow.GroupMember{nil} }, nil, false, false, 0, 1},
		{"nil requester record", func(harness *acceptHarness) { harness.before.RequestingMembers = []*signalmeow.RequestingMember{nil} }, nil, false, false, 0, 1},
		{"nil banned record", func(harness *acceptHarness) { harness.before.BannedMembers = []*signalmeow.BannedMember{nil} }, nil, false, false, 0, 1},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			harness := newAcceptHarness(t)

			testCase.setup(harness)

			got, err := harness.run(t.Context())

			if err == nil || (testCase.want != nil && !errors.Is(err, testCase.want)) || got.Accepted != testCase.accepted || got.Changed != testCase.accepted || got.Verified != testCase.verified || strings.Contains(err.Error(), acceptSecretMarker) {
				t.Fatal(got, err)
			}

			patches, fetches := 0, 0

			for _, call := range harness.calls {
				if call == "patch" {
					patches++
				}

				if call == "fetch" {
					fetches++
				}
			}

			if patches != testCase.patches || fetches != testCase.fetches || harness.calls[len(harness.calls)-1] != "invalidate" {
				t.Fatal(harness.calls)
			}

			if patches > 0 && !testCase.verified && got.Revision != 8 {
				t.Fatal("lost attempted revision", got)
			}

			if testCase.verified && (got.Title != acceptFreshTitle || got.Revision != 9) {
				t.Fatal("verification lost", got)
			}
		})
	}
}

func TestGroupAcceptStoredKeyBinding(t *testing.T) {
	t.Parallel()

	for _, kind := range []string{"unknown acceptance group", "invalid acceptance master key", "wrong derived ID", "snapshot ID", "snapshot key"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()

			harness := newAcceptHarness(t)

			switch kind {
			case "unknown acceptance group":
				harness.resolveErr = signal.ErrUnknownGroup

			case "invalid acceptance master key":
				harness.key = acceptSecretMarker

			case "wrong derived ID":
				harness.gid = types.GroupIdentifier(base64.StdEncoding.EncodeToString(make([]byte, 32)))

			case "snapshot ID":
				harness.before.GroupIdentifier = acceptSecretMarker

			case "snapshot key":
				harness.before.GroupMasterKey = acceptSecretMarker
			}

			got, err := harness.run(t.Context())

			if err == nil || got.Accepted || strings.Contains(err.Error(), acceptSecretMarker) {
				t.Fatal(got, err)
			}

			if kind == "unknown acceptance group" || kind == "invalid acceptance master key" || kind == "wrong derived ID" {
				if got.ID != "" || len(harness.calls) != 1 {
					t.Fatal("resolved invalid key reached HTTP", got, harness.calls)
				}
			} else {
				if len(harness.calls) != 4 {
					t.Fatal(harness.calls)
				}
			}
		})
	}
}

//nolint:gocognit // Stage-specific cancellation and evidence assertions use independent fixtures.
func TestGroupAcceptCanceledFollowUps(t *testing.T) {
	t.Parallel()

	for _, stage := range []string{"resolve acceptance key", "first acceptance fetch", "patch", "fresh fetch", "cache acceptance membership"} {
		t.Run(stage, func(t *testing.T) {
			t.Parallel()

			harness := newAcceptHarness(t)

			ctx, cancel := context.WithCancel(t.Context())

			defer cancel()

			switch stage {
			case "resolve acceptance key":
				original := harness.ops.Resolve
				harness.ops.Resolve = func(ctx context.Context, ref string) (types.GroupIdentifier, types.SerializedGroupMasterKey, error) {
					gid, key, err := original(ctx, ref)

					cancel()

					return gid, key, err
				}

			case "first acceptance fetch", "fresh fetch":
				original := harness.ops.Fetch
				harness.ops.Fetch = func(ctx context.Context, key types.SerializedGroupMasterKey) (*signalmeow.Group, error) {
					raw, err := original(ctx, key)

					if stage == "first acceptance fetch" || harness.invited.UUID != uuid.Nil {
						cancel()
					}

					return raw, err
				}

			case "patch":
				original := harness.ops.Accept
				harness.ops.Accept = func(ctx context.Context, key types.SerializedGroupMasterKey, rev uint32, id libsignalgo.ServiceID) (signalmeow.GroupInvitationAcceptOutcome, error) {
					out, err := original(ctx, key, rev, id)

					cancel()

					return out, err
				}

			case "cache acceptance membership":
				original := harness.ops.Cache

				harness.ops.Cache = func(ctx context.Context, g signal.Group) error { err := original(ctx, g); cancel(); return err }
			}

			got, err := harness.run(ctx)

			if !errors.Is(err, context.Canceled) {
				t.Fatal(got, err)
			}

			if stage == "fresh fetch" {
				if !got.Accepted || !got.Changed || !got.Verified || got.ID != string(harness.gid) || got.Title != acceptFreshTitle || got.Revision != 9 {
					t.Fatalf("confirmed membership lost after cancellation: %+v, %v", got, err)
				}

				if !reflect.DeepEqual(harness.calls, []string{"resolve acceptance key", "invalidate", "fetch", "patch", "invalidate", "fetch", "invalidate"}) {
					t.Fatal("canceled fresh verification started follow-ups", harness.calls)
				}
			}

			for _, call := range harness.calls {
				if call == "notify" {
					t.Fatal("notify after cancellation")
				}

				if stage == "first acceptance fetch" && call == "patch" {
					t.Fatal("patch after cancellation")
				}
			}
		})
	}
}

func TestGroupAcceptLifecycle(t *testing.T) {
	t.Parallel()

	ref := base64.StdEncoding.EncodeToString(make([]byte, 32))

	cli, err := signal.Open(t.Context(), signal.Options{DataDir: seedAccount(t)})
	if err != nil {
		t.Fatal(err)
	}

	_, err = cli.AcceptGroupInvitation(t.Context(), ref)

	if !errors.Is(err, signal.ErrNotConnected) {
		t.Fatal(err)
	}

	err = cli.Close()
	if err != nil {
		t.Fatal(err)
	}

	_, err = cli.AcceptGroupInvitation(t.Context(), ref)

	if !errors.Is(err, signal.ErrClosed) {
		t.Fatal(err)
	}

	cli = openOffline(t, seedAccount(t), signal.SendOnly())

	ctx, cancel := context.WithCancel(t.Context())

	cancel()

	_, err = cli.AcceptGroupInvitation(ctx, ref)

	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}

	signal.LoseConnection(cli)

	_, err = cli.AcceptGroupInvitation(t.Context(), ref)

	if !errors.Is(err, signal.ErrDeviceUnlinked) {
		t.Fatal(err)
	}
}

func TestGroupAcceptSensitiveContext(t *testing.T) {
	t.Parallel()

	harness := newAcceptHarness(t)

	var logs bytes.Buffer

	logger := zerolog.New(&logs).Level(zerolog.TraceLevel)

	emit := func(ctx context.Context) {
		zerolog.Ctx(ctx).Error().Str("credential", acceptSecretMarker).Msg("sensitive")
	}

	resolve := harness.ops.Resolve

	harness.ops.Resolve = func(ctx context.Context, ref string) (types.GroupIdentifier, types.SerializedGroupMasterKey, error) {
		emit(ctx)

		return resolve(ctx, ref)
	}

	fetch := harness.ops.Fetch

	harness.ops.Fetch = func(ctx context.Context, key types.SerializedGroupMasterKey) (*signalmeow.Group, error) {
		emit(ctx)

		return fetch(ctx, key)
	}

	accept := harness.ops.Accept

	harness.ops.Accept = func(ctx context.Context, key types.SerializedGroupMasterKey, rev uint32, id libsignalgo.ServiceID) (signalmeow.GroupInvitationAcceptOutcome, error) {
		emit(ctx)

		return accept(ctx, key, rev, id)
	}

	cache := harness.ops.Cache

	harness.ops.Cache = func(ctx context.Context, g signal.Group) error { emit(ctx); return cache(ctx, g) }

	notify := harness.ops.Notify

	harness.ops.Notify = func(ctx context.Context, g *signalmeow.Group, gctx *signalpb.GroupContextV2, change *signalmeow.GroupChange) (*signalmeow.GroupMessageSendResult, error) {
		emit(ctx)

		return notify(ctx, g, gctx, change)
	}

	_, err := harness.run(logger.WithContext(t.Context()))

	if err != nil || logs.Len() != 0 {
		t.Fatal(err, logs.String())
	}
}

func TestGroupAcceptPNIFallback(t *testing.T) {
	t.Parallel()

	harness := newAcceptHarness(t)

	pni := libsignalgo.NewPNIServiceID(uuid.MustParse(bobACI))

	harness.before.PendingMembers[0].ServiceID = pni

	got, err := harness.run(t.Context())

	if err != nil || !got.Verified || harness.invited != pni {
		t.Fatal(got, err, harness.invited)
	}
}

func TestGroupAcceptSignedArtifactOwnership(t *testing.T) {
	t.Parallel()

	harness := newAcceptHarness(t)

	original := &signalmeow.PromotePendingMember{ACI: uuid.MustParse(seededACI)}

	harness.outcome.Change.PromotePendingMembers = []*signalmeow.PromotePendingMember{original}

	notify := harness.ops.Notify

	harness.ops.Notify = func(ctx context.Context, g *signalmeow.Group, gctx *signalpb.GroupContextV2, change *signalmeow.GroupChange) (*signalmeow.GroupMessageSendResult, error) {
		result, err := notify(ctx, g, gctx, change)

		gctx.GroupChange[0] = 'X'

		change.PromotePendingMembers[0].ACI = uuid.Nil

		return result, err
	}

	_, err := harness.run(t.Context())

	if err != nil || harness.outcome.GroupContext.GetGroupChange()[0] != 's' || original.ACI == uuid.Nil {
		t.Fatal("signed evidence not owned", err)
	}
}

type blockedAcceptanceStore struct {
	entered chan struct{}
	release chan struct{}
}

func (s *blockedAcceptanceStore) MasterKeyFromGroupIdentifier(context.Context, types.GroupIdentifier) (types.SerializedGroupMasterKey, error) {
	close(s.entered)

	<-s.release

	return "", io.ErrClosedPipe
}

func (s *blockedAcceptanceStore) StoreMasterKey(context.Context, types.GroupIdentifier, types.SerializedGroupMasterKey) error {
	return io.ErrClosedPipe
}

func TestGroupAcceptLifecycleCloseWaitsForResolution(t *testing.T) {
	t.Parallel()

	cli := openOffline(t, seedAccount(t), signal.SendOnly())

	blocked := &blockedAcceptanceStore{entered: make(chan struct{}), release: make(chan struct{})}

	restore := signal.InstallAcceptanceGroupStore(cli, blocked)

	defer restore()

	accepted := make(chan error, 1)

	go func() {
		_, err := cli.AcceptGroupInvitation(t.Context(), base64.StdEncoding.EncodeToString(make([]byte, 32)))

		accepted <- err
	}()

	<-blocked.entered

	closed := make(chan error, 1)

	go func() { closed <- cli.Close() }()

	deadline := time.NewTimer(time.Second)

	defer deadline.Stop()

	for !signal.GroupAcceptClosing(cli) {
		select {
		case <-deadline.C:
			t.Fatal("Close did not begin")

		default:
			runtime.Gosched()
		}
	}

	select {
	case err := <-closed:
		t.Fatalf("Close released store during key resolution: %v", err)

	default:
	}

	close(blocked.release)

	err := <-accepted

	if !errors.Is(err, io.ErrClosedPipe) {
		t.Fatal(err)
	}

	err = <-closed
	if err != nil {
		t.Fatal(err)
	}
}

func TestGroupAcceptCanceledNoopEvidence(t *testing.T) {
	t.Parallel()
	harness := newAcceptHarness(t)
	harness.before = harness.after

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	fetch := harness.ops.Fetch
	harness.ops.Fetch = func(ctx context.Context, key types.SerializedGroupMasterKey) (*signalmeow.Group, error) {
		raw, err := fetch(ctx, key)

		cancel()

		return raw, err
	}

	got, err := harness.run(ctx)
	if !errors.Is(err, context.Canceled) || got.Accepted || got.Changed || !got.Verified || got.ID != string(harness.gid) || got.Title != acceptFreshTitle || got.Revision != 9 {
		t.Fatalf("confirmed no-op lost after cancellation: %+v, %v", got, err)
	}

	if !reflect.DeepEqual(harness.calls, []string{"resolve acceptance key", "invalidate", "fetch", "invalidate"}) {
		t.Fatal("canceled no-op started follow-ups", harness.calls)
	}
}
