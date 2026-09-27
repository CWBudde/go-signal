// Package cpu reports the CPU features go-signal's security posture depends on.
package cpu

import (
	"runtime"

	"golang.org/x/sys/cpu"
)

// HasAESHardware reports whether Go's crypto/aes and AES-GCM run on the CPU's AES instructions
// on this machine. Without them the standard library uses a table-based AES whose timing depends
// on the key and data (docs/constant-time-review.md, CT-02). It mirrors the selection in
// crypto/internal/fips140/aes (Go 1.26): AES-NI with SSE4.1, SSSE3 and PCLMULQDQ on amd64, AES
// and PMULL on arm64, CPACF on s390x, and POWER8 on ppc64 and ppc64le. Other architectures have
// no AES assembly in Go.
func HasAESHardware() bool {
	switch runtime.GOARCH {
	case "amd64":
		return x86HasAES()
	case "arm64":
		return cpu.ARM64.HasAES && cpu.ARM64.HasPMULL
	case "s390x":
		return cpu.S390X.HasAES && cpu.S390X.HasAESCBC
	case "ppc64", "ppc64le":
		return true
	default:
		return false
	}
}

func x86HasAES() bool {
	return cpu.X86.HasAES && cpu.X86.HasPCLMULQDQ && cpu.X86.HasSSE41 && cpu.X86.HasSSSE3
}
