//go:build cgo

package signal_test

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/go-signal/internal/store"
	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"go.mau.fi/mautrix-signal/pkg/libsignalgo"
	"go.mau.fi/mautrix-signal/pkg/signalmeow"
	"go.mau.fi/mautrix-signal/pkg/signalmeow/types"
)

const (
	memberACI   = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	invitedACI  = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	joinerACI   = "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
	invitedPNI  = "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
	familyTitle = "Family"
	familyID    = "id-family"
)

// rawGroup is a signalmeow group of which we (seededACI) are an admin, with a member, an
// user invited at invitedAt (and one invited by PNI) and a user asking to join an hour later.
func rawGroup(invitedAt time.Time) *signalmeow.Group {
	return &signalmeow.Group{
		GroupMasterKey:               "master-key",
		GroupIdentifier:              "group-id",
		Title:                        familyTitle,
		Description:                  "All of us",
		Revision:                     7,
		DisappearingMessagesDuration: 3600,
		AnnouncementsOnly:            true,
		Members: []*signalmeow.GroupMember{
			{ACI: uuid.MustParse(seededACI), Role: signalmeow.GroupMember_ADMINISTRATOR},
			nil,
			{ACI: uuid.MustParse(memberACI), Role: signalmeow.GroupMember_DEFAULT, JoinedAtRevision: 3},
		},
		PendingMembers: []*signalmeow.PendingMember{
			{
				ServiceID:     libsignalgo.NewACIServiceID(uuid.MustParse(invitedACI)),
				Role:          signalmeow.GroupMember_DEFAULT,
				AddedByUserID: uuid.MustParse(seededACI),
				Timestamp:     uint64(invitedAt.UnixMilli()),
			},
			{ServiceID: libsignalgo.NewPNIServiceID(uuid.MustParse(invitedPNI))},
		},
		RequestingMembers: []*signalmeow.RequestingMember{
			{ACI: uuid.MustParse(joinerACI), Timestamp: uint64(invitedAt.Add(time.Hour).UnixMilli())},
		},
	}
}

func TestConvertGroup(t *testing.T) {
	t.Parallel()

	invitedAt := time.Date(2026, 9, 20, 12, 30, 0, 0, time.UTC)
	raw := rawGroup(invitedAt)
	got := signal.ConvertGroup(raw, seededACI)

	want := signal.Group{
		ID: "group-id", MasterKey: "master-key", Title: familyTitle, Description: "All of us",
		Revision: 7, Timer: time.Hour, AnnouncementsOnly: true,
		Members: []signal.GroupMember{
			{Recipient: signal.Recipient{ACI: seededACI}, Role: signal.GroupRoleAdmin},
			{Recipient: signal.Recipient{ACI: memberACI}, Role: signal.GroupRoleMember, JoinedAtRevision: 3},
		},
		Pending: []signal.PendingMember{
			{
				Recipient: signal.Recipient{ACI: invitedACI}, Role: signal.GroupRoleMember,
				AddedBy: signal.Recipient{ACI: seededACI}, InvitedAt: invitedAt,
			},
			{Recipient: signal.Recipient{PNI: invitedPNI}},
		},
		Requesting: []signal.RequestingMember{
			{Recipient: signal.Recipient{ACI: joinerACI}, RequestedAt: invitedAt.Add(time.Hour)},
		},
		Membership: signal.MembershipMember,
		Role:       signal.GroupRoleAdmin,
	}

	if fmt.Sprintf("%+v", got) != fmt.Sprintf("%+v", want) {
		t.Errorf("ConvertGroup =\n%+v\nwant\n%+v", got, want)
	}

	if got := signal.ConvertGroup(raw, invitedACI); got.Membership != signal.MembershipPending {
		t.Errorf("invited: membership %v", got.Membership)
	}

	if got := signal.ConvertGroup(raw, joinerACI); got.Membership != signal.MembershipRequesting {
		t.Errorf("requesting: membership %v", got.Membership)
	}
}

func TestGroupIDFromMasterKey(t *testing.T) {
	t.Parallel()

	masterKey := randomKey(t)

	groupID, err := signal.GroupIDFromMasterKey(masterKey)
	if err != nil {
		t.Fatalf("GroupIDFromMasterKey: %v", err)
	}

	raw, err := libsignalgo.GroupMasterKey(masterKey).GroupIdentifier()
	if err != nil {
		t.Fatal(err)
	}

	if want := base64.StdEncoding.EncodeToString(raw[:]); groupID != want {
		t.Errorf("ID = %s, want %s", groupID, want)
	}

	if again, _ := signal.GroupIDFromMasterKey(masterKey); again != groupID {
		t.Errorf("derivation isn't deterministic: %s, %s", groupID, again)
	}

	_, err = signal.GroupIDFromMasterKey(masterKey[:16])
	if !errors.Is(err, signal.ErrUnknownGroup) {
		t.Errorf("short key: %v, want ErrUnknownGroup", err)
	}
}

func TestResolveGroupRef(t *testing.T) {
	t.Parallel()

	dataDir := seedAccount(t)
	masterKey := base64.StdEncoding.EncodeToString(randomKey(t))
	groupID := storeGroupKey(t, dataDir, masterKey)

	client, err := signal.Open(t.Context(), signal.Options{DataDir: dataDir})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer client.Close()

	for _, ref := range []string{groupID, masterKey} {
		got, err := signal.ResolveGroupRef(t.Context(), client, ref)
		if err != nil || got != groupID {
			t.Errorf("ResolveGroupRef(%s) = %s, %v; want %s", ref, got, err, groupID)
		}
	}

	unknown := base64.StdEncoding.EncodeToString(randomKey(t))
	for _, ref := range []string{unknown, "not base64!", base64.StdEncoding.EncodeToString([]byte("short"))} {
		_, err := signal.ResolveGroupRef(t.Context(), client, ref)
		if !errors.Is(err, signal.ErrUnknownGroup) {
			t.Errorf("ResolveGroupRef(%s): %v, want ErrUnknownGroup", ref, err)
		}
	}
}

func TestGroupFetchError(t *testing.T) {
	t.Parallel()

	// signalmeow reports the server's status only in the error text.
	status := func(code int) error {
		return fmt.Errorf("unexpected response status: %d", code) //nolint:err113 // signalmeow's text
	}

	tests := []struct {
		err                  error
		notAMember, notKnown bool
	}{
		{fmt.Errorf("%w for gid", signalmeow.ErrGroupMasterKeyNotFound), false, true},
		{status(403), true, false},
		{status(404), false, true},
		{status(500), false, false},
	}

	for _, test := range tests {
		got := signal.GroupFetchError("gid", test.err)

		if errors.Is(got, signal.ErrNotAMember) != test.notAMember ||
			errors.Is(got, signal.ErrUnknownGroup) != test.notKnown {
			t.Errorf("%v → %v; want not a member %v, unknown %v", test.err, got, test.notAMember, test.notKnown)
		}

		if !test.notAMember && !test.notKnown && !errors.Is(got, test.err) {
			t.Errorf("%v: cause lost in %v", test.err, got)
		}
	}
}

// leaveGroup has us (seededACI) with the role self and a plain member.
func leaveGroup(self signal.GroupRole) signal.Group {
	return signal.Group{Members: []signal.GroupMember{
		{Recipient: signal.Recipient{ACI: seededACI}, Role: self},
		{Recipient: signal.Recipient{ACI: memberACI}, Role: signal.GroupRoleMember},
	}}
}

func TestLeaveChangeMember(t *testing.T) {
	t.Parallel()

	self := uuid.MustParse(seededACI)

	// A plain member removes itself.
	change, promoted, err := signal.LeaveChange(leaveGroup(signal.GroupRoleMember), seededACI, nil)
	if err != nil || len(change.DeleteMembers) != 1 || *change.DeleteMembers[0] != self ||
		len(change.ModifyMemberRoles) != 0 || len(promoted) != 0 {
		t.Errorf("member: %+v, %v, %v", change, promoted, err)
	}

	// The only admin must promote someone.
	_, _, err = signal.LeaveChange(leaveGroup(signal.GroupRoleAdmin), seededACI, nil)
	if !errors.Is(err, signal.ErrLastAdmin) {
		t.Errorf("last admin: %v, want ErrLastAdmin", err)
	}
}

func TestLeaveChangePromotes(t *testing.T) {
	t.Parallel()

	member := uuid.MustParse(memberACI)

	// The promotion goes into the same change.
	change, promoted, err := signal.LeaveChange(leaveGroup(signal.GroupRoleAdmin), seededACI,
		[]signal.Recipient{{ACI: memberACI}})
	if err != nil || len(change.DeleteMembers) != 1 || len(change.ModifyMemberRoles) != 1 ||
		*change.ModifyMemberRoles[0] != (signalmeow.RoleMember{ACI: member, Role: signalmeow.GroupMember_ADMINISTRATOR}) ||
		len(promoted) != 1 || promoted[0].ACI != memberACI {
		t.Errorf("promote: %+v, %v, %v", change, promoted, err)
	}
}

func TestLeaveChangeInvitedOrRequesting(t *testing.T) {
	t.Parallel()

	self := uuid.MustParse(seededACI)
	ourselves := signal.Recipient{ACI: seededACI}

	// An invitation is declined.
	invited := signal.Group{Pending: []signal.PendingMember{{Recipient: ourselves}}}

	change, _, err := signal.LeaveChange(invited, seededACI, nil)
	if err != nil || len(change.DeletePendingMembers) != 1 ||
		*change.DeletePendingMembers[0] != libsignalgo.NewACIServiceID(self) || len(change.DeleteMembers) != 0 {
		t.Errorf("invited: %+v, %v", change, err)
	}

	// A join request is cancelled.
	requesting := signal.Group{Requesting: []signal.RequestingMember{{Recipient: ourselves}}}

	change, _, err = signal.LeaveChange(requesting, seededACI, nil)
	if err != nil || len(change.DeleteRequestingMembers) != 1 || *change.DeleteRequestingMembers[0] != self {
		t.Errorf("requesting: %+v, %v", change, err)
	}

	_, _, err = signal.LeaveChange(signal.Group{}, seededACI, nil)
	if !errors.Is(err, signal.ErrNotAMember) {
		t.Errorf("not a member: %v, want ErrNotAMember", err)
	}
}

func TestGroupTitles(t *testing.T) {
	t.Parallel()

	dataDir := seedAccount(t)
	putGroupRecords(t, dataDir,
		store.GroupRecord{ID: familyID, Title: familyTitle, Revision: 3},
		store.GroupRecord{ID: "id-left", Title: "Old club", LeftAt: time.Now()},
		store.GroupRecord{ID: "id-untitled"},
	)

	client, err := signal.Open(t.Context(), signal.Options{DataDir: dataDir})
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	// No connection needed.
	titles, err := client.GroupTitles(t.Context())
	want := map[string]string{familyID: familyTitle, "id-left": "Old club"}

	if err != nil || !maps.Equal(titles, want) {
		t.Errorf("GroupTitles = %v, %v; want %v", titles, err, want)
	}

	err = client.Close()
	if err != nil {
		t.Fatal(err)
	}

	_, err = client.GroupTitles(t.Context())
	if !errors.Is(err, signal.ErrClosed) {
		t.Errorf("GroupTitles after Close: %v, want ErrClosed", err)
	}
}

func TestGroupsNeedConnect(t *testing.T) {
	t.Parallel()

	client, err := signal.Open(t.Context(), signal.Options{DataDir: seedAccount(t)})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer client.Close()

	_, err = client.Groups(t.Context())
	if !errors.Is(err, signal.ErrNotConnected) {
		t.Errorf("Groups: %v, want ErrNotConnected", err)
	}

	_, err = client.Group(t.Context(), familyID)
	if !errors.Is(err, signal.ErrNotConnected) {
		t.Errorf("Group: %v, want ErrNotConnected", err)
	}

	_, err = client.LeaveGroup(t.Context(), familyID, signal.LeaveOptions{})
	if !errors.Is(err, signal.ErrNotConnected) {
		t.Errorf("LeaveGroup: %v, want ErrNotConnected", err)
	}
}

func randomKey(t *testing.T) []byte {
	t.Helper()

	key := make([]byte, 32)

	_, err := rand.Read(key)
	if err != nil {
		t.Fatal(err)
	}

	return key
}

// storeGroupKey stores the base64 masterKey in the seeded account's group store, as a sync
// would, and returns the group's ID.
func storeGroupKey(t *testing.T, dataDir, masterKey string) string {
	t.Helper()

	raw, err := base64.StdEncoding.DecodeString(masterKey)
	if err != nil {
		t.Fatal(err)
	}

	groupID, err := signal.GroupIDFromMasterKey(raw)
	if err != nil {
		t.Fatal(err)
	}

	data := openSeededStore(t, dataDir)

	device, err := data.Devices.DeviceByACI(t.Context(), uuid.MustParse(seededACI))
	if err != nil || device == nil {
		t.Fatalf("load device: %v", err)
	}

	err = device.GroupStore.StoreMasterKey(t.Context(), types.GroupIdentifier(groupID),
		types.SerializedGroupMasterKey(masterKey))
	if err != nil {
		t.Fatal(err)
	}

	return groupID
}

// putGroupRecords writes recs into the seeded account's title cache.
func putGroupRecords(t *testing.T, dataDir string, recs ...store.GroupRecord) {
	t.Helper()

	data := openSeededStore(t, dataDir)

	for _, rec := range recs {
		err := data.PutGroup(t.Context(), rec)
		if err != nil {
			t.Fatal(err)
		}
	}
}

// openSeededStore opens the seeded account's database until the end of the test.
func openSeededStore(t *testing.T, dataDir string) *store.Store {
	t.Helper()

	dir, err := store.OpenDir(dataDir, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}

	data, err := dir.OpenAccount(t.Context(), seededACI, zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = data.Close() })

	return data
}
