package cmd_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/cwbudde/go-signal/cmd"
	"github.com/cwbudde/go-signal/internal/signal"
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

	if !strings.Contains(out.String(), "libsignal: "+signal.LibsignalVersion+"\n") {
		t.Errorf("missing libsignal version: %q", out.String())
	}

	if !strings.Contains(out.String(), "signalmeow: "+signal.SignalmeowVersion()+"\n") {
		t.Errorf("missing signalmeow version: %q", out.String())
	}

	// Only the purego build runs on libsignal-go and reports its version.
	if version := signal.LibsignalGoVersion(); version != "" {
		if !strings.Contains(out.String(), "libsignal-go: "+version+"\n") {
			t.Errorf("missing libsignal-go version: %q", out.String())
		}
	} else if strings.Contains(out.String(), "libsignal-go:") {
		t.Errorf("libsignal-go version in a cgo build: %q", out.String())
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

func TestReceiveWithoutAccount(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	root := cmd.NewRootCmd()
	root.SetArgs([]string{"receive", "--data-dir", t.TempDir(), "--timeout", "5s"})

	err := root.Execute()
	if !errors.Is(err, signal.ErrNotLinked) && !errors.Is(err, signal.ErrCGORequired) {
		t.Fatalf("got %v, want ErrNotLinked (cgo) or ErrCGORequired (no cgo)", err)
	}
}
