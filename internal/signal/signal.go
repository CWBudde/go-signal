// Package signal is the facade over signalmeow. All other packages talk to Signal through it, so
// that upstream API changes stay contained here.
package signal

import (
	"runtime/debug"

	"go.mau.fi/mautrix-signal/pkg/libsignalgo/signalversion"
)

// LibsignalVersion is the libsignal release that the libsignalgo bindings were generated against.
// The third_party/libsignal submodule must be pinned to the same tag (see `just check-libsignal`).
const LibsignalVersion = signalversion.Version

const (
	signalmeowModule = "go.mau.fi/mautrix-signal"
	unknownVersion   = "unknown"
)

// SignalmeowVersion returns the version of the mautrix-signal module (which provides signalmeow)
// that this binary was built with, or "unknown" when the build info doesn't record it.
func SignalmeowVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return unknownVersion
	}

	for _, dep := range info.Deps {
		if dep.Path != signalmeowModule {
			continue
		}

		if dep.Replace != nil {
			return dep.Replace.Version + " (replaced by " + dep.Replace.Path + ")"
		}

		return dep.Version
	}

	return unknownVersion
}
