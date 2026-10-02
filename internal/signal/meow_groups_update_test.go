//go:build cgo || libsignal_go

package signal_test

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
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

func TestSettingsChangeExactFields(t *testing.T) {
	t.Parallel()

	raw := rawGroup(time.Time{})
	raw.AccessControl = &signalmeow.GroupAccessControl{
		Attributes: signalmeow.AccessControl_MEMBER,
		Members:    signalmeow.AccessControl_MEMBER,
	}
	change, err := signal.SettingsChange(raw, seededACI,
		signal.GroupUpdate{
			Description:              new(""),
			TimerSeconds:             new(uint32(0)),
			AnnouncementsOnly:        new(false),
			MembersCanEditAttributes: new(false),
			MembersCanAddMembers:     new(false),
		})

	want := &signalmeow.GroupChange{
		ModifyDescription:                  new(""),
		ModifyDisappearingMessagesDuration: new(uint32(0)),
		ModifyAnnouncementsOnly:            new(false),
		ModifyAttributesAccess:             new(signalmeow.AccessControl_ADMINISTRATOR),
		ModifyMemberAccess:                 new(signalmeow.AccessControl_ADMINISTRATOR),
	}
	if err != nil || !reflect.DeepEqual(change, want) {
		t.Fatalf("change = %+v, %v", change, err)
	}

	sparse, err := signal.SettingsChange(raw, seededACI, signal.GroupUpdate{Description: new("new")})
	if err != nil || !reflect.DeepEqual(sparse, &signalmeow.GroupChange{ModifyDescription: new("new")}) {
		t.Fatalf("sparse = %+v, %v", sparse, err)
	}

	same, err := signal.SettingsChange(raw, seededACI, signal.GroupUpdate{AnnouncementsOnly: new(true)})
	if err != nil || same != nil {
		t.Fatalf("noop = %+v, %v", same, err)
	}

	_, err = signal.SettingsChange(raw, memberACI,
		signal.GroupUpdate{
			Description:       new("new"),
			AnnouncementsOnly: new(true),
		})
	if !errors.Is(err, signal.ErrGroupPermission) {
		t.Fatalf("unchanged privileged flag = %v", err)
	}

	raw.Revision = math.MaxUint32

	_, err = signal.SettingsChange(raw, seededACI, signal.GroupUpdate{Description: new("new")})
	if !errors.Is(err, signal.ErrUnknownGroup) {
		t.Fatalf("overflow = %v", err)
	}
}

func TestUpdateGroupSettingsOnce(t *testing.T) { //nolint:cyclop,gocognit,funlen // transport outcomes
	t.Parallel()

	for _, test := range []struct {
		name                          string
		patchErr, fetchErr, notifyErr error
		malformed                     bool
		want                          error
	}{
		{name: "success"},
		{name: "settings conflict", patchErr: signalmeow.ConflictError, want: signal.ErrGroupChanged},
		{name: "cancelled", patchErr: context.Canceled, want: context.Canceled},
		{name: "accepted fetch error", fetchErr: io.ErrClosedPipe, want: io.ErrClosedPipe},
		{name: "accepted fetch cancelled", fetchErr: context.Canceled, want: context.Canceled},
		{name: "accepted malformed response", malformed: true},
		{name: "notification fails", notifyErr: io.ErrClosedPipe},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			raw := rawGroup(time.Time{})
			raw.GroupMasterKey = types.SerializedGroupMasterKey(base64.StdEncoding.EncodeToString(make([]byte, 32)))
			accepted := *raw
			accepted.Revision = 8
			accepted.Description = "server state"

			sender := &additionSender{
				removalSender: removalSender{
					patchErr:  test.patchErr,
					notifyErr: test.notifyErr,
					response:  &signalpb.GroupChangeResponse{GroupChange: &signalpb.GroupChange{Actions: []byte{8, 8}}},
				},
				fetched:  &accepted,
				fetchErr: test.fetchErr,
			}
			if test.malformed {
				sender.response = nil
			}

			change := &signalmeow.GroupChange{ModifyDescription: new("new")}

			got, err := signal.UpdateGroupSettingsOnce(t.Context(), sender, raw, change, func() { sender.invalidated = true })
			if sender.patchCalls != 1 || (!test.malformed && !errors.Is(err, test.want)) {
				t.Fatalf("result = %+v,%v; patches %d", got, err, sender.patchCalls)
			}

			if test.patchErr != nil {
				if got != nil || sender.notifyCalls != 0 || sender.fetchCalls != 0 {
					t.Fatal("rejected change treated as accepted")
				}

				return
			}

			if !sender.invalidated || got == nil || got.GroupIdentifier != raw.GroupIdentifier || got.Revision != 8 ||
				errors.Is(err, signal.ErrGroupUpdateUncertain) {
				t.Fatalf("accepted state lost: %+v, %v", got, err)
			}

			if test.malformed || test.fetchErr != nil {
				if err == nil || !strings.Contains(err.Error(), "inspect groups show before retrying") {
					t.Fatalf("missing acceptance guidance: %v", err)
				}

				return
			}

			if got != &accepted ||
				sender.fetchCalls != 1 ||
				sender.fetchRevision != 8 ||
				sender.notifyCalls != 1 ||
				sender.notifiedBeforeEviction {
				t.Fatalf("authoritative result = %+v; sender %+v", got, sender)
			}
		})
	}
}

func TestUpdateGroupSettingsOnceInvalidFollowUp(t *testing.T) {
	t.Parallel()

	for _, fetched := range []*signalmeow.Group{nil, {GroupIdentifier: "wrong", Revision: 8}, rawGroup(time.Time{})} {
		raw := rawGroup(time.Time{})
		raw.GroupMasterKey = types.SerializedGroupMasterKey(base64.StdEncoding.EncodeToString(make([]byte, 32)))
		sender := &additionSender{
			removalSender: removalSender{
				response: &signalpb.GroupChangeResponse{
					GroupChange: &signalpb.GroupChange{Actions: []byte{8, 8}},
				},
			},
			fetched: fetched,
		}

		change := &signalmeow.GroupChange{ModifyDescription: new("new")}

		got, err := signal.UpdateGroupSettingsOnce(t.Context(), sender, raw, change,
			func() { sender.invalidated = true })
		if err == nil ||
			got == nil ||
			got.Revision != 8 ||
			sender.patchCalls != 1 ||
			!strings.Contains(err.Error(),
				"inspect groups show before retrying") {
			t.Fatalf("invalid followup = %+v,%v", got, err)
		}
	}
}

func TestUpdateGroupSettingsRequiresConnection(t *testing.T) {
	t.Parallel()

	cli, openErr := signal.Open(t.Context(), signal.Options{DataDir: seedAccount(t)})
	if openErr != nil {
		t.Fatal(openErr)
	}

	t.Cleanup(func() {
		closeErr := cli.Close()
		if closeErr != nil {
			t.Error(closeErr)
		}
	})

	_, err := cli.UpdateGroup(t.Context(), "unused", signal.GroupUpdate{Description: new("new")})
	if !errors.Is(err, signal.ErrNotConnected) {
		t.Fatalf("not connected = %v", err)
	}

	_, err = cli.UpdateGroup(t.Context(), "unused", signal.GroupUpdate{})
	if !errors.Is(err, signal.ErrInvalidGroupUpdate) {
		t.Fatalf("preflight = %v", err)
	}
}

func TestUpdateGroupSettingsOnceUncertainResponse(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name  string
		cause error
	}{
		{
			"successful PATCH unreadable reply",
			fmt.Errorf("failed to read storage manifest response: %w", io.ErrUnexpectedEOF),
		},
		{"successful PATCH undecodable reply", fmt.Errorf("failed to unmarshal signed groupChange: %w", io.ErrUnexpectedEOF)},
		{"network failure", io.ErrClosedPipe},
		{"cancelled request", context.Canceled},
		{"unexpected no content", signalmeow.NoContentError},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			raw := rawGroup(time.Time{})
			raw.GroupMasterKey = types.SerializedGroupMasterKey(base64.StdEncoding.EncodeToString(make([]byte, 32)))
			raw.GroupIdentifier = types.GroupIdentifier(base64.StdEncoding.EncodeToString(make([]byte, 32)))
			sender := &additionSender{removalSender: removalSender{patchErr: test.cause}}
			change := &signalmeow.GroupChange{ModifyDescription: new("new")}

			got, err := signal.UpdateGroupSettingsOnce(t.Context(), sender, raw, change,
				func() { sender.invalidated = true })
			if got != nil || !errors.Is(err, test.cause) ||
				sender.patchCalls != 1 || sender.fetchCalls != 0 || sender.notifyCalls != 0 {
				t.Fatalf("uncertain result = %+v, %v; sender %+v", got, err, sender)
			}

			guidance := "inspect groups show " + string(raw.GroupIdentifier) + " before retrying"
			if !errors.Is(err, signal.ErrGroupUpdateUncertain) ||
				!strings.Contains(err.Error(), "revision 8") || !strings.Contains(err.Error(), guidance) {
				t.Fatalf("missing uncertainty guidance: %v", err)
			}
		})
	}
}

func TestUpdateGroupSettingsOnceDefiniteRejection(t *testing.T) {
	t.Parallel()

	for _, test := range []struct{ cause, want error }{
		{signalmeow.ConflictError, signal.ErrGroupChanged},
		{signalmeow.ContactManifestMismatchError, signal.ErrGroupChanged},
		{signalmeow.AuthorizationFailedError, signalmeow.AuthorizationFailedError},
		{signalmeow.NotFoundError, signalmeow.NotFoundError},
		{signalmeow.GroupPatchNotAcceptedError, signalmeow.GroupPatchNotAcceptedError},
		{signalmeow.RateLimitError, signalmeow.RateLimitError},
		{signalmeow.DeprecatedVersionError, signalmeow.DeprecatedVersionError},
	} {
		raw := rawGroup(time.Time{})
		raw.GroupMasterKey = types.SerializedGroupMasterKey(base64.StdEncoding.EncodeToString(make([]byte, 32)))
		sender := &additionSender{removalSender: removalSender{patchErr: test.cause}}
		change := &signalmeow.GroupChange{ModifyDescription: new("new")}

		got, err := signal.UpdateGroupSettingsOnce(t.Context(), sender, raw, change, func() { sender.invalidated = true })
		if got != nil || !errors.Is(err, test.want) || errors.Is(err, signal.ErrGroupUpdateUncertain) ||
			sender.patchCalls != 1 || sender.fetchCalls != 0 {
			t.Fatalf("rejected result = %+v, %v", got, err)
		}
	}
}

func TestSettingsChangeNormalizesRawPermissions(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name                               string
		access                             signalmeow.AccessControl
		missing, needsAdmins, needsMembers bool
	}{
		{"missing control", signalmeow.AccessControl_UNKNOWN, true, true, true},
		{"unknown", signalmeow.AccessControl_UNKNOWN, false, true, true},
		{"any", signalmeow.AccessControl_ANY, false, true, true},
		{"unsatisfiable", signalmeow.AccessControl_UNSATISFIABLE, false, true, true},
		{"member", signalmeow.AccessControl_MEMBER, false, true, false},
		{"administrator", signalmeow.AccessControl_ADMINISTRATOR, false, false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			var rawAccess *signalmeow.GroupAccessControl
			if !test.missing {
				rawAccess = &signalmeow.GroupAccessControl{Attributes: test.access, Members: test.access}
			}

			assertPermissionSettings(t, rawAccess, false, test.needsAdmins)
			assertPermissionSettings(t, rawAccess, true, test.needsMembers)
		})
	}
}

func assertPermissionSettings(t *testing.T, rawAccess *signalmeow.GroupAccessControl, members, needed bool) {
	t.Helper()

	requested := signalmeow.AccessControl_ADMINISTRATOR
	if members {
		requested = signalmeow.AccessControl_MEMBER
	}

	// Include an unchanged scalar to ensure it cannot hide a permission normalization.
	for _, permission := range []struct {
		name                   string
		update                 signal.GroupUpdate
		attributes, addMembers bool
	}{
		{"attributes", signal.GroupUpdate{AnnouncementsOnly: new(true), MembersCanEditAttributes: new(members)}, true, false},
		{"members", signal.GroupUpdate{AnnouncementsOnly: new(true), MembersCanAddMembers: new(members)}, false, true},
		{"both", signal.GroupUpdate{
			AnnouncementsOnly: new(true), MembersCanEditAttributes: new(members), MembersCanAddMembers: new(members),
		}, true, true},
	} {
		t.Run(fmt.Sprintf("%s/%v", permission.name, members), func(t *testing.T) {
			raw := rawGroup(time.Time{})
			raw.AccessControl = rawAccess

			var want *signalmeow.GroupChange
			if needed {
				want = &signalmeow.GroupChange{}
				if permission.attributes {
					want.ModifyAttributesAccess = new(requested)
				}

				if permission.addMembers {
					want.ModifyMemberAccess = new(requested)
				}
			}

			assertSettingsNormalization(t, raw, permission.update, want)
		})
	}
}

func assertSettingsNormalization(t *testing.T, raw *signalmeow.Group,
	update signal.GroupUpdate, want *signalmeow.GroupChange,
) {
	t.Helper()

	got, err := signal.SettingsChange(raw, seededACI, update)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, %v; want %+v", got, err, want)
	}

	raw.Revision = math.MaxUint32

	_, err = signal.SettingsChange(raw, seededACI, update)
	if (want == nil && err != nil) || (want != nil && !errors.Is(err, signal.ErrUnknownGroup)) {
		t.Fatalf("overflow: %v", err)
	}
}
