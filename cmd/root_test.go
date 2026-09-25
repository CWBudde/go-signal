package cmd_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/cwbudde/go-signal/cmd"
)

func TestVersionCommand(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	var out bytes.Buffer

	root := cmd.NewRootCmd()
	root.SetOut(&out)
	root.SetArgs([]string{"version"})

	err := root.Execute()
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	if !strings.HasPrefix(out.String(), "go-signal ") {
		t.Errorf("unexpected output: %q", out.String())
	}
}

func TestInvalidLogFormat(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("GOSIGNAL_LOG_FORMAT", "xml")

	root := cmd.NewRootCmd()
	root.SetArgs([]string{"version"})

	err := root.Execute()
	if err == nil {
		t.Fatal("expected error for invalid log format")
	}
}
