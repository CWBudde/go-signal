//go:build cgo

package signal

import (
	"net/http"

	"go.mau.fi/mautrix-signal/pkg/signalmeow/web"
)

// SetSignalTransport routes signalmeow's REST requests through rt until the returned function
// restores the real transport. The client is global, so tests using it must not run in
// parallel.
func SetSignalTransport(rt http.RoundTripper) func() {
	saved := web.SignalHTTPClient.Transport
	web.SignalHTTPClient.Transport = rt

	return func() { web.SignalHTTPClient.Transport = saved }
}
