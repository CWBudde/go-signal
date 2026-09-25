package signal_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cwbudde/go-signal/internal/signal"
)

// TestLibsignalSubmoduleVersion guards against building libsignal_ffi.a from a different
// libsignal release than the one the libsignalgo bindings were generated against.
func TestLibsignalSubmoduleVersion(t *testing.T) {
	t.Parallel()

	dir := filepath.Join("..", "..", "third_party", "libsignal")

	_, err := os.Stat(filepath.Join(dir, ".git"))
	if err != nil {
		t.Skipf("libsignal submodule not checked out: %v", err)
	}

	head := revParse(t, dir, "HEAD")

	// Shallow clones carry no tags; `just check-libsignal` fetches the expected one.
	tag := revParse(t, dir, signal.LibsignalVersion+"^{commit}")
	if tag == "" {
		t.Skipf("tag %s not fetched; run `just check-libsignal`", signal.LibsignalVersion)
	}

	if head != tag {
		t.Errorf("third_party/libsignal is at %s, libsignalgo expects %s (%s)",
			head, signal.LibsignalVersion, tag)
	}
}

// revParse resolves rev in the git repository at dir, returning "" if it does not exist.
func revParse(t *testing.T, dir, rev string) string {
	t.Helper()

	out, err := exec.CommandContext(t.Context(), "git", "-C", dir, "rev-parse", "-q", "--verify", rev).
		Output()
	if err != nil {
		return ""
	}

	return strings.TrimSpace(string(out))
}
