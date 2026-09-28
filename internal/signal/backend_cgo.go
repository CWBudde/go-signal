//go:build cgo && !libsignal_go

package signal

// Backend names the implementation of the Signal protocol this binary was built with: libsignal
// through cgo.
const Backend = "cgo"
