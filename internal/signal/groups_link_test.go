package signal_test

import (
	"bytes"
	"encoding/base64"
	"errors"
	"math"
	"os"
	"strings"
	"testing"

	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow/protobuf/signalpb"
	"google.golang.org/protobuf/proto"
)

const (
	linkPassword  = "AQEBAQEBAQEBAQEBAQEBAQ=="
	linkMasterKey = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
)

func linkGroup() signal.Group {
	group := removalGroup()
	group.ID = "group-link-policy-id"
	group.MasterKey = linkMasterKey

	return group
}

func TestGroupLinkUpdateValidation(t *testing.T) {
	t.Parallel()

	for _, update := range []signal.GroupLinkUpdate{
		{},
		{State: new(signal.GroupLinkUnknown)},
		{State: new(signal.GroupLinkState("not-an-invite-state"))},
	} {
		if !errors.Is(update.Check(), signal.ErrInvalidGroupLinkUpdate) {
			t.Fatalf("invalid update accepted: %+v", update)
		}
	}

	for _, update := range []signal.GroupLinkUpdate{
		{Reset: true},
		{State: new(signal.GroupLinkDisabled)},
		{State: new(signal.GroupLinkEnabled)},
		{State: new(signal.GroupLinkApproval), Reset: true},
	} {
		err := update.Check()
		if err != nil {
			t.Fatal(err)
		}
	}
}

//nolint:cyclop // behavioral matrix
func TestGroupLinkViewAndSecrets(t *testing.T) {
	t.Parallel()

	for _, state := range []signal.GroupLinkState{
		signal.GroupLinkDisabled,
		signal.GroupLinkUnknown,
		signal.GroupLinkEnabled,
		signal.GroupLinkApproval,
	} {
		got, err := linkGroup().Link(aliceACI, state, linkPassword)
		if err != nil || got.ID != "group-link-policy-id" || got.Revision != 9 || got.State != state {
			t.Fatalf("link view %+v,%v", got, err)
		}

		if state == signal.GroupLinkDisabled || state == signal.GroupLinkUnknown {
			if got.URL != "" {
				t.Fatal("inactive URL exposed")
			}

			continue
		}

		assertLinkSecrets(t, got.URL)
	}

	for _, self := range []string{bobACI, requesterACI, "not-a-group-member"} {
		_, err := linkGroup().Link(self, signal.GroupLinkDisabled, linkPassword)
		if !errors.Is(err, signal.ErrNotAMember) {
			t.Fatalf("nonmember link %v", err)
		}
	}
}

func assertLinkSecrets(t *testing.T, url string) {
	t.Helper()

	if !strings.HasPrefix(url, "https://signal.group/#") {
		t.Fatalf("URL protocol %q", url)
	}

	data, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(url, "https://signal.group/#"))
	if err != nil {
		t.Fatal(err)
	}

	message := &signalpb.GroupInviteLink{}

	err = proto.Unmarshal(data, message)
	if err != nil {
		t.Fatal(err)
	}

	contents := message.GetContentsV1()
	if !bytes.Equal(contents.GetGroupMasterKey(), make([]byte, 32)) ||
		!bytes.Equal(contents.GetInviteLinkPassword(), bytes.Repeat([]byte{1}, 16)) {
		t.Fatal("wrong invite secrets")
	}
}

//nolint:cyclop,funlen // behavioral matrix
func TestWithLinkUpdatePolicy(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name                 string
		old, want            signal.GroupLinkState
		update               signal.GroupLinkUpdate
		changed, newPassword bool
	}{
		{
			"enable",
			signal.GroupLinkDisabled,
			signal.GroupLinkEnabled,
			signal.GroupLinkUpdate{State: new(signal.GroupLinkEnabled)},
			true,
			false,
		},
		{
			"approval",
			signal.GroupLinkEnabled,
			signal.GroupLinkApproval,
			signal.GroupLinkUpdate{State: new(signal.GroupLinkApproval)},
			true,
			false,
		},
		{
			"disable",
			signal.GroupLinkApproval,
			signal.GroupLinkDisabled,
			signal.GroupLinkUpdate{State: new(signal.GroupLinkDisabled)},
			true,
			false,
		},
		{
			"same access",
			signal.GroupLinkEnabled,
			signal.GroupLinkEnabled,
			signal.GroupLinkUpdate{State: new(signal.GroupLinkEnabled)},
			false,
			false,
		},
		{
			"reset disabled",
			signal.GroupLinkDisabled,
			signal.GroupLinkDisabled,
			signal.GroupLinkUpdate{Reset: true},
			true,
			true,
		},
		{"reset unknown", signal.GroupLinkUnknown, signal.GroupLinkUnknown, signal.GroupLinkUpdate{Reset: true}, true, true},
		{
			"reset and state",
			signal.GroupLinkEnabled,
			signal.GroupLinkApproval,
			signal.GroupLinkUpdate{State: new(signal.GroupLinkApproval), Reset: true},
			true,
			true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			group := linkGroup()
			got, password, err := group.WithLinkUpdate(selfACI, test.old, linkPassword, test.update)

			wantRevision := uint32(9)
			if test.changed {
				wantRevision++
			}

			if err != nil || got.State != test.want || got.Revision != wantRevision || group.Revision != 9 {
				t.Fatalf("update %+v,%v", got, err)
			}

			decoded, decodeErr := base64.StdEncoding.DecodeString(password)
			if decodeErr != nil || len(decoded) != 16 || (password != linkPassword) != test.newPassword {
				t.Fatal("password update wrong")
			}
		})
	}

	for _, self := range []string{aliceACI, bobACI, requesterACI} {
		_, _, err := linkGroup().WithLinkUpdate(
			self,
			signal.GroupLinkDisabled,
			linkPassword,
			signal.GroupLinkUpdate{State: new(signal.GroupLinkDisabled)},
		)

		want := signal.ErrNotAMember
		if self == aliceACI {
			want = signal.ErrGroupPermission
		}

		if !errors.Is(err, want) {
			t.Fatalf("unauthorized noop %v", err)
		}
	}

	group := linkGroup()
	group.Revision = math.MaxUint32

	_, _, err := group.WithLinkUpdate(selfACI, signal.GroupLinkDisabled, linkPassword, signal.GroupLinkUpdate{Reset: true})
	if !errors.Is(err, signal.ErrUnknownGroup) {
		t.Fatalf("revision overflow %v", err)
	}

	got, _, err := group.WithLinkUpdate(
		selfACI,
		signal.GroupLinkDisabled,
		linkPassword,
		signal.GroupLinkUpdate{State: new(signal.GroupLinkDisabled)},
	)
	if err != nil || got.Revision != math.MaxUint32 {
		t.Fatalf("overflow noop %+v,%v", got, err)
	}
}

//nolint:cyclop,funlen // malformed secret matrix
func TestGroupLinkMalformedSecretsNeverDisclosed(t *testing.T) {
	t.Parallel()

	for _, secret := range []string{
		"sensitive-invalid-base64-secret",
		base64.StdEncoding.EncodeToString(make([]byte, 15)),
		base64.StdEncoding.EncodeToString(make([]byte, 17)),
	} {
		_, err := linkGroup().Link(selfACI, signal.GroupLinkEnabled, secret)
		if err == nil || strings.Contains(err.Error(), secret) {
			t.Fatalf("password error disclosed or absent: %v", err)
		}

		_, _, err = linkGroup().WithLinkUpdate(
			selfACI,
			signal.GroupLinkDisabled,
			secret,
			signal.GroupLinkUpdate{State: new(signal.GroupLinkEnabled)},
		)
		if err == nil || strings.Contains(err.Error(), secret) {
			t.Fatalf("preserved malformed password %v", err)
		}

		got, password, err := linkGroup().WithLinkUpdate(
			selfACI,
			signal.GroupLinkEnabled,
			secret,
			signal.GroupLinkUpdate{Reset: true},
		)
		if err != nil || password == secret || got.URL == "" {
			t.Fatalf("reset did not repair %v", err)
		}

		got, err = linkGroup().Link(selfACI, signal.GroupLinkDisabled, secret)
		if err != nil || got.URL != "" {
			t.Fatalf("inactive secret %+v,%v", got, err)
		}
	}

	for _, secret := range []string{
		"sensitive-invalid-key",
		base64.StdEncoding.EncodeToString(make([]byte, 31)),
		base64.StdEncoding.EncodeToString(make([]byte, 33)),
	} {
		group := linkGroup()
		group.MasterKey = secret

		_, err := group.Link(selfACI, signal.GroupLinkEnabled, linkPassword)
		if err == nil || strings.Contains(err.Error(), secret) {
			t.Fatalf("master key error %v", err)
		}

		_, _, err = group.WithLinkUpdate(selfACI, signal.GroupLinkDisabled, linkPassword, signal.GroupLinkUpdate{Reset: true})
		if err == nil || strings.Contains(err.Error(), secret) {
			t.Fatalf("write key error %v", err)
		}
	}

	got, password, err := linkGroup().WithLinkUpdate(
		selfACI,
		signal.GroupLinkDisabled,
		"",
		signal.GroupLinkUpdate{State: new(signal.GroupLinkEnabled)},
	)

	decoded, decodeErr := base64.StdEncoding.DecodeString(password)
	if err != nil || decodeErr != nil || len(decoded) != 16 || got.URL == "" {
		t.Fatalf("first enable %+v,%v", got, err)
	}
}

func TestGroupLinkReferenceRedactionPreservesErrorIdentity(t *testing.T) {
	t.Parallel()

	cause := &os.PathError{Op: "resolve", Path: "private-group-link-reference", Err: signal.ErrUnknownGroup}
	err := signal.GroupLinkReferenceError(cause)

	var got *os.PathError
	if !errors.Is(err, signal.ErrUnknownGroup) || !errors.As(err, &got) || got != cause ||
		strings.Contains(err.Error(), cause.Path) {
		t.Fatalf("redaction lost identity or leaked reference: %v", err)
	}
}
