package cmd

import "os"

// SignalExitCode exposes signalExitCode to the cmd_test package.
func SignalExitCode(sig os.Signal) int {
	return signalExitCode(sig)
}
