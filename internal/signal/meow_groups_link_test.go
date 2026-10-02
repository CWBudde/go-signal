//go:build cgo || libsignal_go

package signal_test

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow/protobuf/signalpb"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow/types"
)

func rawLinkGroup() *signalmeow.Group {
	raw := rawGroup(time.Time{})
	raw.GroupMasterKey = types.SerializedGroupMasterKey(linkMasterKey)
	raw.InviteLinkPassword = new(types.SerializedInviteLinkPassword(linkPassword))
	raw.AccessControl = &signalmeow.GroupAccessControl{AddFromInviteLink: signalmeow.AccessControl_UNSATISFIABLE}

	return raw
}

//nolint:cyclop // behavioral matrix
func TestGroupLinkChangeExactWire(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		state signal.GroupLinkState
		wire  signalmeow.AccessControl
	}{
		{signal.GroupLinkDisabled, signalmeow.AccessControl_UNSATISFIABLE},
		{signal.GroupLinkEnabled, signalmeow.AccessControl_ANY},
		{signal.GroupLinkApproval, signalmeow.AccessControl_ADMINISTRATOR},
	} {
		raw := rawLinkGroup()
		raw.AccessControl.AddFromInviteLink = signalmeow.AccessControl_MEMBER
		change, err := signal.GroupLinkChange(raw, seededACI, signal.GroupLinkUpdate{State: new(test.state)})

		want := &signalmeow.GroupChange{ModifyAddFromInviteLinkAccess: new(test.wire)}
		if err != nil || !reflect.DeepEqual(change, want) {
			t.Fatalf("wire %+v,%v", change, err)
		}

		raw.AccessControl.AddFromInviteLink = test.wire

		change, err = signal.GroupLinkChange(raw, seededACI, signal.GroupLinkUpdate{State: new(test.state)})
		if err != nil || change != nil {
			t.Fatalf("noop %+v,%v", change, err)
		}

		raw.InviteLinkPassword = nil

		change, err = signal.GroupLinkChange(raw, seededACI, signal.GroupLinkUpdate{State: new(test.state)})
		if err != nil {
			t.Fatal(err)
		}

		if test.state == signal.GroupLinkDisabled {
			if change != nil {
				t.Fatal("disabled invented password")
			}

			continue
		}

		if change == nil || change.ModifyInviteLinkPassword == nil || change.ModifyAddFromInviteLinkAccess != nil {
			t.Fatalf("missing first password %+v", change)
		}
	}

	raw := rawLinkGroup()
	raw.InviteLinkPassword = nil

	change, err := signal.GroupLinkChange(raw, seededACI, signal.GroupLinkUpdate{State: new(signal.GroupLinkApproval)})
	if err != nil || change == nil || change.ModifyInviteLinkPassword == nil ||
		change.ModifyAddFromInviteLinkAccess == nil ||
		*change.ModifyAddFromInviteLinkAccess != signalmeow.AccessControl_ADMINISTRATOR {
		t.Fatalf("first enable atomic %+v,%v", change, err)
	}

	decoded, decodeErr := base64.StdEncoding.DecodeString(string(*change.ModifyInviteLinkPassword))
	if decodeErr != nil || len(decoded) != 16 {
		t.Fatal("bad generated password")
	}
}

//nolint:cyclop // behavioral matrix
func TestGroupLinkResetPreservesRawAccess(t *testing.T) {
	t.Parallel()

	for _, access := range []signalmeow.AccessControl{
		signalmeow.AccessControl_UNKNOWN,
		signalmeow.AccessControl_MEMBER,
		signalmeow.AccessControl_UNSATISFIABLE,
		signalmeow.AccessControl_ANY,
		signalmeow.AccessControl_ADMINISTRATOR,
		signalmeow.AccessControl(42),
	} {
		raw := rawLinkGroup()
		raw.AccessControl.AddFromInviteLink = access

		change, err := signal.GroupLinkChange(raw, seededACI, signal.GroupLinkUpdate{Reset: true})
		if err != nil || change == nil || change.ModifyAddFromInviteLinkAccess != nil ||
			change.ModifyInviteLinkPassword == nil {
			t.Fatalf("reset access %+v,%v", change, err)
		}

		decoded, decodeErr := base64.StdEncoding.DecodeString(string(*change.ModifyInviteLinkPassword))
		if decodeErr != nil || len(decoded) != 16 || string(*change.ModifyInviteLinkPassword) == linkPassword {
			t.Fatal("bad reset password")
		}

		want := &signalmeow.GroupChange{ModifyInviteLinkPassword: change.ModifyInviteLinkPassword}
		if !reflect.DeepEqual(change, want) || raw.Revision != 7 || raw.AccessControl.AddFromInviteLink != access ||
			string(*raw.InviteLinkPassword) != linkPassword {
			t.Fatal("reset changed unrelated or original state")
		}
	}

	raw := rawLinkGroup()
	raw.AccessControl = nil

	got, err := signal.ConvertGroupLink(raw, seededACI)
	if err != nil || got.State != signal.GroupLinkUnknown || got.URL != "" {
		t.Fatalf("unknown view %+v,%v", got, err)
	}

	raw.Revision = math.MaxUint32

	_, err = signal.GroupLinkChange(raw, seededACI, signal.GroupLinkUpdate{Reset: true})
	if !errors.Is(err, signal.ErrUnknownGroup) {
		t.Fatalf("overflow %v", err)
	}

	_, err = signal.GroupLinkChange(raw, memberACI, signal.GroupLinkUpdate{State: new(signal.GroupLinkDisabled)})
	if !errors.Is(err, signal.ErrGroupPermission) {
		t.Fatalf("member write %v", err)
	}
}

//nolint:cyclop,funlen // behavioral matrix
func TestGroupLinkBackendLifecycleAndReferenceRedaction(t *testing.T) {
	t.Parallel()

	cli, err := signal.Open(t.Context(), signal.Options{DataDir: seedAccount(t)})
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		closeErr := cli.Close()
		if closeErr != nil {
			t.Error(closeErr)
		}
	})

	_, err = cli.GroupLink(t.Context(), "unused")
	if !errors.Is(err, signal.ErrNotConnected) {
		t.Fatalf("read disconnected %v", err)
	}

	_, err = cli.UpdateGroupLink(t.Context(), "unused", signal.GroupLinkUpdate{Reset: true})
	if !errors.Is(err, signal.ErrNotConnected) {
		t.Fatalf("write disconnected %v", err)
	}

	_, err = cli.UpdateGroupLink(t.Context(), "unused", signal.GroupLinkUpdate{})
	if !errors.Is(err, signal.ErrInvalidGroupLinkUpdate) {
		t.Fatalf("preflight %v", err)
	}

	err = cli.Close()
	if err != nil {
		t.Fatal(err)
	}

	cli = openOffline(t, seedAccount(t), signal.SendOnly())
	for _, ref := range []string{"private-invalid-invite-reference", base64.StdEncoding.EncodeToString(make([]byte, 32))} {
		_, err = cli.GroupLink(t.Context(), ref)
		if !errors.Is(err, signal.ErrUnknownGroup) || strings.Contains(err.Error(), ref) {
			t.Fatalf("unknown read redaction %v", err)
		}

		_, err = cli.UpdateGroupLink(t.Context(), ref, signal.GroupLinkUpdate{Reset: true})
		if !errors.Is(err, signal.ErrUnknownGroup) || strings.Contains(err.Error(), ref) {
			t.Fatalf("unknown write redaction %v", err)
		}
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err = cli.GroupLink(ctx, "unused")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("read canceled %v", err)
	}

	_, err = cli.UpdateGroupLink(ctx, "unused", signal.GroupLinkUpdate{Reset: true})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("write canceled %v", err)
	}

	signal.LoseConnection(cli)

	_, err = cli.GroupLink(t.Context(), "unused")
	if !errors.Is(err, signal.ErrDeviceUnlinked) {
		t.Fatalf("read unlink %v", err)
	}

	_, err = cli.UpdateGroupLink(t.Context(), "unused", signal.GroupLinkUpdate{Reset: true})
	if !errors.Is(err, signal.ErrDeviceUnlinked) {
		t.Fatalf("write unlink %v", err)
	}

	err = cli.Close()
	if err != nil {
		t.Fatal(err)
	}

	_, err = cli.GroupLink(t.Context(), "unused")
	if !errors.Is(err, signal.ErrClosed) {
		t.Fatalf("read closed %v", err)
	}

	_, err = cli.UpdateGroupLink(t.Context(), "unused", signal.GroupLinkUpdate{Reset: true})
	if !errors.Is(err, signal.ErrClosed) {
		t.Fatalf("write closed %v", err)
	}
}

func TestUpdateGroupLinkOnce(t *testing.T) { //nolint:funlen,cyclop,gocognit,gocyclo // transport outcomes
	t.Parallel()

	for name, test := range map[string]struct {
		patchErr, fetchErr, notifyErr, want error
		resetOnly                           bool
		malformed, invalidFetch, uncertain  bool
	}{
		"invite reset accepted":           {resetOnly: true},
		"invite access revision conflict": {patchErr: signalmeow.ConflictError, want: signal.ErrGroupChanged},
		"invite manifest conflict": {
			patchErr: signalmeow.ContactManifestMismatchError,
			want:     signal.ErrGroupChanged,
		},
		"invite forbidden": {
			patchErr: signalmeow.AuthorizationFailedError,
			want:     signalmeow.AuthorizationFailedError,
		},
		"invite rejected": {
			patchErr: signalmeow.GroupPatchNotAcceptedError,
			want:     signalmeow.GroupPatchNotAcceptedError,
		},
		"invite rate limit":        {patchErr: signalmeow.RateLimitError, want: signalmeow.RateLimitError},
		"invite group disappeared": {patchErr: signalmeow.NotFoundError, want: signalmeow.NotFoundError},
		"invite deprecated": {
			patchErr: signalmeow.DeprecatedVersionError,
			want:     signalmeow.DeprecatedVersionError,
		},
		"invite network uncertain":           {patchErr: io.ErrClosedPipe, want: io.ErrClosedPipe, uncertain: true},
		"invite decode uncertain":            {patchErr: io.ErrUnexpectedEOF, want: io.ErrUnexpectedEOF, uncertain: true},
		"invite cancel uncertain":            {patchErr: context.Canceled, want: context.Canceled, uncertain: true},
		"invite accepted malformed response": {malformed: true},
		"invite accepted fetch failure":      {fetchErr: io.ErrClosedPipe, want: io.ErrClosedPipe},
		"invite accepted fetch cancellation": {fetchErr: context.Canceled, want: context.Canceled},
		"invite accepted invalid fetch":      {invalidFetch: true},
		"invite notify failure":              {notifyErr: io.ErrClosedPipe},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			raw := rawGroup(time.Time{})
			raw.GroupMasterKey = types.SerializedGroupMasterKey(base64.StdEncoding.EncodeToString(make([]byte, 32)))
			accepted := *raw
			accepted.Revision = 8

			sender := &additionSender{removalSender: removalSender{
				patchErr: test.patchErr, notifyErr: test.notifyErr,
				response: &signalpb.GroupChangeResponse{GroupChange: &signalpb.GroupChange{Actions: []byte{8, 8}}},
			}, fetched: &accepted, fetchErr: test.fetchErr}
			if test.malformed {
				sender.response = nil
			}

			if test.invalidFetch {
				sender.fetched = &signalmeow.Group{GroupIdentifier: "incorrect invite response group", Revision: 8}
			}

			change := &signalmeow.GroupChange{ModifyInviteLinkPassword: new(types.SerializedInviteLinkPassword(linkPassword))}
			if !test.resetOnly {
				change = &signalmeow.GroupChange{ModifyAddFromInviteLinkAccess: new(signalmeow.AccessControl_ANY)}
			}

			got, err := signal.UpdateGroupLinkOnce(t.Context(), sender, raw, change, func() { sender.invalidated = true })
			if sender.patchCalls != 1 ||
				change.Revision != 8 ||
				change.GroupMasterKey != raw.GroupMasterKey ||
				(!test.malformed && !test.invalidFetch && !errors.Is(err, test.want)) {
				t.Fatalf("single patch = %+v,%v; sender %+v", got, err, sender)
			}

			if test.patchErr != nil {
				if got != nil ||
					sender.fetchCalls != 0 ||
					sender.notifyCalls != 0 ||
					errors.Is(err, signal.ErrGroupUpdateUncertain) != test.uncertain {
					t.Fatalf("patch failure = %+v,%v", got, err)
				}

				if test.uncertain && !strings.Contains(err.Error(), "inspect groups link show") {
					t.Fatalf("uncertain guidance %v", err)
				}

				return
			}

			if got == nil ||
				got.GroupIdentifier != raw.GroupIdentifier ||
				got.Revision != 8 ||
				!sender.invalidated ||
				errors.Is(err, signal.ErrGroupUpdateUncertain) {
				t.Fatalf("accepted = %+v,%v", got, err)
			}

			if test.malformed || test.fetchErr != nil || test.invalidFetch {
				if err == nil || !strings.Contains(err.Error(), "inspect groups link show before retrying") {
					t.Fatalf("acceptance guidance %v", err)
				}

				if test.malformed && (sender.fetchCalls != 0 || sender.notifyCalls != 0) {
					t.Fatal("used malformed response")
				}

				return
			}

			if got != &accepted ||
				sender.fetchCalls != 1 ||
				sender.fetchRevision != 8 ||
				sender.notifyCalls != 1 ||
				sender.notifiedBeforeEviction ||
				sender.raw != raw {
				t.Fatalf("authoritative result %+v; sender %+v", got, sender)
			}
		})
	}
}

func TestAcceptedGroupLinkVerificationUsesCommittedRevision(t *testing.T) {
	t.Parallel()

	for _, invalidPassword := range []bool{true, false} {
		raw := rawLinkGroup()
		raw.Revision = 9
		raw.GroupIdentifier = "verified-invite-group"

		raw.AccessControl.AddFromInviteLink = signalmeow.AccessControl_ANY
		if invalidPassword {
			raw.InviteLinkPassword = new(types.SerializedInviteLinkPassword("private-invalid-accepted-password"))
		} else {
			raw.Members = raw.Members[1:]
		}

		got, err := signal.VerifiedGroupLink(raw, seededACI, 8)
		if err == nil || got.ID != "verified-invite-group" || got.Revision != 8 || got.State != "" || got.URL != "" ||
			!strings.Contains(err.Error(), "accepted at revision 8") ||
			strings.Contains(err.Error(), "private-invalid-accepted-password") {
			t.Fatalf("verification %+v,%v", got, err)
		}
	}
}
