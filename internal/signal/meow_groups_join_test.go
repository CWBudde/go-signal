//go:build cgo || libsignal_go

//nolint:cyclop,gocognit,goconst,lll // Behavioral matrices keep independent fixtures and assertions together.
package signal_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"math"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/mautrix-signal/pkg/libsignalgo"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow/protobuf/signalpb"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow/types"
	"github.com/google/uuid"
	"github.com/rs/zerolog"
)

// Removing freshness, persistence ordering, response gating or error redaction breaks these tests.
type joinHarness struct {
	ops                                                                       signal.GroupJoinOperations
	calls                                                                     []string
	known                                                                     bool
	knownErr, persistErr, fetchErr, cacheErr, previewErr, patchErr, notifyErr error
	preview                                                                   signalmeow.GroupJoinPreview
	outcome                                                                   signalmeow.GroupJoinOutcome
	raw                                                                       *signalmeow.Group
	notifications                                                             *signalmeow.GroupMessageSendResult
	notifiedContext                                                           *signalpb.GroupContextV2
	notifiedChange                                                            *signalmeow.GroupChange
}

func newJoinHarness(t *testing.T) *joinHarness {
	t.Helper()

	groupID, err := signal.GroupIDFromMasterKey([]byte(strings.Repeat("K", 32)))
	if err != nil {
		t.Fatal(err)
	}

	harness := &joinHarness{
		preview:       signalmeow.GroupJoinPreview{Revision: 7, Access: signalmeow.AccessControl_ANY, Title: "preview title"},
		outcome:       signalmeow.GroupJoinOutcome{Attempted: true, Accepted: true, Verified: true, Revision: 8, GroupContext: &signalpb.GroupContextV2{}, Change: &signalmeow.GroupChange{}},
		raw:           &signalmeow.Group{GroupIdentifier: types.GroupIdentifier(groupID), Revision: 9, Title: "fresh title", Members: []*signalmeow.GroupMember{{ACI: uuid.MustParse(seededACI), Role: signalmeow.GroupMember_DEFAULT}}},
		notifications: &signalmeow.GroupMessageSendResult{},
	}

	harness.ops = signal.GroupJoinOperations{
		Known: func(context.Context, types.GroupIdentifier) (bool, error) {
			harness.calls = append(harness.calls, "known")

			return harness.known, harness.knownErr
		},
		Persist: func(context.Context, types.GroupIdentifier, types.SerializedGroupMasterKey) error {
			harness.calls = append(harness.calls, "persist")

			return harness.persistErr
		},
		Invalidate: func(types.GroupIdentifier) { harness.calls = append(harness.calls, "invalidate") },
		Fetch: func(context.Context, types.GroupIdentifier, uint32) (*signalmeow.Group, error) {
			harness.calls = append(harness.calls, "fetch")

			return harness.raw, harness.fetchErr
		},
		Cache: func(context.Context, signal.Group) error {
			harness.calls = append(harness.calls, "cache")

			return harness.cacheErr
		},
		Preview: func(context.Context, types.SerializedGroupMasterKey, []byte) (signalmeow.GroupJoinPreview, error) {
			harness.calls = append(harness.calls, "preview")

			return harness.preview, harness.previewErr
		},
		Join: func(context.Context, types.SerializedGroupMasterKey, []byte, signalmeow.GroupJoinPreview) (signalmeow.GroupJoinOutcome, error) {
			harness.calls = append(harness.calls, "patch")

			return harness.outcome, harness.patchErr
		},
		Notify: func(_ context.Context, _ *signalmeow.Group, ctx *signalpb.GroupContextV2, change *signalmeow.GroupChange) (*signalmeow.GroupMessageSendResult, error) {
			harness.calls = append(harness.calls, "notify")
			harness.notifiedContext = ctx
			harness.notifiedChange = change

			return harness.notifications, harness.notifyErr
		},
	}

	return harness
}

//nolint:wrapcheck // Test adapter preserves the facade outcome and identity.
func (harness *joinHarness) run(ctx context.Context) (signal.GroupJoinResult, error) {
	return signal.JoinGroupWithOperations(ctx, harness.ops, seededACI, "https://signal.group/#"+joinFragment(32, 16))
}

func TestGroupJoinLifecycle(t *testing.T) {
	t.Parallel()

	harness := newJoinHarness(t)

	got, err := harness.run(t.Context())
	if err != nil || got.Status != signal.GroupJoinMember || !got.Accepted || !got.Changed || !got.Verified || got.Revision != 9 || got.Title != "fresh title" {
		t.Fatalf("result %+v,%v", got, err)
	}

	want := []string{"invalidate", "known", "preview", "persist", "patch", "invalidate", "fetch", "cache", "notify", "invalidate"}
	if !reflect.DeepEqual(harness.calls, want) {
		t.Fatalf("order %v", harness.calls)
	}

	if harness.notifiedContext != harness.outcome.GroupContext || harness.notifiedChange != harness.outcome.Change {
		t.Fatal("did not propagate verified signed response")
	}
}

//nolint:funlen // Distinct fresh membership and request no-op scenarios.
func TestGroupJoinNoOps(t *testing.T) {
	t.Parallel()

	t.Run("member disabled link", func(t *testing.T) {
		t.Parallel()

		harness := newJoinHarness(t)
		harness.known = true
		harness.preview.Access = signalmeow.AccessControl_UNSATISFIABLE

		got, err := harness.run(t.Context())
		if err != nil || got.Status != signal.GroupJoinMember || !got.Verified || got.Accepted || got.Changed {
			t.Fatalf("result %+v,%v", got, err)
		}

		if !reflect.DeepEqual(harness.calls, []string{"invalidate", "known", "fetch", "cache", "invalidate"}) {
			t.Fatal(harness.calls)
		}
	})
	t.Run("pending inaccessible full state", func(t *testing.T) {
		t.Parallel()

		harness := newJoinHarness(t)
		harness.known = true
		harness.fetchErr = signal.ErrNotAMember
		harness.preview.PendingAdminApproval = true

		got, err := harness.run(t.Context())
		if err != nil || got.Status != signal.GroupJoinRequesting || !got.Verified || got.Accepted || got.Changed {
			t.Fatalf("result %+v,%v", got, err)
		}

		if !reflect.DeepEqual(harness.calls, []string{"invalidate", "known", "fetch", "preview", "persist", "invalidate"}) {
			t.Fatal(harness.calls)
		}
	})
	t.Run("approval signed response", func(t *testing.T) {
		t.Parallel()

		harness := newJoinHarness(t)
		harness.preview.Access = signalmeow.AccessControl_ADMINISTRATOR
		harness.outcome.Requesting = true
		harness.fetchErr = signal.ErrNotAMember

		got, err := harness.run(t.Context())
		if err != nil || got.Status != signal.GroupJoinRequesting || !got.Verified || !got.Accepted || got.Title != "preview title" {
			t.Fatalf("result %+v,%v", got, err)
		}

		if !reflect.DeepEqual(harness.calls, []string{"invalidate", "known", "preview", "persist", "patch", "invalidate"}) {
			t.Fatal(harness.calls)
		}
	})
	t.Run("invited self", func(t *testing.T) {
		t.Parallel()

		harness := newJoinHarness(t)
		harness.known = true
		harness.raw.Members = nil
		harness.raw.PendingMembers = []*signalmeow.PendingMember{{ServiceID: libsignalgo.NewACIServiceID(uuid.MustParse(seededACI))}}

		_, err := harness.run(t.Context())
		if !errors.Is(err, signal.ErrGroupInvitationRequiresAcceptance) || !reflect.DeepEqual(harness.calls, []string{"invalidate", "known", "fetch", "invalidate"}) {
			t.Fatalf("%v,%v", harness.calls, err)
		}
	})
}

//nolint:funlen // Accepted, definite and uncertain outcome matrix.
func TestGroupJoinFailureOutcomes(t *testing.T) {
	t.Parallel()

	secretErr := &url.Error{Op: "PATCH", URL: "https://secret/password", Err: io.ErrClosedPipe}

	cases := []struct {
		name           string
		setup          func(*joinHarness)
		sent, accepted bool
		identity       error
	}{
		{"persist", func(harness *joinHarness) { harness.persistErr = secretErr }, false, false, io.ErrClosedPipe},
		{"preview", func(harness *joinHarness) { harness.previewErr = secretErr }, false, false, io.ErrClosedPipe},
		{"known store", func(harness *joinHarness) { harness.knownErr = secretErr }, false, false, io.ErrClosedPipe},
		{"fresh fetch", func(harness *joinHarness) { harness.known = true; harness.fetchErr = secretErr }, false, false, io.ErrClosedPipe},
		{"disabled", func(harness *joinHarness) { harness.preview.Access = signalmeow.AccessControl_UNSATISFIABLE }, false, false, signal.ErrGroupLinkInactive},
		{"overflow", func(harness *joinHarness) { harness.preview.Revision = math.MaxUint32 }, false, false, signal.ErrInvalidGroupInviteLink},
		{"join conflict", func(harness *joinHarness) {
			harness.patchErr = signalmeow.ConflictError
			harness.outcome = signalmeow.GroupJoinOutcome{Attempted: true, Revision: 8}
		}, true, false, signal.ErrGroupChanged},
		{"inactive refusal", func(harness *joinHarness) {
			harness.patchErr = signalmeow.AuthorizationFailedError
			harness.outcome = signalmeow.GroupJoinOutcome{Attempted: true, Revision: 8}
		}, true, false, signal.ErrGroupLinkInactive},
		{"terminated", func(harness *joinHarness) { harness.previewErr = signalmeow.ErrGroupJoinTerminated }, false, false, signal.ErrGroupTerminated},
		{"join uncertain", func(harness *joinHarness) {
			harness.patchErr = errors.Join(signalmeow.ErrGroupJoinUncertain, secretErr)
			harness.outcome = signalmeow.GroupJoinOutcome{Attempted: true, Revision: 8}
		}, true, false, signal.ErrGroupUpdateUncertain},
		{"credential before patch", func(harness *joinHarness) {
			harness.patchErr = secretErr
			harness.outcome = signalmeow.GroupJoinOutcome{}
		}, true, false, io.ErrClosedPipe},
		{"accepted signature failure", func(harness *joinHarness) { harness.patchErr = secretErr; harness.outcome.Verified = false }, true, true, io.ErrClosedPipe},
		{"accepted fetch", func(harness *joinHarness) { harness.fetchErr = secretErr }, true, true, io.ErrClosedPipe},
		{"accepted removal", func(harness *joinHarness) { harness.raw.Members = nil }, true, true, signal.ErrNotAMember},
		{"accepted cache", func(harness *joinHarness) { harness.cacheErr = secretErr }, true, true, io.ErrClosedPipe},
		{"accepted notification", func(harness *joinHarness) { harness.notifyErr = secretErr }, true, true, io.ErrClosedPipe},
		{"recipient failure", func(harness *joinHarness) {
			harness.notifications.FailedToSendTo = []signalmeow.FailedSendResult{{Error: secretErr}}
		}, true, true, io.ErrClosedPipe},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			harness := newJoinHarness(t)
			testCase.setup(harness)

			got, err := harness.run(t.Context())
			if !errors.Is(err, testCase.identity) || got.Accepted != testCase.accepted || got.Changed != testCase.accepted {
				t.Fatalf("result %+v,%v", got, err)
			}

			if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "password") {
				t.Fatal("leaked error")
			}

			patches := 0

			for _, call := range harness.calls {
				if call == "patch" {
					patches++
				}
			}

			if patches > 1 || (patches == 1) != testCase.sent || harness.calls[len(harness.calls)-1] != "invalidate" {
				t.Fatal(harness.calls)
			}

			if testCase.accepted && testCase.name != "accepted notification" && testCase.name != "recipient failure" && testCase.name != "accepted cache" {
				if got.Status != "" || got.Title != "" || got.Verified {
					t.Fatal(got)
				}
			}

			if testCase.name == "accepted cache" && (got.Status != signal.GroupJoinMember || !got.Verified || got.Title != "fresh title" || got.Revision != 9) {
				t.Fatal("cache failure discarded evidence", got)
			}

			if testCase.name == "join uncertain" && (!strings.Contains(err.Error(), "attempted revision 8") || got.Revision != 8) {
				t.Fatal(err)
			}

			if !harness.outcome.Verified && harness.notifiedContext != nil {
				t.Fatal("unverified response propagated")
			}
		})
	}
}

func TestGroupJoinCancellation(t *testing.T) {
	t.Parallel()

	harness := newJoinHarness(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := harness.run(ctx)
	if !errors.Is(err, context.Canceled) || len(harness.calls) != 0 {
		t.Fatalf("%v,%v", harness.calls, err)
	}

	harness = newJoinHarness(t)
	ctx, cancel = context.WithCancel(t.Context())
	original := harness.ops.Persist
	harness.ops.Persist = func(ctx context.Context, gid types.GroupIdentifier, key types.SerializedGroupMasterKey) error {
		err := original(ctx, gid, key)

		cancel()

		return err
	}

	_, err = harness.run(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}

	for _, call := range harness.calls {
		if call == "patch" {
			t.Fatal("submitted after cancellation")
		}
	}
}

func TestGroupJoinClientLifecycle(t *testing.T) {
	t.Parallel()

	link := "https://signal.group/#" + joinFragment(32, 16)

	cli, err := signal.Open(t.Context(), signal.Options{DataDir: seedAccount(t)})
	if err != nil {
		t.Fatal(err)
	}

	_, err = cli.JoinGroup(t.Context(), link)
	if !errors.Is(err, signal.ErrNotConnected) {
		t.Fatal(err)
	}

	err = cli.Close()
	if err != nil {
		t.Fatal(err)
	}

	_, err = cli.JoinGroup(t.Context(), link)
	if !errors.Is(err, signal.ErrClosed) {
		t.Fatal(err)
	}

	cli = openOffline(t, seedAccount(t), signal.SendOnly())
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err = cli.JoinGroup(ctx, link)
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}

	signal.LoseConnection(cli)

	_, err = cli.JoinGroup(t.Context(), link)
	if !errors.Is(err, signal.ErrDeviceUnlinked) {
		t.Fatal(err)
	}

	err = cli.Close()
	if err != nil {
		t.Fatal(err)
	}

	_, err = cli.JoinGroup(t.Context(), link)
	if !errors.Is(err, signal.ErrClosed) {
		t.Fatal(err)
	}
}

func TestGroupJoinSensitiveContext(t *testing.T) {
	t.Parallel()

	harness := newJoinHarness(t)

	var logs bytes.Buffer

	logger := zerolog.New(&logs).Level(zerolog.TraceLevel)
	emit := func(ctx context.Context) { zerolog.Ctx(ctx).Error().Str("credential", "secret").Msg("sensitive") }
	known := harness.ops.Known
	harness.ops.Known = func(ctx context.Context, gid types.GroupIdentifier) (bool, error) { emit(ctx); return known(ctx, gid) }
	preview := harness.ops.Preview
	harness.ops.Preview = func(ctx context.Context, key types.SerializedGroupMasterKey, password []byte) (signalmeow.GroupJoinPreview, error) {
		emit(ctx)

		return preview(ctx, key, password)
	}

	persist := harness.ops.Persist
	harness.ops.Persist = func(ctx context.Context, gid types.GroupIdentifier, key types.SerializedGroupMasterKey) error {
		emit(ctx)

		return persist(ctx, gid, key)
	}

	join := harness.ops.Join
	harness.ops.Join = func(ctx context.Context, key types.SerializedGroupMasterKey, password []byte, preview signalmeow.GroupJoinPreview) (signalmeow.GroupJoinOutcome, error) {
		emit(ctx)

		return join(ctx, key, password, preview)
	}

	fetch := harness.ops.Fetch
	harness.ops.Fetch = func(ctx context.Context, gid types.GroupIdentifier, revision uint32) (*signalmeow.Group, error) {
		emit(ctx)

		return fetch(ctx, gid, revision)
	}

	cache := harness.ops.Cache
	harness.ops.Cache = func(ctx context.Context, group signal.Group) error { emit(ctx); return cache(ctx, group) }
	notify := harness.ops.Notify
	harness.ops.Notify = func(ctx context.Context, group *signalmeow.Group, groupContext *signalpb.GroupContextV2, change *signalmeow.GroupChange) (*signalmeow.GroupMessageSendResult, error) {
		emit(ctx)

		return notify(ctx, group, groupContext, change)
	}

	_, err := harness.run(logger.WithContext(t.Context()))
	if err != nil || logs.Len() != 0 {
		t.Fatalf("operation error %v, log bytes %d", err, logs.Len())
	}
}

func TestGroupJoinCancelAfterFreshFetch(t *testing.T) {
	t.Parallel()

	for _, known := range []bool{false, true} {
		harness := newJoinHarness(t)
		harness.known = known
		ctx, cancel := context.WithCancel(t.Context())
		fetch := harness.ops.Fetch
		harness.ops.Fetch = func(ctx context.Context, gid types.GroupIdentifier, revision uint32) (*signalmeow.Group, error) {
			raw, err := fetch(ctx, gid, revision)

			cancel()

			return raw, err
		}

		_, err := harness.run(ctx)
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}

		for _, call := range harness.calls {
			if call == "cache" || call == "notify" {
				t.Fatal("followup started after cancellation")
			}
		}
	}
}

func TestGroupJoinMemberNoOpCacheFailure(t *testing.T) {
	t.Parallel()

	harness := newJoinHarness(t)
	harness.known = true
	harness.cacheErr = io.ErrClosedPipe

	got, err := harness.run(t.Context())
	if !errors.Is(err, io.ErrClosedPipe) || !got.Verified || got.Status != signal.GroupJoinMember || got.Title != "fresh title" ||
		got.Revision != 9 || got.Accepted || got.Changed {
		t.Fatalf("cache failure discarded no-op evidence %+v,%v", got, err)
	}
}
