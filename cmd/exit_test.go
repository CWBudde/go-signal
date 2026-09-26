package cmd_test

import (
	"os"
	"syscall"
	"testing"

	"github.com/cwbudde/go-signal/cmd"
)

// otherSignal is an os.Signal that is not a syscall.Signal.
type otherSignal struct{}

func (otherSignal) String() string { return "other" }
func (otherSignal) Signal()        {}

func TestSignalExitCode(t *testing.T) {
	t.Parallel()

	for sig, want := range map[os.Signal]int{
		syscall.SIGINT:  130,
		syscall.SIGTERM: 143,
		otherSignal{}:   cmd.ExitFailure,
	} {
		if got := cmd.SignalExitCode(sig); got != want {
			t.Errorf("SignalExitCode(%v) = %d, want %d", sig, got, want)
		}
	}
}
