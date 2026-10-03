//go:build cgo || libsignal_go

//nolint:funlen,gocognit,goconst,lll,cyclop // Explicit stage matrices preserve partial outcomes and side-effect checks.
package signal_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"math"
	"net/http"
	"net/url"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow/protobuf/signalpb"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow/types"
	"github.com/rs/zerolog"
	"google.golang.org/protobuf/proto"
)

type cancelRequestHarness struct {
	ops                              signal.GroupCancelRequestOperations
	gid                              types.GroupIdentifier
	key                              types.SerializedGroupMasterKey
	preview                          signalmeow.GroupJoinPreview
	outcome                          signalmeow.GroupJoinRequestCancelOutcome
	calls                            []string
	resolveErr, previewErr, patchErr error
}

func newCancelRequestHarness(t *testing.T) *cancelRequestHarness {
	t.Helper()

	key := types.SerializedGroupMasterKey(base64.StdEncoding.EncodeToString([]byte(strings.Repeat("K", 32))))

	gid, err := signal.GroupIDFromMasterKey([]byte(strings.Repeat("K", 32)))
	if err != nil {
		t.Fatal(err)
	}

	harness := &cancelRequestHarness{
		gid: types.GroupIdentifier(gid), key: key,
		preview: signalmeow.GroupJoinPreview{Title: "preview title", Revision: 7, PendingAdminApproval: true},
		outcome: signalmeow.GroupJoinRequestCancelOutcome{Attempted: true, Accepted: true, Verified: true, Revision: 8, GroupContext: &signalpb.GroupContextV2{Revision: proto.Uint32(8), GroupChange: []byte("signed")}, Change: &signalmeow.GroupChange{Revision: 8}},
	}
	harness.ops = signal.GroupCancelRequestOperations{
		Resolve: func(_ context.Context, ref string) (types.GroupIdentifier, types.SerializedGroupMasterKey, error) {
			harness.calls = append(harness.calls, "resolve cancellation key")
			if ref != string(harness.gid) {
				t.Fatal("wrong resolution reference", ref)
			}

			return harness.gid, harness.key, harness.resolveErr
		},
		Invalidate: func(gid types.GroupIdentifier) {
			if gid != harness.gid {
				t.Fatal("wrong invalidation")
			}

			harness.calls = append(harness.calls, "invalidate")
		},
		Preview: func(_ context.Context, key types.SerializedGroupMasterKey) (signalmeow.GroupJoinPreview, error) {
			harness.calls = append(harness.calls, "preview")
			if key != harness.key {
				t.Fatal("wrong preview key")
			}

			return harness.preview, harness.previewErr
		},
		Cancel: func(_ context.Context, key types.SerializedGroupMasterKey, rev uint32) (signalmeow.GroupJoinRequestCancelOutcome, error) {
			harness.calls = append(harness.calls, "patch")
			if key != harness.key || rev != harness.preview.Revision {
				t.Fatal("wrong patch key/revision")
			}

			return harness.outcome, harness.patchErr
		},
	}

	return harness
}

//nolint:wrapcheck // Test adapter intentionally preserves error identities.
func (harness *cancelRequestHarness) run(ctx context.Context) (signal.GroupCancelRequestResult, error) {
	return signal.CancelGroupRequestWithOperations(ctx, harness.ops, string(harness.gid))
}

func TestGroupCancelRequestSingleAttempt(t *testing.T) {
	t.Parallel()
	harness := newCancelRequestHarness(t)
	got, err := harness.run(t.Context())

	want := signal.GroupCancelRequestResult{ID: string(harness.gid), Title: "preview title", Revision: 8, Changed: true, Accepted: true, Verified: true}
	if err != nil || got != want {
		t.Fatal(got, err)
	}

	if !reflect.DeepEqual(harness.calls, []string{"resolve cancellation key", "invalidate", "preview", "patch", "invalidate"}) {
		t.Fatal("unexpected postread or retry", harness.calls)
	}
}

func TestGroupCancelRequestNoop(t *testing.T) {
	t.Parallel()
	harness := newCancelRequestHarness(t)
	harness.preview.PendingAdminApproval = false
	harness.preview.Revision = math.MaxUint32
	harness.patchErr = io.ErrClosedPipe

	got, err := harness.run(t.Context())
	if err != nil || got.Accepted || got.Changed || !got.Verified || got.Title != "preview title" || got.Revision != math.MaxUint32 {
		t.Fatal(got, err)
	}

	if !reflect.DeepEqual(harness.calls, []string{"resolve cancellation key", "invalidate", "preview", "invalidate"}) {
		t.Fatal(harness.calls)
	}
}

func TestGroupCancelRequestStoredKeyBinding(t *testing.T) {
	t.Parallel()

	for _, kind := range []string{"unknown cancellation group", "bad key", "mismatched ID"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			harness := newCancelRequestHarness(t)

			switch kind {
			case "unknown cancellation group":
				harness.resolveErr = signal.ErrUnknownGroup
			case "bad key":
				harness.key = "private-secret"
			case "mismatched ID":
				harness.key = types.SerializedGroupMasterKey(base64.StdEncoding.EncodeToString(make([]byte, 32)))
			}

			got, err := harness.run(t.Context())
			if err == nil || got != (signal.GroupCancelRequestResult{}) || len(harness.calls) != 1 || strings.Contains(err.Error(), "private-secret") {
				t.Fatal(got, err, harness.calls)
			}
		})
	}
}

func TestGroupCancelRequestPartialResults(t *testing.T) {
	t.Parallel()

	secret := &url.Error{Op: http.MethodPatch, URL: "https://private-secret/key", Err: io.ErrClosedPipe}

	tests := []struct {
		name     string
		setup    func(*cancelRequestHarness)
		cause    error
		accepted bool
		revision uint32
		patches  int
	}{
		{"403 is error", func(harness *cancelRequestHarness) { harness.previewErr = signalmeow.AuthorizationFailedError }, signal.ErrNotAMember, false, 0, 0},
		{"404 is error", func(harness *cancelRequestHarness) { harness.previewErr = signalmeow.NotFoundError }, signal.ErrUnknownGroup, false, 0, 0},
		{"cancellation terminated", func(harness *cancelRequestHarness) { harness.previewErr = signalmeow.ErrGroupCancellationTerminated }, signal.ErrGroupTerminated, false, 0, 0},
		{"bad preview", func(harness *cancelRequestHarness) {
			harness.previewErr = errors.Join(signalmeow.ErrGroupCancellationInvalid, secret)
		}, io.ErrClosedPipe, false, 0, 0},
		{"cancellation revision overflow", func(harness *cancelRequestHarness) { harness.preview.Revision = math.MaxUint32 }, nil, false, math.MaxUint32, 0},
		{"cancellation conflict", func(harness *cancelRequestHarness) {
			harness.patchErr = signalmeow.ConflictError
			harness.outcome.Accepted = false
			harness.outcome.Verified = false
		}, signal.ErrGroupChanged, false, 8, 1},
		{"uncertain cancellation", func(harness *cancelRequestHarness) {
			harness.patchErr = errors.Join(signalmeow.ErrGroupCancellationUncertain, secret)
			harness.outcome.Accepted = false
			harness.outcome.Verified = false
		}, signal.ErrGroupUpdateUncertain, false, 8, 1},
		{"accepted invalid", func(harness *cancelRequestHarness) {
			harness.patchErr = errors.Join(signalmeow.ErrGroupCancellationInvalid, secret)
			harness.outcome.Verified = false
		}, io.ErrClosedPipe, true, 8, 1},
		{"unsigned", func(harness *cancelRequestHarness) { harness.outcome.Verified = false }, nil, true, 8, 1},
		{"nil context", func(harness *cancelRequestHarness) { harness.outcome.GroupContext = nil }, nil, true, 8, 1},
		{"nil change", func(harness *cancelRequestHarness) { harness.outcome.Change = nil }, nil, true, 8, 1},
		{"wrong signed revision", func(harness *cancelRequestHarness) { harness.outcome.Revision = 9 }, nil, true, 9, 1},
		{"not accepted", func(harness *cancelRequestHarness) { harness.outcome.Accepted = false }, nil, false, 8, 1},
		{"not attempted", func(harness *cancelRequestHarness) {
			harness.outcome = signalmeow.GroupJoinRequestCancelOutcome{}
			harness.patchErr = secret
		}, io.ErrClosedPipe, false, 7, 1},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			harness := newCancelRequestHarness(t)
			testCase.setup(harness)

			got, err := harness.run(t.Context())
			if err == nil || (testCase.cause != nil && !errors.Is(err, testCase.cause)) || got.Accepted != testCase.accepted || got.Changed != testCase.accepted || got.Verified || got.Revision != testCase.revision || strings.Contains(err.Error(), "private-secret") {
				t.Fatal(got, err)
			}

			patches := 0

			for _, call := range harness.calls {
				if call == "patch" {
					patches++
				}
			}

			if patches != testCase.patches || harness.calls[len(harness.calls)-1] != "invalidate" {
				t.Fatal(harness.calls)
			}

			if testCase.patches > 0 && got.Title != "preview title" {
				t.Fatal("lost preview title", got)
			}
		})
	}
}

func TestGroupCancelRequestCanceledBoundaries(t *testing.T) {
	t.Parallel()

	for _, stage := range []string{"resolve cancellation key", "preview", "noop", "patch", "lost preview"} {
		t.Run(stage, func(t *testing.T) {
			t.Parallel()
			harness := newCancelRequestHarness(t)

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()

			lost := false
			harness.ops.Check = func() error {
				if lost {
					return signal.ErrDeviceUnlinked
				}

				return nil
			}

			switch stage {
			case "resolve cancellation key":
				original := harness.ops.Resolve
				harness.ops.Resolve = func(ctx context.Context, ref string) (types.GroupIdentifier, types.SerializedGroupMasterKey, error) {
					gid, key, err := original(ctx, ref)

					cancel()

					return gid, key, err
				}
			case "preview", "noop", "lost preview":
				if stage == "noop" {
					harness.preview.PendingAdminApproval = false
				}

				original := harness.ops.Preview
				harness.ops.Preview = func(ctx context.Context, key types.SerializedGroupMasterKey) (signalmeow.GroupJoinPreview, error) {
					p, err := original(ctx, key)

					if stage == "lost preview" {
						lost = true
					} else {
						cancel()
					}

					return p, err
				}
			case "patch":
				original := harness.ops.Cancel
				harness.ops.Cancel = func(ctx context.Context, key types.SerializedGroupMasterKey, rev uint32) (signalmeow.GroupJoinRequestCancelOutcome, error) {
					out, err := original(ctx, key, rev)

					cancel()

					return out, err
				}
			}

			got, err := harness.run(ctx)

			want := context.Canceled
			if stage == "lost preview" {
				want = signal.ErrDeviceUnlinked
			}

			if !errors.Is(err, want) || got.Accepted != (stage == "patch") || got.Verified != (stage == "patch" || stage == "noop") {
				t.Fatal(got, err)
			}

			if stage != "patch" {
				for _, call := range harness.calls {
					if call == "patch" {
						t.Fatal("patch after canceled/lost", harness.calls)
					}
				}
			}

			if harness.calls[len(harness.calls)-1] != "invalidate" {
				t.Fatal(harness.calls)
			}
		})
	}
}

func TestGroupCancelRequestSensitiveContext(t *testing.T) {
	t.Parallel()
	harness := newCancelRequestHarness(t)

	var logs bytes.Buffer

	emit := func(ctx context.Context) {
		zerolog.Ctx(ctx).Error().Str("credential", "private-secret").Msg("sensitive")
	}
	resolve := harness.ops.Resolve
	harness.ops.Resolve = func(ctx context.Context, ref string) (types.GroupIdentifier, types.SerializedGroupMasterKey, error) {
		emit(ctx)
		return resolve(ctx, ref)
	}
	preview := harness.ops.Preview
	harness.ops.Preview = func(ctx context.Context, key types.SerializedGroupMasterKey) (signalmeow.GroupJoinPreview, error) {
		emit(ctx)
		return preview(ctx, key)
	}
	patch := harness.ops.Cancel
	harness.ops.Cancel = func(ctx context.Context, key types.SerializedGroupMasterKey, rev uint32) (signalmeow.GroupJoinRequestCancelOutcome, error) {
		emit(ctx)
		return patch(ctx, key, rev)
	}
	logger := zerolog.New(&logs).Level(zerolog.TraceLevel)

	_, err := harness.run(logger.WithContext(t.Context()))
	if err != nil || logs.Len() != 0 {
		t.Fatal(err, logs.String())
	}
}

func TestGroupCancelRequestLifecycle(t *testing.T) {
	t.Parallel()

	for _, stage := range []string{"not connected", "closed", "caller canceled", "connection lost"} {
		t.Run(stage, func(t *testing.T) {
			t.Parallel()
			ctx := t.Context()

			var (
				cli signal.Client
				err error
			)

			switch stage {
			case "not connected", "closed":
				cli, err = signal.Open(ctx, signal.Options{DataDir: seedAccount(t)})
				if err != nil {
					t.Fatal(err)
				}

				t.Cleanup(func() {
					closeErr := cli.Close()
					if closeErr != nil {
						t.Error(closeErr)
					}
				})

				if stage == "closed" {
					err = cli.Close()
					if err != nil {
						t.Fatal(err)
					}
				}
			default:
				cli = openOffline(t, seedAccount(t), signal.SendOnly())
			}

			expected := signal.ErrNotConnected

			switch stage {
			case "closed":
				expected = signal.ErrClosed
			case "caller canceled":
				var cancel context.CancelFunc

				ctx, cancel = context.WithCancel(ctx)
				cancel()

				expected = context.Canceled
			case "connection lost":
				signal.LoseConnection(cli)

				expected = signal.ErrDeviceUnlinked
			}

			result, err := cli.CancelGroupJoinRequest(ctx, base64.StdEncoding.EncodeToString(make([]byte, 32)))
			if !errors.Is(err, expected) || result != (signal.GroupCancelRequestResult{}) {
				t.Fatal(result, err)
			}
		})
	}
}

func waitForCancellationClose(t *testing.T, cli signal.Client) {
	t.Helper()

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
}

func TestGroupCancelRequestLifecycleCloseWaitsForResolution(t *testing.T) {
	t.Parallel()
	cli := openOffline(t, seedAccount(t), signal.SendOnly())
	blocked := &blockedAcceptanceStore{entered: make(chan struct{}), release: make(chan struct{})}

	restore := signal.InstallAcceptanceGroupStore(cli, blocked)
	defer restore()

	finished := make(chan error, 1)
	go func() {
		_, err := cli.CancelGroupJoinRequest(t.Context(), base64.StdEncoding.EncodeToString(make([]byte, 32)))
		finished <- err
	}()

	<-blocked.entered

	closed := make(chan error, 1)
	go func() { closed <- cli.Close() }()

	waitForCancellationClose(t, cli)

	select {
	case err := <-closed:
		t.Fatal("Close released store during resolution", err)
	default:
	}

	close(blocked.release)

	err := <-finished
	if !errors.Is(err, io.ErrClosedPipe) {
		t.Fatal(err)
	}

	err = <-closed
	if err != nil {
		t.Fatal(err)
	}
}
