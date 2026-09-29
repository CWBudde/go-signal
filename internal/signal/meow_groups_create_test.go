//go:build cgo || libsignal_go

package signal_test

import (
	"reflect"
	"testing"

	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow"
	"github.com/google/uuid"
)

func TestNewGroup(t *testing.T) {
	t.Parallel()

	opts := signal.CreateGroupOptions{Title: "Weekend 🐶", Members: []signal.Recipient{
		{ACI: selfACI}, {ACI: aliceACI}, {ACI: aliceACI},
	}}
	raw := signal.NewGroup(opts, uuid.MustParse(selfACI))

	wantMembers := []*signalmeow.GroupMember{
		{ACI: uuid.MustParse(selfACI), Role: signalmeow.GroupMember_ADMINISTRATOR},
		{ACI: uuid.MustParse(aliceACI), Role: signalmeow.GroupMember_DEFAULT},
	}
	if raw.Title != opts.Title || !reflect.DeepEqual(raw.Members, wantMembers) {
		t.Fatalf("group = %+v", raw)
	}

	wantAccess := &signalmeow.GroupAccessControl{
		Members: signalmeow.AccessControl_MEMBER, Attributes: signalmeow.AccessControl_MEMBER,
		AddFromInviteLink: signalmeow.AccessControl_UNSATISFIABLE,
	}
	if !reflect.DeepEqual(raw.AccessControl, wantAccess) || raw.Revision != 0 {
		t.Errorf("creation defaults = %+v", raw)
	}

	_, err := signalmeow.PrepareGroupCreation(raw)
	if err != nil {
		t.Fatal(err)
	}

	group := signal.ConvertGroup(raw, selfACI)
	if group.ID == "" || group.MasterKey == "" || group.Role != signal.GroupRoleAdmin || !group.MembersCanEditAttributes {
		t.Errorf("prepared group = %+v", group)
	}
}
