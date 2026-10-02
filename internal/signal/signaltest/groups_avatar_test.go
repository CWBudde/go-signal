package signaltest_test

import (
	"bytes"
	"errors"
	"image"
	"image/png"
	"io"
	"reflect"
	"testing"

	"github.com/cwbudde/go-signal/internal/signal"
)

func fakeAvatar(t *testing.T) []byte {
	t.Helper()

	var buf bytes.Buffer

	err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 1, 1)))
	if err != nil {
		t.Fatal(err)
	}

	return buf.Bytes()
}

func TestFakeGroupAvatarLifecycle(t *testing.T) { //nolint:cyclop,funlen // acceptance and ownership
	t.Parallel()

	fake := settingsFake()
	cli := profileClient(t, fake, profileBobACI, true)
	data := fakeAvatar(t)
	update := signal.GroupUpdate{Avatar: &signal.GroupAvatarUpdate{Data: data}, Description: new("avatar description")}
	before := fake.GroupInfo[roleFakeGroupID]
	fake.GroupAvatarUploadErr = io.ErrClosedPipe

	_, err := cli.UpdateGroup(t.Context(), roleFakeGroupID, update)
	if !errors.Is(err, io.ErrClosedPipe) || !reflect.DeepEqual(before, fake.GroupInfo[roleFakeGroupID]) {
		t.Fatalf("upload failure = %+v,%v", fake.GroupInfo, err)
	}

	fake.GroupAvatarUploadErr = nil
	fake.UpdateGroupErr = signal.ErrGroupChanged

	_, err = cli.UpdateGroup(t.Context(), roleFakeGroupID, update)
	if !errors.Is(err, signal.ErrGroupChanged) || !reflect.DeepEqual(before, fake.GroupInfo[roleFakeGroupID]) ||
		len(fake.GroupAvatarData) != 0 {
		t.Fatalf("patch failure = %+v,%v", fake.GroupInfo, err)
	}

	fake.UpdateGroupErr = nil

	got, err := cli.UpdateGroup(t.Context(), "key", update)
	if err != nil || got.AvatarPath != "fake-group-avatar-8" || got.Revision != 8 ||
		got.Description != "avatar description" {
		t.Fatalf("set = %+v,%v", got, err)
	}

	want := bytes.Clone(data)
	data[0] ^= 1

	if !bytes.Equal(fake.GroupAvatarData[roleFakeGroupID], want) {
		t.Fatal("stored bytes alias request")
	}

	got.Members[0].Role = signal.GroupRoleMember
	if fake.GroupInfo[roleFakeGroupID].Members[0].Role != signal.GroupRoleAdmin {
		t.Fatal("result aliases state")
	}

	fake.GroupUpdateFollowUpErr = io.ErrClosedPipe

	got, err = cli.UpdateGroup(t.Context(), roleFakeGroupID,
		signal.GroupUpdate{Avatar: &signal.GroupAvatarUpdate{Data: want}})
	if !errors.Is(err, io.ErrClosedPipe) || got.ID != roleFakeGroupID || got.Revision != 9 || got.AvatarPath != "" {
		t.Fatalf("accepted partial = %+v,%v", got, err)
	}

	fake.GroupUpdateFollowUpErr = nil

	got, err = cli.UpdateGroup(t.Context(), roleFakeGroupID,
		signal.GroupUpdate{Avatar: &signal.GroupAvatarUpdate{Remove: true}})
	if err != nil || got.Revision != 10 || got.AvatarPath != "" ||
		len(fake.GroupAvatarData) != 0 {
		t.Fatalf("remove = %+v,%v", got, err)
	}

	fake.UpdateGroupErr = signal.ErrGroupChanged

	got, err = cli.UpdateGroup(t.Context(), roleFakeGroupID,
		signal.GroupUpdate{Avatar: &signal.GroupAvatarUpdate{Remove: true}})
	if err != nil || got.Revision != 10 {
		t.Fatalf("remove noop = %+v,%v", got, err)
	}

	fake.GroupInfo[roleFakeGroupID] = signal.Group{ID: roleFakeGroupID, Members: before.Members}

	_, err = cli.UpdateGroup(t.Context(), roleFakeGroupID,
		signal.GroupUpdate{Avatar: &signal.GroupAvatarUpdate{Remove: true}})
	if !errors.Is(err, signal.ErrGroupPermission) {
		t.Fatalf("fresh permission noop = %v", err)
	}
}

func TestFakeInvalidAvatarBeforeLifecycle(t *testing.T) {
	t.Parallel()

	fake := settingsFake()
	cli := profileClient(t, fake, profileAliceACI, false)

	_, err := cli.UpdateGroup(t.Context(), roleFakeGroupID,
		signal.GroupUpdate{Avatar: &signal.GroupAvatarUpdate{Data: []byte("bad")}})
	if !errors.Is(err, signal.ErrInvalidGroupUpdate) {
		t.Fatalf("invalid before connect = %v", err)
	}
}
