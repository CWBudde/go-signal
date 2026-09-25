// Package signal is the facade over signalmeow. All other packages talk to Signal through it, so
// that upstream API changes stay contained here.
package signal

import "go.mau.fi/mautrix-signal/pkg/libsignalgo/signalversion"

// LibsignalVersion is the libsignal release that the libsignalgo bindings were generated against.
// The third_party/libsignal submodule must be pinned to the same tag (see `just check-libsignal`).
const LibsignalVersion = signalversion.Version
