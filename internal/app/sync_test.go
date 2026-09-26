package app_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/go-signal/internal/signal/signaltest"
)

func TestSync(t *testing.T) {
	t.Parallel()

	synced := signal.SyncResult{Contacts: 12, Groups: 3, MasterKey: true, Storage: true, ContactList: true}
	fake := &signaltest.Fake{Linked: []signal.Account{testAccount()}, SyncResult: synced}

	var stages []signal.SyncStage

	res, err := open(t, fake).Sync(t.Context(), app.SyncRequest{
		Timeout:  time.Minute,
		Progress: func(stage signal.SyncStage) { stages = append(stages, stage) },
	})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}

	if res.SyncResult != synced || res.Incomplete != nil {
		t.Errorf("Sync = %+v, want %+v and complete", res, synced)
	}

	if len(stages) == 0 || stages[len(stages)-1] != signal.SyncDone {
		t.Errorf("progress = %v, want stages ending with done", stages)
	}

	aci := testAccount().ACI
	if got := fake.Syncs(); !slices.Equal(got, []string{aci}) {
		t.Errorf("syncs = %v, want one for %s", got, aci)
	}

	if got := fake.Connects(); len(got) != 1 {
		t.Errorf("connected %d times, want once", len(got))
	}
}

func TestSyncIncomplete(t *testing.T) {
	t.Parallel()

	partial := signal.SyncResult{Contacts: 4, MasterKey: true, Storage: true}
	fake := &signaltest.Fake{
		Linked:     []signal.Account{testAccount()},
		SyncResult: partial,
		SyncErr:    fmt.Errorf("%w: %w", signal.ErrSyncIncomplete, context.DeadlineExceeded),
	}

	res, err := open(t, fake).Sync(t.Context(), app.SyncRequest{Timeout: time.Minute})
	if err != nil {
		t.Fatalf("an incomplete sync is not an error: %v", err)
	}

	if res.SyncResult != partial {
		t.Errorf("result = %+v, want %+v", res.SyncResult, partial)
	}

	if !errors.Is(res.Incomplete, signal.ErrSyncIncomplete) || !errors.Is(res.Incomplete, context.DeadlineExceeded) {
		t.Errorf("Incomplete = %v, want ErrSyncIncomplete with the timeout", res.Incomplete)
	}

	if got := res.Missing(); !slices.Equal(got, []string{"contact list"}) {
		t.Errorf("Missing = %v", got)
	}
}

func TestSyncErrors(t *testing.T) {
	t.Parallel()

	unlinked := testAccount()
	unlinked.UnlinkedAt = time.Date(2026, 9, 25, 8, 0, 0, 0, time.UTC)

	tests := []struct {
		name string
		fake *signaltest.Fake
		want error
	}{
		{"not linked", &signaltest.Fake{}, signal.ErrNotLinked},
		{"unlinked", &signaltest.Fake{Linked: []signal.Account{unlinked}}, signal.ErrDeviceUnlinked},
		{"in use", &signaltest.Fake{Linked: []signal.Account{testAccount()}, InUse: true}, signal.ErrAccountInUse},
		{"connect", &signaltest.Fake{Linked: []signal.Account{testAccount()}, ConnectErr: errBoom}, errBoom},
		{"sync", &signaltest.Fake{Linked: []signal.Account{testAccount()}, SyncErr: errBoom}, errBoom},
		{"logged out", &signaltest.Fake{
			Linked:   []signal.Account{testAccount()},
			Incoming: []signal.Event{&signal.Connection{State: signal.StateLoggedOut}},
		}, signal.ErrDeviceUnlinked},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, err := open(t, test.fake).Sync(t.Context(), app.SyncRequest{})
			if !errors.Is(err, test.want) {
				t.Fatalf("Sync error = %v, want %v", err, test.want)
			}

			if !strings.HasPrefix(err.Error(), "sync: ") {
				t.Errorf("error %q doesn't name the command", err)
			}
		})
	}
}
