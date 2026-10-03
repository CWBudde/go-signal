//go:build cgo || libsignal_go

package signal

import (
	"context"

	mstore "github.com/cwbudde/mautrix-signal/pkg/signalmeow/store"
)

// GroupAcceptOperations exposes the isolated acceptance orchestration dependencies.
type GroupAcceptOperations = groupAcceptOperations

// AcceptGroupWithOperations runs orchestration with controlled storage and transport.
func AcceptGroupWithOperations(ctx context.Context, ops GroupAcceptOperations, self Recipient,
	ref string,
) (GroupAcceptResult, error) {
	return acceptGroupWithOperations(ctx, ops, self, ref)
}

// InstallAcceptanceGroupStore controls key resolution on a connected offline client.
func InstallAcceptanceGroupStore(client Client, groups mstore.GroupStore) func() {
	meow := client.(*meowClient) //nolint:forcetypeassert // Test-only adapter requires the real client.
	previous := meow.connDevice.GroupStore
	meow.connDevice.GroupStore = groups

	return func() { meow.connDevice.GroupStore = previous }
}

// GroupAcceptClosing reports when Close has begun, to synchronize the lifecycle test.
func GroupAcceptClosing(client Client) bool {
	meow := client.(*meowClient) //nolint:forcetypeassert // Test-only adapter requires the real client.
	meow.mu.Lock()
	defer meow.mu.Unlock()

	return meow.closing
}
