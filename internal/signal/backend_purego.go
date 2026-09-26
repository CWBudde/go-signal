//go:build purego

package signal

const libsignalGoModule = "github.com/cwbudde/libsignal-go"

// LibsignalGoVersion returns the version of libsignal-go, which the purego build of libsignalgo
// runs on. LibsignalVersion is still the libsignal release it is wire-compatible with.
func LibsignalGoVersion() string {
	return moduleVersion(libsignalGoModule)
}
