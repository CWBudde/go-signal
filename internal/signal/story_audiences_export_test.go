//go:build cgo || libsignal_go

package signal

import (
	"context"

	"github.com/cwbudde/mautrix-signal/pkg/signalmeow"
)

// ProjectStoryAudiences exposes the complete storage projection for offline tests.
func ProjectStoryAudiences(update *signalmeow.StorageUpdate, self string) (StoryAudiences, error) {
	return projectStoryAudiences(update, self)
}

// FetchStoryAudiences substitutes only the storage HTTP fetch, using production persistence.
func FetchStoryAudiences(
	ctx context.Context, client Client, fetch func(context.Context) (*signalmeow.StorageUpdate, error),
) (StoryAudiences, error) {
	meow, device := meowOf(ctx, client)
	return meow.freshStoryAudiences(ctx, device.ACI.String(), fetch)
}
