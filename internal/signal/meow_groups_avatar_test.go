//go:build cgo || libsignal_go

package signal_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow/protobuf/signalpb"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow/types"
)

const avatarUploadedPath = "avatar-uploaded-path"

type avatarSender struct {
	additionSender

	uploadCalls int
	uploadErr   error
	path        string
	uploaded    []byte
	key         types.SerializedGroupMasterKey
	gid         types.GroupIdentifier
	change      *signalmeow.GroupChange
}

func (s *avatarSender) UploadGroupAvatar(_ context.Context, data []byte, gid types.GroupIdentifier,
	key types.SerializedGroupMasterKey,
) (string, error) {
	s.uploadCalls++
	s.uploaded = bytes.Clone(data)
	s.key, s.gid = key, gid
	data[0] ^= 1 // Ensure dependency ownership cannot corrupt the request.

	return s.path, s.uploadErr
}

func (s *avatarSender) EncryptAndSignGroupChange(ctx context.Context, change *signalmeow.GroupChange) (
	*signalpb.GroupChangeResponse, error,
) {
	s.change = change
	return s.removalSender.EncryptAndSignGroupChange(ctx, change)
}

func avatarRaw() *signalmeow.Group {
	raw := rawGroup(time.Time{})
	raw.GroupMasterKey = types.SerializedGroupMasterKey(base64.StdEncoding.EncodeToString(make([]byte, 32)))
	raw.AvatarPath = "old-path"

	return raw
}

func newAvatarSender(raw *signalmeow.Group) *avatarSender {
	accepted := *raw
	accepted.Revision += 2 // A later authoritative revision may include another administrator's change.
	accepted.AvatarPath = "server-path"

	return &avatarSender{
		path: "uploaded/path",
		additionSender: additionSender{
			fetched: &accepted,
			removalSender: removalSender{
				response: &signalpb.GroupChangeResponse{GroupChange: &signalpb.GroupChange{Actions: []byte{8, 8}}},
			},
		},
	}
}

func TestGroupAvatarUploadAndCombinedPatch(t *testing.T) { //nolint:cyclop // wire and ownership assertions
	t.Parallel()

	raw := avatarRaw()
	original := *raw
	data := avatarPNG(t, 1)
	before := bytes.Clone(data)
	sender := newAvatarSender(raw)

	got, err := signal.UpdateGroupWithAvatarOnce(t.Context(), sender, raw, seededACI,
		signal.GroupUpdate{Avatar: &signal.GroupAvatarUpdate{Data: data}, Description: new("new")},
		func() { sender.invalidated = true })
	if err != nil || got != sender.fetched || got.AvatarPath != "server-path" ||
		sender.uploadCalls != 1 || sender.patchCalls != 1 || sender.fetchCalls != 1 {
		t.Fatalf("update=%+v,%v sender=%+v", got, err, sender)
	}

	want := &signalmeow.GroupChange{
		GroupMasterKey: raw.GroupMasterKey, Revision: 8,
		ModifyAvatar: new("uploaded/path"), ModifyDescription: new("new"),
	}
	if !reflect.DeepEqual(sender.change, want) {
		t.Fatalf("wire=%+v want=%+v", sender.change, want)
	}

	if !bytes.Equal(sender.uploaded, before) || !bytes.Equal(data, before) || sender.gid != raw.GroupIdentifier ||
		sender.key != raw.GroupMasterKey {
		t.Fatal("upload changed data or wrong group key")
	}

	if sender.raw != raw || !reflect.DeepEqual(*raw, original) || sender.notifiedBeforeEviction {
		t.Fatal("original notification state changed")
	}
}

func TestGroupAvatarPreflightBeforeUpload(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		self    string
		prepare func(*signalmeow.Group, *signal.GroupUpdate)
		want    error
	}{
		{name: "invalid image", prepare: func(_ *signalmeow.Group, update *signal.GroupUpdate) {
			update.Avatar.Data = []byte("bad")
		}, want: signal.ErrInvalidGroupUpdate},
		{name: "nonmember", self: newMemberACI, want: signal.ErrNotAMember},
		{name: "attributes denied", self: memberACI, want: signal.ErrGroupPermission},
		{name: "mixed admin setting", self: memberACI, prepare: func(r *signalmeow.Group, u *signal.GroupUpdate) {
			r.AccessControl = &signalmeow.GroupAccessControl{Attributes: signalmeow.AccessControl_MEMBER}
			u.AnnouncementsOnly = new(r.AnnouncementsOnly)
		}, want: signal.ErrGroupPermission},
		{name: "malformed key", prepare: func(raw *signalmeow.Group, _ *signal.GroupUpdate) {
			raw.GroupMasterKey = "invalid-avatar-master-key"
		}, want: signal.ErrUnknownGroup},
		{name: "avatar short key", prepare: func(r *signalmeow.Group, _ *signal.GroupUpdate) {
			r.GroupMasterKey = types.SerializedGroupMasterKey(base64.StdEncoding.EncodeToString(make([]byte, 31)))
		}, want: signal.ErrUnknownGroup},
		{name: "revision overflow", prepare: func(raw *signalmeow.Group, _ *signal.GroupUpdate) {
			raw.Revision = math.MaxUint32
		}, want: signal.ErrUnknownGroup},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			raw := avatarRaw()
			update := signal.GroupUpdate{Avatar: &signal.GroupAvatarUpdate{Data: avatarPNG(t, 1)}}

			self := test.self
			if self == "" {
				self = seededACI
			}

			if test.prepare != nil {
				test.prepare(raw, &update)
			}

			sender := newAvatarSender(raw)

			got, err := signal.UpdateGroupWithAvatarOnce(t.Context(), sender, raw, self, update, func() {})
			if !errors.Is(err, test.want) || got != nil ||
				sender.uploadCalls != 0 || sender.patchCalls != 0 {
				t.Fatalf("preflight=%+v,%v uploads=%d patches=%d", got, err, sender.uploadCalls, sender.patchCalls)
			}
		})
	}
}

func TestGroupAvatarTransportFailures(t *testing.T) { //nolint:cyclop,funlen // transport outcome matrix
	t.Parallel()

	for _, test := range []struct {
		name, path                    string
		uploadErr, patchErr, fetchErr error
		malformed                     bool
		want                          error
		accepted                      bool
		patches                       int
		uncertain                     bool
	}{
		{name: "upload failure", path: avatarUploadedPath, uploadErr: io.ErrClosedPipe, want: io.ErrClosedPipe},
		{name: "blank path", path: "  ", want: signal.ErrInvalidGroupUpdate},
		{name: "control path", path: "path\n", want: signal.ErrInvalidGroupUpdate},
		{name: "invalid UTF8", path: string([]byte{255}), want: signal.ErrInvalidGroupUpdate},
		{
			name: "avatar patch conflict", path: avatarUploadedPath, patchErr: signalmeow.ConflictError,
			want: signal.ErrGroupChanged, patches: 1,
		},
		{
			name: "uncertain", path: avatarUploadedPath, patchErr: io.ErrUnexpectedEOF,
			want: io.ErrUnexpectedEOF, patches: 1, uncertain: true,
		},
		{
			name: "accepted fetch failure", path: avatarUploadedPath, fetchErr: io.ErrClosedPipe,
			want: io.ErrClosedPipe, patches: 1, accepted: true,
		},
		{name: "malformed accepted response", path: avatarUploadedPath, malformed: true, patches: 1, accepted: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			raw := avatarRaw()
			sender := newAvatarSender(raw)
			sender.path = test.path
			sender.uploadErr = test.uploadErr
			sender.patchErr = test.patchErr

			sender.fetchErr = test.fetchErr
			if test.malformed {
				sender.response = &signalpb.GroupChangeResponse{}
			}

			got, err := signal.UpdateGroupWithAvatarOnce(t.Context(), sender, raw, seededACI,
				signal.GroupUpdate{Avatar: &signal.GroupAvatarUpdate{Data: avatarPNG(t, 1)}},
				func() { sender.invalidated = true })
			if err == nil || (!test.malformed && !errors.Is(err, test.want)) ||
				sender.uploadCalls != 1 || sender.patchCalls != test.patches ||
				errors.Is(err, signal.ErrGroupUpdateUncertain) != test.uncertain {
				t.Fatalf("failure=%+v,%v uploads=%d patches=%d", got, err, sender.uploadCalls, sender.patchCalls)
			}

			if test.accepted {
				if got == nil || got.GroupIdentifier != raw.GroupIdentifier || got.Revision != 8 || got.AvatarPath != "" {
					t.Fatalf("partial=%+v", got)
				}
			} else if got != nil {
				t.Fatalf("unaccepted=%+v", got)
			}
		})
	}
}

func TestGroupAvatarRemovalNoUpload(t *testing.T) {
	t.Parallel()

	raw := avatarRaw()
	sender := newAvatarSender(raw)

	_, err := signal.UpdateGroupWithAvatarOnce(t.Context(), sender, raw, seededACI,
		signal.GroupUpdate{Avatar: &signal.GroupAvatarUpdate{Remove: true}},
		func() { sender.invalidated = true })
	if err != nil ||
		sender.uploadCalls != 0 || sender.patchCalls != 1 ||
		sender.change.ModifyAvatar == nil || *sender.change.ModifyAvatar != "" {
		t.Fatalf("remove=%+v,%v", sender.change, err)
	}

	raw.AvatarPath = ""
	raw.Revision = math.MaxUint32
	sender = newAvatarSender(raw)

	got, err := signal.UpdateGroupWithAvatarOnce(t.Context(), sender, raw, seededACI,
		signal.GroupUpdate{Avatar: &signal.GroupAvatarUpdate{Remove: true}},
		func() {})
	if err != nil || got != raw ||
		sender.uploadCalls != 0 || sender.patchCalls != 0 {
		t.Fatalf("noop=%+v,%v", got, err)
	}
}

func TestGroupAvatarCanceledBeforeUpload(t *testing.T) {
	t.Parallel()

	raw := avatarRaw()
	sender := newAvatarSender(raw)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := signal.UpdateGroupWithAvatarOnce(ctx, sender, raw, seededACI,
		signal.GroupUpdate{Avatar: &signal.GroupAvatarUpdate{Data: avatarPNG(t, 1)}}, func() {})
	if !errors.Is(err, context.Canceled) || sender.uploadCalls != 0 || sender.patchCalls != 0 {
		t.Fatalf("canceled=%v uploads=%d patches=%d", err, sender.uploadCalls, sender.patchCalls)
	}
}
