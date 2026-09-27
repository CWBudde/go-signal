package cpu_test

import (
	"runtime"
	"testing"

	"github.com/cwbudde/go-signal/internal/cpu"
	syscpu "golang.org/x/sys/cpu"
)

// TestHasAESHardware checks HasAESHardware against x/sys/cpu on the host architecture.
func TestHasAESHardware(t *testing.T) {
	t.Parallel()

	want := map[string]bool{
		"amd64": syscpu.X86.HasAES && syscpu.X86.HasPCLMULQDQ && syscpu.X86.HasSSE41 && syscpu.X86.HasSSSE3,
		"arm64": syscpu.ARM64.HasAES && syscpu.ARM64.HasPMULL,
		"s390x": syscpu.S390X.HasAES && syscpu.S390X.HasAESCBC,
		"ppc64": true, "ppc64le": true,
	}[runtime.GOARCH]

	if got := cpu.HasAESHardware(); got != want {
		t.Fatalf("HasAESHardware() = %v, want %v on %s", got, want, runtime.GOARCH)
	}
}
