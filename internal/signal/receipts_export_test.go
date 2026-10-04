//go:build cgo || libsignal_go

package signal

import (
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow"
	mstore "github.com/cwbudde/mautrix-signal/pkg/signalmeow/store"
)

// WithReceiptBackend installs the real send backend on a ConnectOffline client. The supplied
// session store stops sends before encryption or network access; restoring it precedes Close.
func WithReceiptBackend(client Client, sessions mstore.SessionStore) func() {
	meow := client.(*meowClient) //nolint:forcetypeassert // test helper for clients from Open
	device := *meow.connDevice
	device.ACISessionStore = sessions
	meow.cli = signalmeow.NewClient(&device, meow.zlog, meow.handle)

	return func() { meow.cli = nil }
}
