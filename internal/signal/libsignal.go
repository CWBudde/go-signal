//go:build cgo && !libsignal_go

package signal

// Linking libsignalgo pulls libsignal_ffi.a into every cgo build, so a missing or mismatched
// library fails at build time rather than at first use. CGO_ENABLED=0 builds skip this file.
import _ "github.com/cwbudde/mautrix-signal/pkg/libsignalgo"
