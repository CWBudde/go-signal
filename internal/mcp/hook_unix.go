//go:build unix

package mcp

import (
	"os/exec"
	"syscall"
)

// killGroup runs cmd in its own process group and has a cancelled run kill the whole group, so
// that nothing the hook started outlives it.
func killGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
