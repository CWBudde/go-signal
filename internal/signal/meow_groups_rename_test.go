//go:build cgo || libsignal_go

package signal_test

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow"
)

func TestRenameChange(t *testing.T) {
	t.Parallel()

	group := signal.ConvertGroup(rawGroup(time.Time{}), seededACI)
	title := renameTitle

	change, err := signal.RenameChange(group, seededACI, title)
	if err != nil || !reflect.DeepEqual(change, &signalmeow.GroupChange{ModifyTitle: &title}) {
		t.Fatalf("RenameChange = %+v, %v", change, err)
	}

	change, err = signal.RenameChange(group, seededACI, group.Title)
	if err != nil || change != nil {
		t.Errorf("unchanged title = %+v, %v", change, err)
	}

	change, err = signal.RenameChange(group, memberACI, title)
	if !errors.Is(err, signal.ErrGroupPermission) || change != nil {
		t.Errorf("forbidden rename = %+v, %v", change, err)
	}
}

func TestConvertGroupEditPermission(t *testing.T) {
	t.Parallel()

	for _, access := range []signalmeow.AccessControl{
		signalmeow.AccessControl_UNKNOWN, signalmeow.AccessControl_ANY,
		signalmeow.AccessControl_MEMBER, signalmeow.AccessControl_ADMINISTRATOR,
		signalmeow.AccessControl_UNSATISFIABLE,
	} {
		raw := rawGroup(time.Time{})
		raw.AccessControl = &signalmeow.GroupAccessControl{Attributes: access}

		group := signal.ConvertGroup(raw, memberACI)
		if group.MembersCanEditAttributes != (access == signalmeow.AccessControl_MEMBER) {
			t.Errorf("access %v: members can edit = %v", access, group.MembersCanEditAttributes)
		}
	}
}
