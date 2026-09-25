//go:build cgo

package signal

// Linking libsignalgo pulls libsignal_ffi.a into every cgo build, so a missing or mismatched
// library fails at build time rather than at first use. CGO_ENABLED=0 builds skip this file.
import _ "go.mau.fi/mautrix-signal/pkg/libsignalgo"
