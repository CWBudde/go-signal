//go:build libsignal_go

package signal

// Backend names the implementation of the Signal protocol this binary was built with: the
// build tag of the pure-Go backend.
const Backend = "libsignal_go"

const libsignalGoModule = "github.com/cwbudde/libsignal-go"

// LibsignalGoVersion returns the version of libsignal-go, which the purego build of libsignalgo
// runs on. LibsignalVersion is still the libsignal release it is wire-compatible with.
func LibsignalGoVersion() string {
	return moduleVersion(libsignalGoModule)
}
