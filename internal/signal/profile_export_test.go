//go:build cgo || libsignal_go

package signal

import (
	"context"
	"time"

	"github.com/cwbudde/mautrix-signal/pkg/libsignalgo"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow/protobuf/signalpb"
	mstore "github.com/cwbudde/mautrix-signal/pkg/signalmeow/store"
)

type (
	RawOwnProfile       = rawOwnProfile
	ProfileWriteRequest = profileWriteRequest
)

func EncryptProfileText(key libsignalgo.ProfileKey, value string, sizes []int) ([]byte, error) {
	return encryptProfileText(key, value, sizes)
}

func DecryptProfileText(key libsignalgo.ProfileKey, value []byte, sizes []int) (string, error) {
	return decryptProfileText(key, value, sizes)
}

func DecodeOwnProfile(raw RawOwnProfile, aci string, key libsignalgo.ProfileKey) (Profile, error) {
	return decodeOwnProfile(raw, aci, key)
}

func PrepareOwnProfileUpdate(
	raw RawOwnProfile, current Profile, key libsignalgo.ProfileKey, update ProfileUpdate,
) (ProfileWriteRequest, Profile, bool, error) {
	return prepareOwnProfileUpdate(raw, current, key, update)
}

func ProfileHTTPRequest(
	ctx context.Context, device *mstore.DeviceData, method, path string, body []byte,
) ([]byte, bool, error) {
	return profileHTTPRequest(ctx, device, method, path, body)
}

type ProfileUpdateHooks struct {
	Fetch    func(context.Context) (RawOwnProfile, Profile, error)
	CheckKey func(context.Context) error
	Write    func(context.Context, ProfileWriteRequest) (bool, error)
	Refresh  func(context.Context) error
	Persist  func(context.Context, Profile) error
	Notify   func(context.Context) error
}

func UpdateOwnProfileOnce(
	ctx context.Context, aci string, key libsignalgo.ProfileKey, update ProfileUpdate, h ProfileUpdateHooks,
) (ProfileUpdateResult, error) {
	return updateOwnProfileOnce(ctx, aci, key, update, profileUpdateHooks{
		fetch: h.Fetch, checkKey: h.CheckKey, write: h.Write,
		refresh: h.Refresh, persist: h.Persist, notify: h.Notify,
	})
}

// InitializeProfileClient gives an offline facade a real backend with initialized private caches.
func InitializeProfileClient(ctx context.Context, client Client) *signalmeow.Client {
	meow, device := meowOf(ctx, client)
	cli := signalmeow.NewClient(device, meow.zlog, meow.handle)
	meow.cliMu.Lock()
	meow.cli = cli
	meow.cliMu.Unlock()

	return cli
}

func PersistOwnProfile(ctx context.Context, recipients mstore.RecipientStore, profile Profile) error {
	return persistOwnProfile(ctx, recipients, profile)
}

func RefreshProfileWithCancel(ctx context.Context, interrupt func(), refresh func() error) error {
	return profileWithCancel(ctx, interrupt, refresh)
}

func ProfileSync(ctx context.Context, cli *signalmeow.Client) error {
	message := &signalpb.SyncMessage{Content: &signalpb.SyncMessage_FetchLatest_{
		FetchLatest: &signalpb.SyncMessage_FetchLatest{Type: signalpb.SyncMessage_FetchLatest_LOCAL_PROFILE.Enum()},
	}}

	return sendSyncMessage(ctx, cli, message)
}

func ProfileDrainTimeout(client Client) {
	meow, ok := client.(*meowClient)
	if !ok {
		panic("not a meow client")
	}

	meow.drainTimeout = time.Millisecond
}

// ProfileFollowUpWithCancel exposes the shared cache/sync cancellation lifetime.
func ProfileFollowUpWithCancel(ctx context.Context, interrupt func(), operation func() error) error {
	return profileWithCancel(ctx, interrupt, operation)
}
