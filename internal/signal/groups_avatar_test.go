package signal_test

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"math"
	"reflect"
	"slices"
	"testing"

	"github.com/cwbudde/go-signal/internal/signal"
)

func avatarPNG(t *testing.T, width int) []byte {
	t.Helper()

	var buf bytes.Buffer

	img := image.NewRGBA(image.Rect(0, 0, width, 1))
	img.Set(0, 0, color.White)

	err := png.Encode(&buf, img)
	if err != nil {
		t.Fatal(err)
	}

	return buf.Bytes()
}

func TestGroupAvatarValidation(t *testing.T) {
	t.Parallel()
	pngData := avatarPNG(t, 1)

	var jpegBuf, gifBuf bytes.Buffer

	img := image.NewRGBA(image.Rect(0, 0, 1, 1))

	err := jpeg.Encode(&jpegBuf, img, nil)
	if err != nil {
		t.Fatal(err)
	}

	err = gif.Encode(&gifBuf, img, nil)
	if err != nil {
		t.Fatal(err)
	}

	truncated := pngData[:len(pngData)-12]

	_, _, err = image.DecodeConfig(bytes.NewReader(truncated))
	if err != nil {
		t.Fatalf("truncation fixture: %v", err)
	}

	for _, test := range []struct {
		name   string
		avatar signal.GroupAvatarUpdate
		valid  bool
	}{
		{"png", signal.GroupAvatarUpdate{Data: pngData}, true},
		{"jpeg", signal.GroupAvatarUpdate{Data: jpegBuf.Bytes()}, true},
		{"remove", signal.GroupAvatarUpdate{Remove: true}, true},
		{"empty avatar operation", signal.GroupAvatarUpdate{}, false},
		{"mixed avatar operation", signal.GroupAvatarUpdate{Data: pngData, Remove: true}, false},
		{"gif", signal.GroupAvatarUpdate{Data: gifBuf.Bytes()}, false},
		{"truncated", signal.GroupAvatarUpdate{Data: truncated}, false},
		{"max bytes", signal.GroupAvatarUpdate{
			Data: append(bytes.Clone(pngData), make([]byte, signal.MaxGroupAvatarSize-len(pngData))...),
		}, true},
		{"oversize", signal.GroupAvatarUpdate{
			Data: append(bytes.Clone(pngData), make([]byte, signal.MaxGroupAvatarSize)...),
		}, false},
		{"max dimension", signal.GroupAvatarUpdate{Data: avatarPNG(t, signal.MaxGroupAvatarDimension)}, true},
		{"dimension overflow", signal.GroupAvatarUpdate{Data: avatarPNG(t, signal.MaxGroupAvatarDimension+1)}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			err := (signal.GroupUpdate{Avatar: &test.avatar}).Check()
			if (err == nil) != test.valid || (!test.valid && !errors.Is(err, signal.ErrInvalidGroupUpdate)) {
				t.Fatalf("Check = %v, valid=%v", err, test.valid)
			}
		})
	}
}

func TestGroupAvatarPolicy(t *testing.T) { //nolint:cyclop,funlen // policy and ownership assertions
	t.Parallel()

	group := removalGroup()
	group.AvatarPath = "existing"
	data := avatarPNG(t, 1)
	set := signal.GroupUpdate{Avatar: &signal.GroupAvatarUpdate{Data: data}, Description: new(group.Description)}

	next, err := group.WithUpdate(selfACI, set)
	if err != nil || next.Revision != group.Revision+1 || next.AvatarPath != group.AvatarPath {
		t.Fatalf("set = %+v,%v", next, err)
	}

	next, err = group.WithUpdate(selfACI, signal.GroupUpdate{
		Avatar: &signal.GroupAvatarUpdate{Remove: true}, Description: new("new"),
	})
	if err != nil || next.Revision != group.Revision+1 || next.AvatarPath != "" || next.Description != "new" {
		t.Fatalf("combined removal = %+v,%v", next, err)
	}

	_, err = group.WithUpdate(aliceACI, signal.GroupUpdate{Avatar: &signal.GroupAvatarUpdate{Remove: true}})
	if !errors.Is(err, signal.ErrGroupPermission) {
		t.Fatalf("member removal = %v", err)
	}

	group.MembersCanEditAttributes = true

	_, err = group.WithUpdate(aliceACI, set)
	if err != nil {
		t.Fatal(err)
	}

	_, err = group.WithUpdate(aliceACI, signal.GroupUpdate{Avatar: set.Avatar, MembersCanEditAttributes: new(false)})
	if !errors.Is(err, signal.ErrGroupPermission) {
		t.Fatalf("mixed permissions = %v", err)
	}

	group.Revision = math.MaxUint32

	_, err = group.WithUpdate(selfACI, set)
	if !errors.Is(err, signal.ErrUnknownGroup) {
		t.Fatalf("overflow = %v", err)
	}

	group.AvatarPath = ""
	group.Banned = []signal.BannedMember{{Recipient: signal.Recipient{ACI: bobACI}}}

	next, err = group.WithUpdate(selfACI, signal.GroupUpdate{Avatar: &signal.GroupAvatarUpdate{Remove: true}})
	if err != nil || next.Revision != group.Revision {
		t.Fatalf("absent removal = %+v,%v", next, err)
	}

	before := group
	before.Members = slices.Clone(group.Members)
	next.Members[0].Role = signal.GroupRoleMember
	next.Pending[0].Role = signal.GroupRoleMember
	next.Requesting[0].Recipient.ACI = "changed-avatar-requester"
	next.Banned[0].Recipient.ACI = "changed-avatar-ban"

	if !reflect.DeepEqual(group.Members, before.Members) ||
		group.Pending[0].Role != signal.GroupRoleAdmin ||
		group.Requesting[0].Recipient.ACI == "changed-avatar-requester" || group.Banned[0].Recipient.ACI != bobACI {
		t.Fatal("policy aliases original slices")
	}
}
