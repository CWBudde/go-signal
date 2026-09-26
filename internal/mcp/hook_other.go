//go:build !unix

package mcp

import "os/exec"

// killGroup leaves cmd as it is: a cancelled run kills only the process itself.
func killGroup(*exec.Cmd) {}
