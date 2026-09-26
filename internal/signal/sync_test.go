package signal_test

import (
	"slices"
	"testing"

	"github.com/cwbudde/go-signal/internal/signal"
)

func TestSyncResultMissing(t *testing.T) {
	t.Parallel()

	tests := []struct {
		res  signal.SyncResult
		want []string
	}{
		{signal.SyncResult{MasterKey: true, Storage: true, ContactList: true}, nil},
		{signal.SyncResult{MasterKey: true, Storage: true}, []string{"contact list"}},
		{signal.SyncResult{ContactList: true}, []string{"storage key", "storage service"}},
		{signal.SyncResult{}, []string{"storage key", "storage service", "contact list"}},
	}

	for _, test := range tests {
		if got := test.res.Missing(); !slices.Equal(got, test.want) {
			t.Errorf("%+v.Missing() = %v, want %v", test.res, got, test.want)
		}

		if got := test.res.Complete(); got != (test.want == nil) {
			t.Errorf("%+v.Complete() = %v", test.res, got)
		}
	}
}

func TestSyncOptionsReport(t *testing.T) {
	t.Parallel()

	signal.SyncOptions{}.Report(signal.SyncDone) // no Progress: nothing happens

	var got []signal.SyncStage

	opts := signal.SyncOptions{Progress: func(stage signal.SyncStage) { got = append(got, stage) }}
	for stage := signal.SyncRequestingContacts; stage <= signal.SyncDone; stage++ {
		opts.Report(stage)

		if stage.String() == signal.SyncStage(0).String() {
			t.Errorf("stage %d has no description", stage)
		}
	}

	if len(got) != int(signal.SyncDone) {
		t.Errorf("reported %v", got)
	}
}
