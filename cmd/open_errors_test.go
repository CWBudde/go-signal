package cmd_test

import (
	"errors"
	"testing"

	"github.com/cwbudde/go-signal/internal/output"
	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/go-signal/internal/signal/signaltest"
)

var errOpen = errors.New("data dir unreadable")

// commandLines are valid invocations of every command that uses the account.
func commandLines() [][]string {
	const (
		recipient = "+15550101"
		group     = "Z3JvdXAtaWQtZ3JvdXAtaWQtZ3JvdXAtaWQtZ3JvdXA="
	)

	return [][]string{
		{accountCmd, showCmd},
		{accountCmd, "sync"},
		{accountCmd, "unlink", "--yes"},
		{"devices", listCmd},
		{contactsCmd, listCmd},
		{contactsCmd, showCmd, recipient},
		{contactsCmd, blockCmd, recipient},
		{groupsCmd, listCmd},
		{groupsCmd, showCmd, group},
		{groupsCmd, leaveCmd, group, "--yes"},
		{identitiesCmd, listCmd},
		{identitiesCmd, showCmd, recipient},
		{identitiesCmd, trustCmd, recipient},
		{"send", recipient, "-m", "hi"},
		{reactCmd, recipient, "--target", recipient + ":1790000000000", "--emoji", "👍"},
		{deleteCmd, recipient, "--target", "1790000000000"},
		{receiveCmd, "--timeout", "1ms"},
		{"link"},
	}
}

func TestCommandsReportOpenErrors(t *testing.T) {
	t.Parallel()

	for _, args := range commandLines() {
		t.Run(args[0]+" "+args[1%len(args)], func(t *testing.T) {
			t.Parallel()

			_, err := run(t, &signaltest.Fake{OpenErr: errOpen}, args...)
			if !errors.Is(err, errOpen) {
				t.Errorf("%v: got %v, want the open error", args, err)
			}
		})
	}
}

func TestCommandsRejectInvalidOutputFormat(t *testing.T) {
	t.Parallel()

	for _, args := range commandLines() {
		t.Run(args[0]+" "+args[1%len(args)], func(t *testing.T) {
			t.Parallel()

			fake := &signaltest.Fake{Linked: []signal.Account{*testAccount()}}

			_, err := run(t, fake, append([]string{"-o", "yaml"}, args...)...)
			if !errors.Is(err, output.ErrInvalidFormat) {
				t.Errorf("%v: got %v, want ErrInvalidFormat", args, err)
			}

			if len(fake.Opened()) != 0 {
				t.Errorf("%v: client opened despite the invalid format", args)
			}
		})
	}
}
