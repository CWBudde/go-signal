//go:build cgo

package signal

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cwbudde/go-signal/internal/store"
	"github.com/google/uuid"
	"go.mau.fi/mautrix-signal/pkg/libsignalgo"
	mstore "go.mau.fi/mautrix-signal/pkg/signalmeow/store"
	"google.golang.org/protobuf/encoding/protowire"
)

// fingerprintIterations and fingerprintVersion are what the Signal apps use for safety numbers
// (NumericFingerprintGenerator(5200), version 2 with ACIs as identifiers).
const (
	fingerprintIterations = libsignalgo.FingerprintVersion(5200)
	fingerprintVersion    = libsignalgo.FingerprintVersionV2
)

// identityTrust keeps the trust state of other users' identity keys in the account database
// (store.IdentityRecord) and decides which keys libsignal may send to. It is shared by the
// trustStore wrappers of a device's ACI and PNI identity stores.
//
// Policy (trust on first use): the first key seen for a user is trusted, unverified. A different
// key seen later, when decrypting a message from them (SaveIdentityKey) or when setting up a
// session to send to them (IsTrustedIdentity), replaces it as untrusted: a warning is logged and
// the change is marked for an IdentityChanged event. libsignal then refuses to encrypt for the
// new key (ErrorCodeUntrustedIdentity) until TrustIdentity; decrypting is never refused.
//
// Only the current key can be trusted. All devices of an account share its identity key, so a
// key that differs from the current one is always a change, the one trusted before the last
// change included: accepting that one would let a prekey bundle signed with an old key (a lost
// phone, a malicious server) through after the user trusted or verified the new one. So a
// change also removes the user's sessions with any other key (see removeStaleSessions), which
// libsignal would otherwise keep encrypting to; the next send fetches new prekey bundles. A
// message that still arrives on a removed session can't be decrypted (a DecryptionFailure
// event); where its content hint allows, signalmeow asks the sender to resend it (a retry
// receipt), which sets up a new session.
//
// Only ACIs are checked: PNI identity keys can't be listed or trusted, so they are left to
// signalmeow, which trusts every key. Not covered either, because signalmeow uses its store
// directly there: the PNI identity key that a sync message reports (saveSyncPNIIdentityKey), the
// PNI signature checks and provisioning. And libsignal's multi-recipient (sender key) encryption
// only looks the key up without asking whether it is trusted, so a group member who already has
// our sender key still gets group messages after an identity change.
type identityTrust struct {
	data *store.Store
	log  *slog.Logger
	now  func() time.Time
	// sessions are the device's ACI and PNI session stores, for removeStaleSessions.
	sessions []mstore.SessionStore

	// pending is set when the store may hold changes not reported as events yet.
	pending atomic.Bool
	// reportMu serialises report.
	reportMu sync.Mutex
}

// trustStore wraps one of signalmeow's identity stores with identityTrust.
type trustStore struct {
	inner libsignalgo.IdentityKeyStore
	trust *identityTrust
}

var _ libsignalgo.IdentityKeyStore = (*trustStore)(nil)

// installTrust replaces device's ACI and PNI identity stores with trustStore wrappers; libsignal
// reaches them through signalmeow's Device.IdentityStore and ACIIdentityStore. It must run
// before signalmeow.NewClient(device, …). Changes left unreported by an earlier run are reported
// with the next event.
func installTrust(device *mstore.Device, data *store.Store, log *slog.Logger, now func() time.Time) *identityTrust {
	trust := &identityTrust{
		data: data, log: log, now: now,
		sessions: []mstore.SessionStore{device.ACISessionStore, device.PNISessionStore},
	}
	trust.pending.Store(true)

	if _, done := device.ACIIdentityStore.(*trustStore); !done {
		device.ACIIdentityStore = &trustStore{inner: device.ACIIdentityStore, trust: trust}
		device.PNIIdentityStore = &trustStore{inner: device.PNIIdentityStore, trust: trust}
	}

	return trust
}

func (s *trustStore) GetIdentityKeyPair(ctx context.Context) (*libsignalgo.IdentityKeyPair, error) {
	return s.inner.GetIdentityKeyPair(ctx) //nolint:wrapcheck // a transparent wrapper
}

func (s *trustStore) GetLocalRegistrationID(ctx context.Context) (uint32, error) {
	return s.inner.GetLocalRegistrationID(ctx) //nolint:wrapcheck // a transparent wrapper
}

func (s *trustStore) GetIdentityKey(
	ctx context.Context, theirServiceID libsignalgo.ServiceID,
) (*libsignalgo.IdentityKey, error) {
	return s.inner.GetIdentityKey(ctx, theirServiceID) //nolint:wrapcheck // a transparent wrapper
}

// SaveIdentityKey stores the key in signalmeow's store as before, and records a change of an
// ACI's key. libsignal calls it after decrypting a message and after encrypting one.
func (s *trustStore) SaveIdentityKey(
	ctx context.Context, theirServiceID libsignalgo.ServiceID, identityKey *libsignalgo.IdentityKey,
) (bool, error) {
	if theirServiceID.Type != libsignalgo.ServiceIDTypeACI {
		return s.inner.SaveIdentityKey(ctx, theirServiceID, identityKey) //nolint:wrapcheck // a transparent wrapper
	}

	key, err := identityKey.Serialize()
	if err != nil {
		return false, fmt.Errorf("serialize identity key: %w", err)
	}

	known, err := s.knownKey(ctx, theirServiceID)
	if err != nil {
		return false, err
	}

	replaced, err := s.inner.SaveIdentityKey(ctx, theirServiceID, identityKey)
	if err != nil {
		return replaced, err //nolint:wrapcheck // a transparent wrapper
	}

	_, err = s.trust.observe(ctx, theirServiceID, key, known)

	return replaced, err
}

// IsTrustedIdentity trusts every key for receiving. For sending to an ACI it trusts only the
// current key, and only if the user trusted it (or it was the first one); any other key is
// recorded as a change and refused. PNIs are left to signalmeow.
func (s *trustStore) IsTrustedIdentity(
	ctx context.Context, theirServiceID libsignalgo.ServiceID, identityKey *libsignalgo.IdentityKey,
	direction libsignalgo.SignalDirection,
) (bool, error) {
	if theirServiceID.Type != libsignalgo.ServiceIDTypeACI {
		//nolint:wrapcheck // a transparent wrapper
		return s.inner.IsTrustedIdentity(ctx, theirServiceID, identityKey, direction)
	}

	if direction == libsignalgo.SignalDirectionReceiving {
		return true, nil
	}

	key, err := identityKey.Serialize()
	if err != nil {
		return false, fmt.Errorf("serialize identity key: %w", err)
	}

	known, err := s.knownKey(ctx, theirServiceID)
	if err != nil {
		return false, err
	}

	return s.trust.observe(ctx, theirServiceID, key, known)
}

// knownKey returns the key signalmeow has stored for theirServiceID, serialized; nil if none.
func (s *trustStore) knownKey(ctx context.Context, theirServiceID libsignalgo.ServiceID) ([]byte, error) {
	known, err := s.inner.GetIdentityKey(ctx, theirServiceID)
	if err != nil || known == nil {
		return nil, err //nolint:wrapcheck // a transparent wrapper
	}

	key, err := known.Serialize()
	if err != nil {
		return nil, fmt.Errorf("serialize stored identity key: %w", err)
	}

	return key, nil
}

// observe records key as the identity key of serviceID and reports whether it may be sent to.
// known is the key signalmeow had stored before, which counts as trusted on first use when
// go-signal has no record yet (accounts linked before it tracked trust). ctx may carry
// signalmeow's database transaction.
func (t *identityTrust) observe(ctx context.Context, serviceID libsignalgo.ServiceID, key, known []byte) (bool, error) {
	name := serviceID.String()

	rec, err := t.data.Identity(ctx, name)
	if err != nil {
		return false, err //nolint:wrapcheck // the store names the operation
	}

	if rec == nil {
		rec = &store.IdentityRecord{ServiceID: name, Key: known, Trust: TrustUnverified.String()}
		if known == nil {
			// First key of this user: trust on first use.
			rec.Key, rec.FirstSeen = key, t.now().UTC()

			return true, t.data.PutIdentity(ctx, *rec) //nolint:wrapcheck // the store names the operation
		}
	}

	if bytes.Equal(rec.Key, key) {
		return ParseTrustLevel(rec.Trust).Trusted(), nil
	}

	// A change, even back to the key trusted before (see identityTrust).
	if ParseTrustLevel(rec.Trust).Trusted() {
		rec.PreviousKey = rec.Key
	}

	rec.Key, rec.Trust, rec.ChangedAt, rec.PendingEvent = key, TrustUntrusted.String(), t.now().UTC(), true

	err = t.data.PutIdentity(ctx, *rec)
	if err != nil {
		return false, err //nolint:wrapcheck // the store names the operation
	}

	t.removeStaleSessions(ctx, serviceID, key)

	t.pending.Store(true)
	t.log.Warn("the safety number with a contact changed; sending to them is blocked until you trust the new key",
		"recipient", serviceID.UUID.String(), "fingerprint", fingerprint(key),
		"hint", "go-signal identities trust "+serviceID.UUID.String())

	return false, nil
}

// removeStaleSessions removes the sessions with serviceID whose identity key isn't key (see
// identityTrust). libsignal stores the session it is working on after this, so the one that
// brought key stays. A failure is only logged: sending over a remaining session is refused
// anyway, since its key isn't the current one.
func (t *identityTrust) removeStaleSessions(ctx context.Context, serviceID libsignalgo.ServiceID, key []byte) {
	removed, err := removeStaleSessions(ctx, t.sessions, serviceID, key)
	if err != nil {
		t.log.Warn("remove sessions with an old identity key", "recipient", serviceID.UUID.String(), "error", err)
	}

	if removed > 0 {
		t.log.Debug("removed sessions with an old identity key", "recipient", serviceID.UUID.String(),
			"sessions", removed)
	}
}

// removeStaleSessions removes the sessions in stores with serviceID whose remote identity key
// isn't key (serialized), and those whose key can't be read. It returns how many it removed.
func removeStaleSessions(
	ctx context.Context, stores []mstore.SessionStore, serviceID libsignalgo.ServiceID, key []byte,
) (int, error) {
	removed := 0

	for _, sessions := range stores {
		if sessions == nil {
			continue
		}

		tuples, err := sessions.AllSessionsForServiceID(ctx, serviceID)
		if err != nil {
			return removed, fmt.Errorf("list sessions: %w", err)
		}

		for _, tuple := range tuples {
			record, err := tuple.Record.Serialize()
			if err == nil && bytes.Equal(sessionRemoteIdentity(record), key) {
				continue
			}

			err = sessions.RemoveSession(ctx, tuple.Address)
			if err != nil {
				return removed, fmt.Errorf("remove session: %w", err)
			}

			removed++
		}
	}

	return removed, nil
}

// Field numbers of libsignal's session record (rust/protocol/src/proto/storage.proto).
const (
	recordCurrentSession     protowire.Number = 1 // RecordStructure.current_session
	sessionRemoteIdentityKey protowire.Number = 3 // SessionStructure.remote_identity_public
)

// sessionRemoteIdentity returns the serialized identity key of the other side of the current
// session in a serialized session record; nil if there is none or the record can't be parsed.
// libsignalgo has no accessor for it.
func sessionRemoteIdentity(record []byte) []byte {
	return protoBytesField(protoBytesField(record, recordCurrentSession), sessionRemoteIdentityKey)
}

// protoBytesField returns the first length-delimited field num of the protobuf message msg;
// nil if there is none or msg is malformed.
func protoBytesField(msg []byte, num protowire.Number) []byte {
	for len(msg) > 0 {
		field, typ, n := protowire.ConsumeTag(msg)
		if n < 0 {
			return nil
		}

		msg = msg[n:]

		if field == num && typ == protowire.BytesType {
			value, m := protowire.ConsumeBytes(msg)
			if m < 0 {
				return nil
			}

			return value
		}

		m := protowire.ConsumeFieldValue(field, typ, msg)
		if m < 0 {
			return nil
		}

		msg = msg[m:]
	}

	return nil
}

// report emits an IdentityChanged event for every change not reported yet, and marks it as
// reported once emit took it. It returns false if emit failed (the client is closing); the
// remaining changes are reported next time.
func (t *identityTrust) report(ctx context.Context, emit func(Event) bool) bool {
	if !t.pending.Swap(false) {
		return true
	}

	t.reportMu.Lock()
	defer t.reportMu.Unlock()

	records, err := t.data.PendingIdentityChanges(ctx)
	if err != nil {
		t.log.Warn("read identity changes", "error", err)
		t.pending.Store(true)

		return true
	}

	for _, rec := range records {
		evt := identityChangedEvent(rec)
		if evt != nil && !emit(evt) {
			t.pending.Store(true)

			return false
		}

		err = t.data.MarkIdentityReported(ctx, rec.ServiceID, rec.Key)
		if err != nil {
			t.log.Warn("mark identity change as reported", "error", err)
		}
	}

	return true
}

// identityChangedEvent converts a pending change; nil for service IDs that aren't ACIs.
func identityChangedEvent(rec store.IdentityRecord) *IdentityChanged {
	aci, ok := aciOf(rec.ServiceID)
	if !ok {
		return nil
	}

	return &IdentityChanged{
		Recipient:      Recipient{ACI: aci},
		OldFingerprint: fingerprint(rec.PreviousKey),
		NewFingerprint: fingerprint(rec.Key),
		Time:           rec.ChangedAt,
	}
}

// reportIdentityChanges reports pending identity changes on Events before evt, which comes from
// the envelope that may have caused them. It returns false if the client is closing.
func (c *meowClient) reportIdentityChanges(evt Event) bool {
	if _, isConn := evt.(*Connection); isConn || c.trust == nil {
		return true
	}

	return c.trust.report(c.zlog.WithContext(context.Background()), c.emit)
}

func (c *meowClient) Identities(ctx context.Context, rcpt *Recipient) ([]Identity, error) {
	if !c.begin(&c.sending) {
		return nil, ErrClosed
	}
	defer c.sending.Done()

	ctx = c.zlog.WithContext(ctx)

	device, err := c.storeDevice(ctx)
	if err != nil {
		return nil, err
	}

	filter := ""
	if rcpt != nil {
		filter, err = requireACI(*rcpt)
		if err != nil {
			return nil, err
		}
	}

	all, err := c.identities(ctx, device)
	if err != nil {
		return nil, err
	}

	out := make([]Identity, 0, len(all))

	for _, stored := range all {
		switch {
		case filter == "":
			out = append(out, stored.Identity)
		case stored.Recipient.ACI == filter:
			stored.Recipient = mergeRecipient(stored.Recipient, *rcpt)
			out = append(out, stored.Identity)
		}
	}

	return out, nil
}

func (c *meowClient) SafetyNumber(ctx context.Context, rcpt Recipient) (SafetyNumber, error) {
	if !c.begin(&c.sending) {
		return SafetyNumber{}, ErrClosed
	}
	defer c.sending.Done()

	ctx = c.zlog.WithContext(ctx)

	device, err := c.storeDevice(ctx)
	if err != nil {
		return SafetyNumber{}, err
	}

	identity, err := c.identityOf(ctx, device, rcpt)
	if err != nil {
		return SafetyNumber{}, err
	}

	number, scannable, err := safetyNumber(device, identity.Recipient.ACI, identity.key)
	if err != nil {
		return SafetyNumber{}, err
	}

	return SafetyNumber{Identity: identity.Identity, Number: number, Scannable: scannable}, nil
}

func (c *meowClient) TrustIdentity(ctx context.Context, rcpt Recipient, number string) (Identity, error) {
	if !c.begin(&c.sending) {
		return Identity{}, ErrClosed
	}
	defer c.sending.Done()

	ctx = c.zlog.WithContext(ctx)

	device, err := c.storeDevice(ctx)
	if err != nil {
		return Identity{}, err
	}

	identity, err := c.identityOf(ctx, device, rcpt)
	if err != nil {
		return Identity{}, err
	}

	level, err := trustLevelFor(device, identity, number)
	if err != nil {
		return Identity{}, err
	}

	// Sessions set up with another key (e.g. by an older go-signal, which kept them on a change)
	// must not get the trust given to this one.
	theirID, err := aciServiceID(identity.Recipient)
	if err != nil {
		return Identity{}, err
	}

	_, err = removeStaleSessions(ctx, []mstore.SessionStore{device.ACISessionStore, device.PNISessionStore},
		theirID, identity.key)
	if err != nil {
		return Identity{}, fmt.Errorf("trust identity: %w", err)
	}

	rec := identity.record
	rec.Key, rec.Trust, rec.PendingEvent = identity.key, level.String(), false

	err = c.data.PutIdentity(ctx, rec)
	if err != nil {
		return Identity{}, fmt.Errorf("trust identity: %w", err)
	}

	identity.Trust = level

	return identity.Identity, nil
}

// trustLevelFor returns the level identity gets from TrustIdentity: verified if number is its
// safety number (ErrSafetyNumberMismatch if it isn't), else at least unverified.
func trustLevelFor(device *mstore.Device, identity storedIdentity, number string) (TrustLevel, error) {
	if number == "" {
		return max(TrustUnverified, identity.Trust), nil
	}

	want, err := NormalizeSafetyNumber(number)
	if err != nil {
		return 0, err
	}

	got, _, err := safetyNumber(device, identity.Recipient.ACI, identity.key)
	if err != nil {
		return 0, err
	}

	if got != want {
		return 0, fmt.Errorf("%w with %s: check that you compare it for this account (%s)",
			ErrSafetyNumberMismatch, identity.Recipient, device.Number)
	}

	return TrustVerified, nil
}

// storeDevice returns the device of the selected account for a method that only uses the store:
// the connected one, else it is loaded (opening the database if needed), without connecting.
func (c *meowClient) storeDevice(ctx context.Context) (*mstore.Device, error) {
	if c.connDevice != nil {
		return c.connDevice, nil
	}

	return c.device(ctx)
}

// storedIdentity is an Identity with its key and record (a new one if go-signal had none).
type storedIdentity struct {
	Identity

	key    []byte
	record store.IdentityRecord
}

// identities merges go-signal's records with the keys signalmeow stored: a key without a record
// counts as trusted on first use. Only other users' ACIs are listed, ordered by ACI.
func (c *meowClient) identities(ctx context.Context, device *mstore.Device) ([]storedIdentity, error) {
	records, err := c.data.Identities(ctx)
	if err != nil {
		return nil, fmt.Errorf("identities: %w", err)
	}

	keys, err := c.data.StoredIdentityKeys(ctx, device.ACI.String())
	if err != nil {
		return nil, fmt.Errorf("identities: %w", err)
	}

	byID := make(map[string]store.IdentityRecord, len(keys))
	for serviceID, key := range keys {
		byID[serviceID] = store.IdentityRecord{ServiceID: serviceID, Key: key, Trust: TrustUnverified.String()}
	}

	for _, rec := range records {
		byID[rec.ServiceID] = rec
	}

	own := device.ACI.String()
	out := make([]storedIdentity, 0, len(byID))

	for serviceID, rec := range byID {
		aci, ok := aciOf(serviceID)
		if !ok || aci == own {
			continue
		}

		out = append(out, storedIdentity{
			Identity: Identity{
				Recipient:   Recipient{ACI: aci},
				Fingerprint: fingerprint(rec.Key),
				Trust:       ParseTrustLevel(rec.Trust),
				FirstSeen:   rec.FirstSeen,
				ChangedAt:   rec.ChangedAt,
			},
			key:    rec.Key,
			record: rec,
		})
	}

	slices.SortFunc(out, func(a, b storedIdentity) int { return strings.Compare(a.Recipient.ACI, b.Recipient.ACI) })

	return out, nil
}

// identityOf returns rcpt's current identity; ErrUnknownIdentity if there is none.
func (c *meowClient) identityOf(ctx context.Context, device *mstore.Device, rcpt Recipient) (storedIdentity, error) {
	aci, err := requireACI(rcpt)
	if err != nil {
		return storedIdentity{}, err
	}

	all, err := c.identities(ctx, device)
	if err != nil {
		return storedIdentity{}, err
	}

	i := slices.IndexFunc(all, func(id storedIdentity) bool { return id.Recipient.ACI == aci })
	if i < 0 {
		return storedIdentity{}, fmt.Errorf("%w for %s: go-signal learns it when it receives from or sends to them",
			ErrUnknownIdentity, rcpt)
	}

	id := all[i]
	id.Recipient = mergeRecipient(id.Recipient, rcpt)

	return id, nil
}

// safetyNumber computes the safety number of device's account and the user theirACI with the
// identity key theirKey (serialized), as the Signal apps do: numeric fingerprint version 2 over
// both ACIs, 5200 iterations. Both sides get the same number.
func safetyNumber(device *mstore.Device, theirACI string, theirKey []byte) (string, []byte, error) {
	their, err := uuid.Parse(theirACI)
	if err != nil {
		return "", nil, fmt.Errorf("%w: invalid ACI %q: %w", ErrUnresolvable, theirACI, err)
	}

	return computeSafetyNumber(device.ACI, device.ACIIdentityKeyPair.GetPublicKey(), their, theirKey)
}

func computeSafetyNumber(
	ownACI uuid.UUID, ownKey *libsignalgo.PublicKey, theirACI uuid.UUID, theirKey []byte,
) (string, []byte, error) {
	remote, err := libsignalgo.DeserializePublicKey(theirKey)
	if err != nil {
		return "", nil, fmt.Errorf("safety number: identity key: %w", err)
	}

	generated, err := libsignalgo.NewFingerprint(fingerprintIterations, fingerprintVersion,
		libsignalgo.NewACIServiceID(ownACI).Bytes(), ownKey,
		libsignalgo.NewACIServiceID(theirACI).Bytes(), remote)
	if err != nil {
		return "", nil, fmt.Errorf("safety number: %w", err)
	}

	number, err := generated.DisplayString()
	if err != nil {
		return "", nil, fmt.Errorf("safety number: %w", err)
	}

	scannable, err := generated.ScannableEncoding()
	if err != nil {
		return "", nil, fmt.Errorf("safety number: %w", err)
	}

	return number, scannable, nil
}

// requireACI returns rcpt's ACI, or ErrUnresolvable.
func requireACI(rcpt Recipient) (string, error) {
	id, err := aciServiceID(rcpt)
	if err != nil {
		return "", err
	}

	return id.UUID.String(), nil
}

// mergeRecipient adds what the caller knows about rcpt (number, username) to stored.
func mergeRecipient(stored, rcpt Recipient) Recipient {
	if stored.Number == "" {
		stored.Number = rcpt.Number
	}

	if stored.Username == "" {
		stored.Username = rcpt.Username
	}

	return stored
}

// aciOf returns the ACI of a service ID as signalmeow stores it; false for PNIs.
func aciOf(serviceID string) (string, bool) {
	id, err := libsignalgo.ServiceIDFromString(serviceID)
	if err != nil || id.Type != libsignalgo.ServiceIDTypeACI {
		return "", false
	}

	return id.UUID.String(), true
}

// fingerprint hex-encodes a serialized identity key; "" for none.
func fingerprint(key []byte) string {
	return hex.EncodeToString(key)
}
