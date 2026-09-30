//go:build cgo || libsignal_go

package signal

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"sync"
	"time"

	"github.com/cwbudde/mautrix-signal/pkg/libsignalgo"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow/protobuf/signalpb"
	mstore "github.com/cwbudde/mautrix-signal/pkg/signalmeow/store"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow/types"
	"github.com/google/uuid"
)

type profileUpdateHooks struct {
	fetch    func(context.Context) (rawOwnProfile, Profile, error)
	checkKey func(context.Context) error
	write    func(context.Context, profileWriteRequest) (bool, error)
	refresh  func(context.Context) error
	persist  func(context.Context, Profile) error
	notify   func(context.Context) error
}

func updateOwnProfileOnce(
	ctx context.Context, ownACI string, key libsignalgo.ProfileKey, update ProfileUpdate, hooks profileUpdateHooks,
) (ProfileUpdateResult, error) {
	err := checkProfileOperation(ctx, update)
	if err != nil {
		return ProfileUpdateResult{}, err
	}

	raw, current, err := hooks.fetch(ctx)
	if err != nil {
		return ProfileUpdateResult{}, err
	}

	err = hooks.checkKey(ctx)
	if err != nil {
		return ProfileUpdateResult{}, err
	}

	request, proposed, changed, err := prepareOwnProfileUpdate(raw, current, key, update)
	if err != nil {
		return ProfileUpdateResult{}, err
	}

	if !changed {
		return ProfileUpdateResult{Profile: current, Verified: true}, nil
	}

	err = hooks.checkKey(ctx)
	if err != nil {
		return ProfileUpdateResult{}, err
	}

	err = ctx.Err()
	if err != nil {
		return ProfileUpdateResult{}, fmt.Errorf("profile update: %w", err)
	}

	accepted, writeErr := hooks.write(ctx, request)
	if !accepted {
		return ProfileUpdateResult{}, writeErr
	}

	result := ProfileUpdateResult{Profile: Profile{ACI: ownACI}, Changed: true, Accepted: true}
	followErr := verifyOwnProfile(ctx, raw, request, proposed, hooks, &result)
	notifyErr := hooks.notify(ctx)

	err = errors.Join(writeErr, followErr, notifyErr)
	if err != nil {
		return result, fmt.Errorf("profile write accepted; follow-up failed: %w", err)
	}

	return result, nil
}

func checkProfileOperation(ctx context.Context, update ProfileUpdate) error {
	err := update.Check()
	if err != nil {
		return err
	}

	err = ctx.Err()
	if err != nil {
		return fmt.Errorf("profile update: %w", err)
	}

	return nil
}

func verifyOwnProfile(
	ctx context.Context,
	before rawOwnProfile,
	request profileWriteRequest,
	proposed Profile,
	hooks profileUpdateHooks,
	result *ProfileUpdateResult,
) error {
	contextErr := ctx.Err()
	if contextErr != nil {
		return fmt.Errorf("%w: %w", ErrProfileVerification, contextErr)
	}

	after, actual, err := hooks.fetch(ctx)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrProfileVerification, err)
	}

	err = hooks.checkKey(ctx)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrProfileVerification, err)
	}

	if actual != proposed || !matchesProfileSnapshot(before, after, request) {
		return ErrProfileVerification
	}

	result.Profile = actual
	result.Verified = true
	refreshErr := hooks.refresh(ctx)
	persistErr := hooks.persist(ctx, actual)

	return errors.Join(refreshErr, persistErr)
}

func matchesProfileSnapshot(before, after rawOwnProfile, request profileWriteRequest) bool {
	if !preservedProfileMetadata(before, after) {
		return false
	}

	for _, field := range []struct{ before, after, submitted []byte }{
		{before.Name, after.Name, request.Name},
		{before.About, after.About, request.About},
		{before.AboutEmoji, after.AboutEmoji, request.AboutEmoji},
	} {
		if sameProfileBytes(field.submitted, field.before) && !sameProfileBytes(field.after, field.before) {
			return false
		}
	}

	return true
}

func sameProfileBytes(left, right []byte) bool {
	return (left == nil) == (right == nil) && bytes.Equal(left, right)
}

func preservedProfileMetadata(before, after rawOwnProfile) bool {
	if before.Avatar != after.Avatar ||
		!sameProfileBytes(before.PaymentAddress, after.PaymentAddress) ||
		!sameProfileBytes(before.PhoneNumberSharing, after.PhoneNumberSharing) {
		return false
	}

	var left, right any
	if len(before.Badges) > 0 {
		if json.Unmarshal(before.Badges, &left) != nil {
			return false
		}
	}

	if len(after.Badges) > 0 {
		if json.Unmarshal(after.Badges, &right) != nil {
			return false
		}
	}

	return reflect.DeepEqual(left, right)
}

// profileMutex serializes operations without making canceled callers wait for another request.
type profileMutex struct {
	once  sync.Once
	token chan struct{}
}

func (mutex *profileMutex) lock(ctx context.Context) error {
	mutex.once.Do(func() { mutex.token = make(chan struct{}, 1) })

	select {
	case mutex.token <- struct{}{}:
		err := ctx.Err()
		if err != nil {
			<-mutex.token
			return fmt.Errorf("profile wait: %w", err)
		}

		return nil
	case <-ctx.Done():
		return fmt.Errorf("profile wait: %w", ctx.Err())
	}
}
func (mutex *profileMutex) unlock() { <-mutex.token }

func (c *meowClient) profileOperation(ctx context.Context) (context.Context, *signalmeow.Client, func(), error) {
	cli, done, err := c.groupClient("own profile")
	if err != nil {
		return nil, nil, nil, err
	}

	if cli == nil {
		done()
		return nil, nil, nil, ErrNotConnected
	}
	// Close waits for handlers before stopping the backend websockets. Register here too
	// so its pointer resets cannot race the cache refresh or notification stages.
	if !c.begin(&c.handling) {
		done()
		return nil, nil, nil, ErrClosed
	}

	operation, cancel := context.WithCancel(ctx)

	stopped := make(chan struct{})
	go func() {
		defer close(stopped)

		select {
		case <-c.done:
			cancel()
		case <-operation.Done():
		}
	}()

	finish := func() { cancel(); <-stopped; c.handling.Done(); done() }

	err = c.profileMu.lock(operation)
	if err != nil {
		finish()
		return nil, nil, nil, err
	}

	return operation, cli, func() { c.profileMu.unlock(); finish() }, nil
}

func (c *meowClient) profileKey(ctx context.Context) (libsignalgo.ProfileKey, error) {
	key, err := c.connDevice.RecipientStore.MyProfileKey(ctx)
	if err != nil {
		return libsignalgo.ProfileKey{}, fmt.Errorf("load own profile key: %w", err)
	}

	if key == nil {
		return libsignalgo.ProfileKey{}, ErrProfileKeyUnavailable
	}

	return *key, nil
}

func (c *meowClient) checkProfileKey(ctx context.Context, key libsignalgo.ProfileKey) error {
	contextErr := ctx.Err()
	if contextErr != nil {
		return fmt.Errorf("check profile key: %w", contextErr)
	}

	current, err := c.profileKey(ctx)
	if err != nil {
		return err
	}

	if current != key {
		return ErrProfileKeyChanged
	}

	return nil
}

func (c *meowClient) fetchOwnProfile(
	ctx context.Context, cli *signalmeow.Client, key libsignalgo.ProfileKey,
) (rawOwnProfile, Profile, error) {
	version, err := key.GetProfileKeyVersion(c.connDevice.ACI)
	if err != nil {
		return rawOwnProfile{}, Profile{}, fmt.Errorf("profile version: %w", err)
	}

	credential, err := cli.ProfileKeyCredentialRequest(ctx, c.connDevice.ACI)
	if err != nil {
		return rawOwnProfile{}, Profile{}, fmt.Errorf("profile credential request: %w", err)
	}

	err = c.checkProfileKey(ctx, key)
	if err != nil {
		return rawOwnProfile{}, Profile{}, err
	}

	path := "/v1/profile/" + c.ownACI + "/" + version.String() + "/" + string(credential) +
		"?credentialType=expiringProfileKey"
	body, _, err := profileHTTPRequest(ctx, &c.connDevice.DeviceData, http.MethodGet, path, nil)

	keyErr := c.checkProfileKey(ctx, key)
	if errors.Is(err, ErrDeviceUnlinked) {
		err = c.markUnlinked(c.account, err)
	}

	err = errors.Join(err, keyErr)
	if err != nil {
		return rawOwnProfile{}, Profile{}, err
	}

	var raw rawOwnProfile

	err = json.Unmarshal(body, &raw)
	if err != nil {
		return rawOwnProfile{}, Profile{}, fmt.Errorf("%w: malformed profile response", ErrInvalidProfile)
	}

	profile, err := decodeOwnProfile(raw, c.ownACI, key)

	return raw, profile, err
}

func (c *meowClient) OwnProfile(ctx context.Context) (Profile, error) {
	ctx, cli, done, err := c.profileOperation(ctx)
	if err != nil {
		return Profile{}, err
	}
	defer done()

	key, err := c.profileKey(ctx)
	if err != nil {
		return Profile{}, err
	}

	_, profile, err := c.fetchOwnProfile(ctx, cli, key)

	return profile, err
}

func (c *meowClient) UpdateOwnProfile(ctx context.Context, update ProfileUpdate) (ProfileUpdateResult, error) {
	validationErr := update.Check()
	if validationErr != nil {
		return ProfileUpdateResult{}, validationErr
	}

	ctx, cli, done, err := c.profileOperation(ctx)
	if err != nil {
		return ProfileUpdateResult{}, err
	}
	defer done()

	key, err := c.profileKey(ctx)
	if err != nil {
		return ProfileUpdateResult{}, err
	}

	return updateOwnProfileOnce(ctx, c.ownACI, key, update, c.ownProfileHooks(cli, key))
}

func (c *meowClient) ownProfileHooks(cli *signalmeow.Client, key libsignalgo.ProfileKey) profileUpdateHooks {
	return profileUpdateHooks{
		fetch:    func(ctx context.Context) (rawOwnProfile, Profile, error) { return c.fetchOwnProfile(ctx, cli, key) },
		checkKey: func(ctx context.Context) error { return c.checkProfileKey(ctx, key) },
		write: func(ctx context.Context, request profileWriteRequest) (bool, error) {
			body, marshalErr := json.Marshal(request)
			if marshalErr != nil {
				return false, fmt.Errorf("encode profile: %w", marshalErr)
			}

			keyErr := c.checkProfileKey(ctx, key)
			if keyErr != nil {
				return false, keyErr
			}

			_, accepted, writeErr := profileHTTPRequest(ctx, &c.connDevice.DeviceData, http.MethodPut, "/v1/profile", body)
			if errors.Is(writeErr, ErrDeviceUnlinked) {
				writeErr = c.markUnlinked(c.account, writeErr)
			}

			return accepted, writeErr
		},
		refresh: func(ctx context.Context) error {
			return profileWithCancel(ctx, cli.UnauthedWS.ForceReconnect, func() error {
				_, refreshErr := cli.RetrieveProfileByID(ctx, c.connDevice.ACI, 0)
				if refreshErr != nil {
					return fmt.Errorf("refresh profile cache: %w", refreshErr)
				}

				return nil
			})
		},
		persist: func(ctx context.Context, profile Profile) error {
			return persistOwnProfile(ctx, c.connDevice.RecipientStore, profile)
		},
		notify: func(ctx context.Context) error {
			contextErr := ctx.Err()
			if contextErr != nil {
				return fmt.Errorf("notify profile update: %w", contextErr)
			}

			message := &signalpb.SyncMessage{Content: &signalpb.SyncMessage_FetchLatest_{
				FetchLatest: &signalpb.SyncMessage_FetchLatest{Type: signalpb.SyncMessage_FetchLatest_LOCAL_PROFILE.Enum()},
			}}

			return profileWithCancel(ctx, cli.AuthedWS.ForceReconnect, func() error {
				return sendSyncMessage(ctx, cli, message)
			})
		},
	}
}

// profileWithCancel interrupts a dependency websocket operation that ignores caller cancellation.
// Cancellation can race a late enqueue or a retry onto a reconnected websocket, so
// interruption continues until the synchronous operation returns. The callback is
// then joined before the operation's lifecycle registration can be released.
func profileWithCancel(ctx context.Context, interrupt func(), operation func() error) error {
	contextErr := ctx.Err()
	if contextErr != nil {
		return fmt.Errorf("profile follow-up: %w", contextErr)
	}

	operationDone := make(chan struct{})
	callbackDone := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		defer close(callbackDone)

		interruptProfileUntilDone(operationDone, interrupt)
	})

	defer func() {
		close(operationDone)

		if !stop() {
			<-callbackDone
		}
	}()

	err := operation()

	return errors.Join(err, ctx.Err())
}

const profileInterruptInterval = 20 * time.Millisecond

func interruptProfileUntilDone(done <-chan struct{}, interrupt func()) {
	ticker := time.NewTicker(profileInterruptInterval)
	defer ticker.Stop()

	for {
		select {
		case <-done:
			return
		default:
		}

		interrupt()

		select {
		case <-done:
			return
		case <-ticker.C:
		}
	}
}

func persistOwnProfile(ctx context.Context, recipients mstore.RecipientStore, profile Profile) error {
	aci, err := uuid.Parse(profile.ACI)
	if err != nil {
		return fmt.Errorf("persist profile identifier: %w", err)
	}

	_, err = recipients.LoadAndUpdateRecipient(ctx, aci, uuid.Nil, func(recipient *types.Recipient) (bool, error) {
		recipient.Profile.Name = profile.GivenName
		if profile.FamilyName != "" {
			recipient.Profile.Name += " " + profile.FamilyName
		}

		recipient.Profile.About = profile.About
		recipient.Profile.AboutEmoji = profile.AboutEmoji
		recipient.Profile.AvatarPath = profile.AvatarPath
		recipient.Profile.FetchedAt = time.Now()

		return true, nil
	})
	if err != nil {
		return fmt.Errorf("persist own profile: %w", err)
	}

	return nil
}
