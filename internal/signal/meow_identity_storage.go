//go:build cgo || libsignal_go

package signal

import (
	"bytes"
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/cwbudde/go-signal/internal/store"
	"github.com/cwbudde/mautrix-signal/pkg/libsignalgo"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow/protobuf/signalpb"
	mstore "github.com/cwbudde/mautrix-signal/pkg/signalmeow/store"
	"github.com/google/uuid"
)

const storageIdentityVersion = "storage_identity_version"

type contactIdentity struct {
	aci   uuid.UUID
	key   []byte
	level TrustLevel
}

// reconcileStorageIdentities runs inside the fork's storage transaction, including background sync.
func (c *meowClient) reconcileStorageIdentities(ctx context.Context, update *signalmeow.StorageUpdate) error {
	if emptyIdentityStorageUpdate(update) {
		return nil
	}

	if len(update.MissingRecords) > 0 {
		return fmt.Errorf("incomplete storage identity snapshot: %w", ErrStorageNotStored)
	}

	version, err := c.storageIdentityVersion(ctx)
	if err != nil {
		return err
	}

	if update.Version <= version {
		return nil
	}

	identities, err := storageContactIdentities(update)
	if err != nil {
		return err
	}

	device, err := c.storeDevice(ctx)
	if err != nil {
		return err
	}

	for _, identity := range identities {
		if identity.aci == device.ACI {
			continue
		}

		err = c.reconcileContactIdentity(ctx, device, identity)
		if err != nil {
			return err
		}
	}

	// The store names its operation.
	return c.data.SetMeta(ctx, storageIdentityVersion, strconv.FormatUint(update.Version, 10)) //nolint:wrapcheck
}

// Reject ambiguous duplicates before choosing any key. Invalid identity payloads are ignored.
func storageContactIdentities(update *signalmeow.StorageUpdate) ([]contactIdentity, error) {
	seen := make(map[uuid.UUID]contactIdentity)

	var out []contactIdentity

	for _, record := range update.NewRecords {
		if record == nil {
			continue
		}

		contact := record.StorageRecord.GetContact()

		identity, ok := parseContactIdentity(contact)
		if !ok {
			continue
		}

		if previous, exists := seen[identity.aci]; exists {
			if !bytes.Equal(previous.key, identity.key) || previous.level != identity.level {
				return nil, fmt.Errorf("conflicting storage identities for %s: %w", identity.aci, ErrStorageNotStored)
			}

			continue
		}

		seen[identity.aci] = identity
		out = append(out, identity)
	}

	return out, nil
}

func parseContactIdentity(contact *signalpb.ContactRecord) (contactIdentity, bool) {
	if contact == nil {
		return contactIdentity{}, false
	}

	aci, ok := contactIdentityACI(contact)
	if !ok || !validStorageIdentityKey(contact.GetIdentityKey()) {
		return contactIdentity{}, false
	}

	var level TrustLevel

	switch contact.GetIdentityState() {
	case signalpb.ContactRecord_DEFAULT:
		level = TrustUnverified
	case signalpb.ContactRecord_VERIFIED:
		level = TrustVerified
	case signalpb.ContactRecord_UNVERIFIED:
		level = TrustUntrusted
	default:
		return contactIdentity{}, false
	}

	return contactIdentity{aci: aci, key: contact.GetIdentityKey(), level: level}, true
}

func (c *meowClient) reconcileContactIdentity(
	ctx context.Context, device *mstore.Device, remote contactIdentity,
) error {
	name := remote.aci.String()

	observation, err := c.data.StorageIdentity(ctx, name)
	if err != nil {
		return fmt.Errorf("read storage observation: %w", err)
	}

	current, err := c.identityRecordForStorage(ctx, device, remote.aci)
	if err != nil {
		return err
	}

	protocolKey, err := storageProtocolKey(ctx, device, remote.aci)
	if err != nil {
		return err
	}

	diverged := current != nil && !bytes.Equal(current.Key, protocolKey)

	apply, protected := storageIdentityDecision(current, observation, remote, diverged)
	if observation == nil {
		observation = &store.StorageIdentity{ServiceID: name}
	}

	if apply {
		current, err = c.applyStorageIdentity(ctx, device, current, remote)
		if err != nil {
			return err
		}
	}

	observation.RemoteKey, observation.RemoteTrust = remote.key, remote.level.String()

	observation.Dirty = protected
	if current != nil {
		observation.LocalKey, observation.LocalTrust = current.Key, current.Trust
	}

	return c.data.PutStorageIdentity(ctx, *observation) //nolint:wrapcheck // store names the operation
}

// Legacy protocol-only keys are known local identities, not fresh bootstrap opportunities.
func (c *meowClient) identityRecordForStorage(
	ctx context.Context, device *mstore.Device, aci uuid.UUID,
) (*store.IdentityRecord, error) {
	rec, err := c.data.Identity(ctx, aci.String())
	if err != nil || rec != nil {
		return rec, err //nolint:wrapcheck // store names the operation
	}

	key, err := device.ACIIdentityStore.GetIdentityKey(ctx, libsignalgo.NewACIServiceID(aci))
	if err != nil {
		return nil, fmt.Errorf("read protocol storage identity: %w", err)
	}

	if key == nil {
		return nil, nil //nolint:nilnil // unknown key
	}

	serialized, err := key.Serialize()
	if err != nil {
		return nil, fmt.Errorf("serialize protocol storage identity: %w", err)
	}

	return &store.IdentityRecord{ServiceID: aci.String(), Key: serialized, Trust: TrustUnverified.String()}, nil
}

func (c *meowClient) applyStorageIdentity(
	ctx context.Context, device *mstore.Device, current *store.IdentityRecord, remote contactIdentity,
) (*store.IdentityRecord, error) {
	key, err := libsignalgo.DeserializeIdentityKey(remote.key)
	if err != nil {
		return nil, fmt.Errorf("parse storage identity: %w", err)
	}

	identityStore := device.ACIIdentityStore
	if wrapped, ok := identityStore.(*trustStore); ok {
		identityStore = wrapped.inner
	}

	_, err = identityStore.SaveIdentityKey(ctx, libsignalgo.NewACIServiceID(remote.aci), key)
	if err != nil {
		return nil, fmt.Errorf("save storage protocol identity: %w", err)
	}

	now := time.Now().UTC()
	if current == nil {
		current = &store.IdentityRecord{ServiceID: remote.aci.String(), FirstSeen: now}
	} else if !bytes.Equal(current.Key, remote.key) {
		if ParseTrustLevel(current.Trust).Trusted() {
			current.PreviousKey = current.Key
		}

		current.ChangedAt = now
	}

	current.Key, current.Trust, current.PendingEvent = remote.key, remote.level.String(), false

	err = c.data.PutIdentity(ctx, *current)
	if err != nil {
		return nil, err //nolint:wrapcheck // store names the operation
	}

	sessions := []mstore.SessionStore{device.ACISessionStore, device.PNISessionStore}

	_, err = removeStaleSessions(ctx, sessions, libsignalgo.NewACIServiceID(remote.aci), remote.key)
	if err != nil {
		return nil, err
	}

	err = c.data.DeleteIdentitySync(ctx, remote.aci.String())
	if err != nil {
		return nil, err //nolint:wrapcheck // store names the operation
	}

	return current, nil
}

func (c *meowClient) storageIdentityVersion(ctx context.Context) (uint64, error) {
	raw, _, err := c.data.Meta(ctx, storageIdentityVersion)
	if err != nil {
		return 0, fmt.Errorf("read storage identity version: %w", err)
	}

	if raw == "" {
		return 0, nil
	}

	version, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse storage identity version: %w", err)
	}

	return version, nil
}

func contactIdentityACI(contact *signalpb.ContactRecord) (uuid.UUID, bool) {
	aci, err := signalmeow.ParseStringOrBinaryUUID(contact.GetAci(), contact.GetAciBinary())
	if err != nil || aci == uuid.Nil {
		return uuid.Nil, false
	}

	if len(contact.GetAciBinary()) > 0 {
		binary, parseErr := uuid.FromBytes(contact.GetAciBinary())
		if parseErr != nil || binary != aci {
			return uuid.Nil, false
		}
	}

	return aci, true
}

func validStorageIdentityKey(key []byte) bool {
	if len(key) != 33 || key[0] != 5 {
		return false
	}

	_, err := libsignalgo.DeserializeIdentityKey(key)

	return err == nil
}

func storageProtocolKey(ctx context.Context, device *mstore.Device, aci uuid.UUID) ([]byte, error) {
	key, err := device.ACIIdentityStore.GetIdentityKey(ctx, libsignalgo.NewACIServiceID(aci))
	if err != nil {
		return nil, fmt.Errorf("read storage protocol key: %w", err)
	}

	if key == nil {
		return nil, nil
	}

	serialized, err := key.Serialize()
	if err != nil {
		return nil, fmt.Errorf("serialize storage protocol key: %w", err)
	}

	return serialized, nil
}

func storageIdentityDecision(current *store.IdentityRecord, observation *store.StorageIdentity,
	remote contactIdentity, diverged bool,
) (bool, bool) {
	aligned := identityTupleMatches(current, remote.key, remote.level.String())
	if aligned && !diverged {
		return false, false
	}

	protected := current != nil
	changed := true

	if observation != nil {
		localChanged := current != nil && !identityTupleMatches(current, observation.LocalKey, observation.LocalTrust)
		protected = observation.Dirty || localChanged
		changed = !bytes.Equal(remote.key, observation.RemoteKey) || remote.level.String() != observation.RemoteTrust
	}

	protected = protected || diverged

	return !protected && changed, protected
}

func identityTupleMatches(current *store.IdentityRecord, key []byte, trust string) bool {
	return current != nil && bytes.Equal(current.Key, key) && current.Trust == trust
}

func emptyIdentityStorageUpdate(update *signalmeow.StorageUpdate) bool {
	return update == nil || update.Version == 0
}
