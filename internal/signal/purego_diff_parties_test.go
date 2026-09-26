//go:build cgo && !purego

package signal_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"testing"
	"time"

	"github.com/cwbudde/libsignal-go/address"
	"github.com/cwbudde/libsignal-go/curve"
	"github.com/cwbudde/libsignal-go/groups"
	"github.com/cwbudde/libsignal-go/kem"
	"github.com/cwbudde/libsignal-go/protocol"
	"github.com/cwbudde/libsignal-go/sealedsender"
	"github.com/cwbudde/libsignal-go/session"
	"github.com/cwbudde/libsignal-go/stores/inmem"
	"github.com/google/uuid"
	"go.mau.fi/mautrix-signal/pkg/libsignalgo"
)

// The differential tests run the same protocol flows with one party on the cgo libsignalgo and
// the other on libsignal-go, which is what the purego build of libsignalgo calls. diffParty hides
// the backend so that every flow runs in both directions.

const (
	diffDeviceID       = 1
	diffPreKeyID       = 31
	diffSignedPreKeyID = 32
	diffKyberPreKeyID  = 33
	diffCertKeyID      = 7
)

// diffBundle is a pre-key bundle in serialized form, so that either backend can build its own.
type diffBundle struct {
	registrationID           uint32
	preKey, signedPreKey     []byte
	signedPreKeySignature    []byte
	kyberPreKey              []byte
	kyberPreKeySignature     []byte
	identityKey              []byte
	preKeyID, signedPreKeyID uint32
	kyberPreKeyID            uint32
}

// diffTrust is a trust root and the server certificate it signed, shared by both parties.
type diffTrust struct {
	root       []byte // serialized public key
	serverCert []byte
	serverKey  []byte // serialized private key of the server certificate
}

// diffUnsealed is what a recipient learns from a sealed-sender message.
type diffUnsealed struct {
	messageType uint8
	contents    []byte
	senderUUID  string
	certValid   bool
}

type diffParty interface { //nolint:interfacebloat // one method per protocol step
	backend() string
	aci() uuid.UUID
	registrationID() uint32
	identityKey(t *testing.T) []byte

	bundle(t *testing.T) diffBundle
	processBundle(t *testing.T, peer diffParty, b diffBundle)
	// encrypt returns the ciphertext and whether it is a pre-key message.
	encrypt(t *testing.T, peer diffParty, plaintext []byte) ([]byte, bool)
	decrypt(t *testing.T, peer diffParty, ciphertext []byte, preKey bool) []byte
	session(t *testing.T, peer diffParty) []byte

	createSKDM(t *testing.T, distributionID uuid.UUID) []byte
	processSKDM(t *testing.T, sender diffParty, skdm []byte)
	groupEncrypt(t *testing.T, distributionID uuid.UUID, plaintext []byte) []byte
	groupDecrypt(t *testing.T, sender diffParty, ciphertext []byte) []byte
	senderKey(t *testing.T, sender diffParty, distributionID uuid.UUID) []byte

	// sealedEncrypt encrypts plaintext for peer and seals it: v1, or v2 in its sent form.
	sealedEncrypt(t *testing.T, peer diffParty, trust diffTrust, plaintext []byte, sealV2 bool) []byte
	unseal(t *testing.T, trust diffTrust, sealed []byte) diffUnsealed
}

func newDiffTrust(t *testing.T) diffTrust {
	t.Helper()

	root := must(curve.GenerateKeyPair(rand.Reader))(t)
	server := must(curve.GenerateKeyPair(rand.Reader))(t)
	cert := must(sealedsender.NewServerCertificate(diffCertKeyID, server.PublicKey, root.PrivateKey, rand.Reader))(t)

	return diffTrust{
		root:       root.PublicKey.Serialize(),
		serverCert: cert.Serialized(),
		serverKey:  server.PrivateKey.Serialize(),
	}
}

// must takes a (value, error) result and returns a function that fails the test on the error:
// must(f())(t).
func must[V any](value V, err error) func(t *testing.T) V {
	return func(t *testing.T) V {
		t.Helper()

		if err != nil {
			t.Fatal(err)
		}

		return value
	}
}

// wrapped wraps the error of a libsignalgo call made by cgoStore.
func wrapped[V any](value V, err error) (V, error) {
	if err != nil {
		return value, fmt.Errorf("libsignalgo: %w", err)
	}

	return value, nil
}

func check(t *testing.T, err error) {
	t.Helper()

	if err != nil {
		t.Fatal(err)
	}
}

// ---- cgo party: libsignalgo over libsignal_ffi.a ----

type cgoParty struct {
	*cgoStore

	id uuid.UUID
}

func newCGOParty(t *testing.T, regID uint32) *cgoParty {
	t.Helper()

	return &cgoParty{
		id: uuid.New(),
		cgoStore: &cgoStore{
			regID:      regID,
			identity:   must(libsignalgo.GenerateIdentityKeyPair())(t),
			sessions:   map[string][]byte{},
			identities: map[string][]byte{},
			preKeys:    map[uint32][]byte{},
			signed:     map[uint32][]byte{},
			kyber:      map[uint32][]byte{},
			senderKeys: map[string][]byte{},
		},
	}
}

func (*cgoParty) backend() string          { return "cgo" }
func (p *cgoParty) aci() uuid.UUID         { return p.id }
func (p *cgoParty) registrationID() uint32 { return p.regID }

func (p *cgoParty) identityKey(t *testing.T) []byte {
	t.Helper()

	return must(p.identity.GetPublicKey().Serialize())(t)
}

func cgoAddress(t *testing.T, party diffParty) *libsignalgo.Address {
	t.Helper()

	return must(libsignalgo.NewACIServiceID(party.aci()).Address(diffDeviceID))(t)
}

func (p *cgoParty) bundle(t *testing.T) diffBundle {
	t.Helper()

	identityPrivate := p.identity.GetPrivateKey()

	preKey := must(libsignalgo.GeneratePrivateKey())(t)
	preKeyRecord := must(libsignalgo.NewPreKeyRecordFromPrivateKey(diffPreKeyID, preKey))(t)
	p.preKeys[diffPreKeyID] = must(preKeyRecord.Serialize())(t)

	signed := must(libsignalgo.GeneratePrivateKey())(t)
	signedPublic := must(must(signed.GetPublicKey())(t).Serialize())(t)
	signedSignature := must(identityPrivate.Sign(signedPublic))(t)
	signedRecord := must(libsignalgo.NewSignedPreKeyRecordFromPrivateKey(
		diffSignedPreKeyID, time.Now(), signed, signedSignature))(t)
	p.signed[diffSignedPreKeyID] = must(signedRecord.Serialize())(t)

	kyber := must(libsignalgo.KyberKeyPairGenerate())(t)
	kyberPublic := must(must(kyber.GetPublicKey())(t).Serialize())(t)
	kyberSignature := must(identityPrivate.Sign(kyberPublic))(t)
	kyberRecord := must(libsignalgo.NewKyberPreKeyRecord(diffKyberPreKeyID, time.Now(), kyber, kyberSignature))(t)
	p.kyber[diffKyberPreKeyID] = must(kyberRecord.Serialize())(t)

	return diffBundle{
		registrationID:        p.regID,
		preKeyID:              diffPreKeyID,
		preKey:                must(must(preKey.GetPublicKey())(t).Serialize())(t),
		signedPreKeyID:        diffSignedPreKeyID,
		signedPreKey:          signedPublic,
		signedPreKeySignature: signedSignature,
		kyberPreKeyID:         diffKyberPreKeyID,
		kyberPreKey:           kyberPublic,
		kyberPreKeySignature:  kyberSignature,
		identityKey:           p.identityKey(t),
	}
}

func (p *cgoParty) processBundle(t *testing.T, peer diffParty, b diffBundle) {
	t.Helper()

	bundle := must(libsignalgo.NewPreKeyBundle(
		b.registrationID, diffDeviceID,
		b.preKeyID, must(libsignalgo.DeserializePublicKey(b.preKey))(t),
		b.signedPreKeyID, must(libsignalgo.DeserializePublicKey(b.signedPreKey))(t), b.signedPreKeySignature,
		b.kyberPreKeyID, must(libsignalgo.DeserializeKyberPublicKey(b.kyberPreKey))(t), b.kyberPreKeySignature,
		must(libsignalgo.DeserializeIdentityKey(b.identityKey))(t),
	))(t)
	check(t, libsignalgo.ProcessPreKeyBundle(t.Context(), bundle, cgoAddress(t, peer), cgoAddress(t, p), p, p))
}

func (p *cgoParty) encryptMessage(t *testing.T, peer diffParty, plaintext []byte) *libsignalgo.CiphertextMessage {
	t.Helper()

	return must(libsignalgo.Encrypt(t.Context(), plaintext, cgoAddress(t, peer), cgoAddress(t, p), p, p))(t)
}

func (p *cgoParty) encrypt(t *testing.T, peer diffParty, plaintext []byte) ([]byte, bool) {
	t.Helper()

	message := p.encryptMessage(t, peer, plaintext)

	return must(message.Serialize())(t),
		must(message.MessageType())(t) == libsignalgo.CiphertextMessageTypePreKey
}

func (p *cgoParty) decrypt(t *testing.T, peer diffParty, ciphertext []byte, preKey bool) []byte {
	t.Helper()

	from, local := cgoAddress(t, peer), cgoAddress(t, p)
	if preKey {
		message := must(libsignalgo.DeserializePreKeyMessage(ciphertext))(t)

		return must(libsignalgo.DecryptPreKey(t.Context(), message, from, local, p, p, p, p, p))(t)
	}

	message := must(libsignalgo.DeserializeMessage(ciphertext))(t)

	return must(libsignalgo.Decrypt(t.Context(), message, from, local, p, p))(t)
}

func (p *cgoParty) session(t *testing.T, peer diffParty) []byte {
	t.Helper()

	return p.sessions[must(addressKey(cgoAddress(t, peer)))(t)]
}

func (p *cgoParty) createSKDM(t *testing.T, distributionID uuid.UUID) []byte {
	t.Helper()

	skdm := must(libsignalgo.NewSenderKeyDistributionMessage(t.Context(), cgoAddress(t, p), distributionID, p))(t)

	return must(skdm.Serialize())(t)
}

func (p *cgoParty) processSKDM(t *testing.T, sender diffParty, skdm []byte) {
	t.Helper()

	message := must(libsignalgo.DeserializeSenderKeyDistributionMessage(skdm))(t)
	check(t, libsignalgo.ProcessSenderKeyDistributionMessage(t.Context(), message, cgoAddress(t, sender), p))
}

func (p *cgoParty) groupEncrypt(t *testing.T, distributionID uuid.UUID, plaintext []byte) []byte {
	t.Helper()

	message := must(libsignalgo.GroupEncrypt(t.Context(), plaintext, cgoAddress(t, p), distributionID, p))(t)

	return must(message.Serialize())(t)
}

func (p *cgoParty) groupDecrypt(t *testing.T, sender diffParty, ciphertext []byte) []byte {
	t.Helper()

	return must(libsignalgo.GroupDecrypt(t.Context(), ciphertext, cgoAddress(t, sender), p))(t)
}

func (p *cgoParty) senderKey(t *testing.T, sender diffParty, distributionID uuid.UUID) []byte {
	t.Helper()

	return p.senderKeys[must(addressKey(cgoAddress(t, sender)))(t)+"/"+distributionID.String()]
}

func (p *cgoParty) senderCertificate(t *testing.T, trust diffTrust) *libsignalgo.SenderCertificate {
	t.Helper()

	serverCert := must(libsignalgo.DeserializeServerCertificate(trust.serverCert))(t)
	serverKey := must(libsignalgo.DeserializePrivateKey(trust.serverKey))(t)
	sender := libsignalgo.NewSealedSenderAddress("+491701234567", p.id, diffDeviceID)

	return must(libsignalgo.NewSenderCertificate(
		sender, p.identity.GetPublicKey(), time.Now().Add(time.Hour), serverCert, serverKey))(t)
}

func (p *cgoParty) sealedEncrypt(t *testing.T, peer diffParty, trust diffTrust, plaintext []byte, sealV2 bool) []byte {
	t.Helper()

	usmc := must(libsignalgo.NewUnidentifiedSenderMessageContent(
		p.encryptMessage(t, peer, plaintext), p.senderCertificate(t, trust),
		libsignalgo.UnidentifiedSenderMessageContentHintResendable, nil))(t)

	addr := cgoAddress(t, peer)
	if !sealV2 {
		return must(libsignalgo.SealedSenderEncrypt(t.Context(), usmc, addr, p))(t)
	}

	recipient := libsignalgo.SessionAddressTuple{
		ServiceID: libsignalgo.NewACIServiceID(peer.aci()),
		DeviceID:  diffDeviceID,
		Address:   addr,
		Record:    must(p.LoadSession(t.Context(), addr))(t),
	}

	return must(libsignalgo.SealedSenderMultiRecipientEncrypt(
		t.Context(), usmc, []libsignalgo.SessionAddressTuple{recipient}, p))(t)
}

func (p *cgoParty) unseal(t *testing.T, trust diffTrust, sealed []byte) diffUnsealed {
	t.Helper()

	usmc := must(libsignalgo.SealedSenderDecryptToUSMC(t.Context(), sealed, p))(t)
	cert := must(usmc.GetSenderCertificate())(t)
	root := must(libsignalgo.DeserializePublicKey(trust.root))(t)

	return diffUnsealed{
		messageType: uint8(must(usmc.GetMessageType())(t)),
		contents:    must(usmc.GetContents())(t),
		senderUUID:  must(cert.GetSenderUUID())(t).String(),
		certValid:   must(cert.Validate([]*libsignalgo.PublicKey{root}, time.Now()))(t),
	}
}

// cgoStore implements libsignalgo's store interfaces for a cgoParty, keeping every record
// serialized.
type cgoStore struct {
	regID    uint32
	identity *libsignalgo.IdentityKeyPair

	sessions   map[string][]byte
	identities map[string][]byte
	preKeys    map[uint32][]byte
	signed     map[uint32][]byte
	kyber      map[uint32][]byte
	senderKeys map[string][]byte
}

func (s *cgoStore) GetIdentityKeyPair(context.Context) (*libsignalgo.IdentityKeyPair, error) {
	return s.identity, nil
}

func (s *cgoStore) GetLocalRegistrationID(context.Context) (uint32, error) { return s.regID, nil }

func (s *cgoStore) SaveIdentityKey(
	_ context.Context, theirServiceID libsignalgo.ServiceID, identityKey *libsignalgo.IdentityKey,
) (bool, error) {
	serialized, err := identityKey.Serialize()
	if err != nil {
		return false, fmt.Errorf("serialize identity key: %w", err)
	}

	old, known := s.identities[theirServiceID.String()]
	s.identities[theirServiceID.String()] = serialized

	return known && !bytes.Equal(old, serialized), nil
}

func (s *cgoStore) GetIdentityKey(
	_ context.Context, theirServiceID libsignalgo.ServiceID,
) (*libsignalgo.IdentityKey, error) {
	serialized, ok := s.identities[theirServiceID.String()]
	if !ok {
		return nil, nil //nolint:nilnil // libsignalgo's contract for an unknown identity
	}

	return wrapped(libsignalgo.DeserializeIdentityKey(serialized))
}

func (*cgoStore) IsTrustedIdentity(
	context.Context, libsignalgo.ServiceID, *libsignalgo.IdentityKey, libsignalgo.SignalDirection,
) (bool, error) {
	return true, nil
}

func (s *cgoStore) LoadSession(_ context.Context, addr *libsignalgo.Address) (*libsignalgo.SessionRecord, error) {
	key, err := addressKey(addr)
	if err != nil {
		return nil, err
	}

	serialized, ok := s.sessions[key]
	if !ok {
		return nil, nil //nolint:nilnil // libsignalgo's contract for a missing session
	}

	return wrapped(libsignalgo.DeserializeSessionRecord(serialized))
}

func (s *cgoStore) StoreSession(_ context.Context, addr *libsignalgo.Address, record *libsignalgo.SessionRecord) error {
	key, err := addressKey(addr)
	if err != nil {
		return err
	}

	s.sessions[key], err = wrapped(record.Serialize())

	return err
}

func (s *cgoStore) LoadPreKey(_ context.Context, id uint32) (*libsignalgo.PreKeyRecord, error) {
	return wrapped(libsignalgo.DeserializePreKeyRecord(s.preKeys[id]))
}

func (s *cgoStore) StorePreKey(_ context.Context, id uint32, record *libsignalgo.PreKeyRecord) error {
	var err error

	s.preKeys[id], err = wrapped(record.Serialize())

	return err
}

func (s *cgoStore) RemovePreKey(_ context.Context, id uint32) error {
	delete(s.preKeys, id)

	return nil
}

func (s *cgoStore) LoadSignedPreKey(_ context.Context, id uint32) (*libsignalgo.SignedPreKeyRecord, error) {
	return wrapped(libsignalgo.DeserializeSignedPreKeyRecord(s.signed[id]))
}

func (s *cgoStore) StoreSignedPreKey(_ context.Context, id uint32, record *libsignalgo.SignedPreKeyRecord) error {
	var err error

	s.signed[id], err = wrapped(record.Serialize())

	return err
}

func (s *cgoStore) LoadKyberPreKey(_ context.Context, id uint32) (*libsignalgo.KyberPreKeyRecord, error) {
	return wrapped(libsignalgo.DeserializeKyberPreKeyRecord(s.kyber[id]))
}

func (s *cgoStore) StoreKyberPreKey(_ context.Context, id uint32, record *libsignalgo.KyberPreKeyRecord) error {
	var err error

	s.kyber[id], err = wrapped(record.Serialize())

	return err
}

func (*cgoStore) MarkKyberPreKeyUsed(context.Context, uint32) error { return nil }

func (s *cgoStore) LoadSenderKey(
	_ context.Context, sender *libsignalgo.Address, distributionID uuid.UUID,
) (*libsignalgo.SenderKeyRecord, error) {
	key, err := addressKey(sender)
	if err != nil {
		return nil, err
	}

	serialized, ok := s.senderKeys[key+"/"+distributionID.String()]
	if !ok {
		return nil, nil //nolint:nilnil // libsignalgo's contract for a missing sender key
	}

	return wrapped(libsignalgo.DeserializeSenderKeyRecord(serialized))
}

func (s *cgoStore) StoreSenderKey(
	_ context.Context, sender *libsignalgo.Address, distributionID uuid.UUID, record *libsignalgo.SenderKeyRecord,
) error {
	key, err := addressKey(sender)
	if err != nil {
		return err
	}

	s.senderKeys[key+"/"+distributionID.String()], err = wrapped(record.Serialize())

	return err
}

func addressKey(addr *libsignalgo.Address) (string, error) {
	name, err := addr.Name()
	if err != nil {
		return "", fmt.Errorf("address name: %w", err)
	}

	device, err := addr.DeviceID()
	if err != nil {
		return "", fmt.Errorf("address device: %w", err)
	}

	return fmt.Sprintf("%s.%d", name, device), nil
}

// ---- pure party: libsignal-go, as the purego build uses it ----

type pureParty struct {
	id       uuid.UUID
	regID    uint32
	identity curve.KeyPair

	identities *inmem.IdentityKeyStore
	sessions   *inmem.SessionStore
	preKeys    *inmem.PreKeyStore
	signed     *inmem.SignedPreKeyStore
	kyber      *inmem.KyberPreKeyStore
	senderKeys *inmem.SenderKeyStore
}

func newPureParty(t *testing.T, regID uint32) *pureParty {
	t.Helper()

	identity := must(curve.GenerateKeyPair(rand.Reader))(t)

	return &pureParty{
		id:         uuid.New(),
		regID:      regID,
		identity:   identity,
		identities: inmem.NewIdentityKeyStore(identity, regID),
		sessions:   inmem.NewSessionStore(),
		preKeys:    inmem.NewPreKeyStore(),
		signed:     inmem.NewSignedPreKeyStore(),
		kyber:      inmem.NewKyberPreKeyStore(),
		senderKeys: inmem.NewSenderKeyStore(),
	}
}

func (*pureParty) backend() string                 { return "libsignal-go" }
func (p *pureParty) aci() uuid.UUID                { return p.id }
func (p *pureParty) registrationID() uint32        { return p.regID }
func (p *pureParty) identityKey(*testing.T) []byte { return p.identity.PublicKey.Serialize() }

func pureAddress(t *testing.T, party diffParty) address.ProtocolAddress {
	t.Helper()

	return address.NewProtocolAddress(
		address.NewACI(party.aci()).ServiceIDString(), must(address.NewDeviceID(diffDeviceID))(t))
}

func (p *pureParty) bundle(t *testing.T) diffBundle {
	t.Helper()

	preKey := must(curve.GenerateKeyPair(rand.Reader))(t)
	check(t, p.preKeys.SavePreKey(t.Context(), diffPreKeyID,
		must(session.NewPreKeyRecord(diffPreKeyID, preKey).Serialize())(t)))

	signed := must(curve.GenerateKeyPair(rand.Reader))(t)
	signedPublic := signed.PublicKey.Serialize()
	signedSignature := must(p.identity.PrivateKey.CalculateSignature(rand.Reader, signedPublic))(t)
	check(t, p.signed.SaveSignedPreKey(t.Context(), diffSignedPreKeyID, must(
		session.NewSignedPreKeyRecord(diffSignedPreKeyID, time.Now(), signed, signedSignature).Serialize())(t)))

	kyber := must(kem.GenerateKeyPair(kem.KeyTypeKyber1024, rand.Reader))(t)
	kyberPublic := kyber.PublicKey.Serialize()
	kyberSignature := must(p.identity.PrivateKey.CalculateSignature(rand.Reader, kyberPublic))(t)
	check(t, p.kyber.SaveKyberPreKey(t.Context(), diffKyberPreKeyID, must(
		session.NewKyberPreKeyRecord(diffKyberPreKeyID, time.Now(), kyber, kyberSignature).Serialize())(t)))

	return diffBundle{
		registrationID:        p.regID,
		preKeyID:              diffPreKeyID,
		preKey:                preKey.PublicKey.Serialize(),
		signedPreKeyID:        diffSignedPreKeyID,
		signedPreKey:          signedPublic,
		signedPreKeySignature: signedSignature,
		kyberPreKeyID:         diffKyberPreKeyID,
		kyberPreKey:           kyberPublic,
		kyberPreKeySignature:  kyberSignature,
		identityKey:           p.identityKey(t),
	}
}

func (p *pureParty) processBundle(t *testing.T, peer diffParty, b diffBundle) {
	t.Helper()

	preKeyID, preKey := b.preKeyID, must(curve.DeserializePublicKey(b.preKey))(t)
	bundle := must(session.NewPreKeyBundle(session.PreKeyBundleParams{
		RegistrationID:  b.registrationID,
		DeviceID:        diffDeviceID,
		PreKeyID:        &preKeyID,
		PreKey:          &preKey,
		SignedPreKeyID:  b.signedPreKeyID,
		SignedPreKey:    must(curve.DeserializePublicKey(b.signedPreKey))(t),
		SignedPreKeySig: b.signedPreKeySignature,
		KyberPreKeyID:   b.kyberPreKeyID,
		KyberPreKey:     must(kem.DeserializePublicKey(b.kyberPreKey))(t),
		KyberPreKeySig:  b.kyberPreKeySignature,
		IdentityKey:     must(curve.DeserializePublicKey(b.identityKey))(t),
	}))(t)
	check(t, session.ProcessPreKeyBundle(t.Context(), rand.Reader, pureAddress(t, peer), bundle,
		p.sessions, p.identities, session.WithLocalAddress(pureAddress(t, p)), session.WithClock(time.Now)))
}

// encryptMessage returns the serialized ciphertext and its libsignal message type.
func (p *pureParty) encryptMessage(t *testing.T, peer diffParty, plaintext []byte) ([]byte, uint8) {
	t.Helper()

	signal, preKey, err := session.MessageEncrypt(t.Context(), plaintext, pureAddress(t, peer), pureAddress(t, p),
		p.sessions, p.identities, time.Now(), rand.Reader)
	check(t, err)

	if preKey != nil {
		return preKey.Serialize(), uint8(libsignalgo.CiphertextMessageTypePreKey)
	}

	return signal.Serialize(), uint8(libsignalgo.CiphertextMessageTypeWhisper)
}

func (p *pureParty) encrypt(t *testing.T, peer diffParty, plaintext []byte) ([]byte, bool) {
	t.Helper()

	ciphertext, messageType := p.encryptMessage(t, peer, plaintext)

	return ciphertext, messageType == uint8(libsignalgo.CiphertextMessageTypePreKey)
}

func (p *pureParty) decrypt(t *testing.T, peer diffParty, ciphertext []byte, preKey bool) []byte {
	t.Helper()

	from, local := pureAddress(t, peer), pureAddress(t, p)
	if preKey {
		message := must(protocol.DeserializePreKeySignalMessage(ciphertext))(t)

		return must(session.MessageDecryptPreKey(t.Context(), message, from, local,
			p.sessions, p.identities, p.preKeys, p.signed, p.kyber, rand.Reader))(t)
	}

	message := must(protocol.DeserializeSignalMessage(ciphertext))(t)

	return must(session.MessageDecryptSignal(t.Context(), message, from, local,
		p.sessions, p.identities, rand.Reader))(t)
}

func (p *pureParty) session(t *testing.T, peer diffParty) []byte {
	t.Helper()

	record := must(p.sessions.LoadSession(t.Context(), pureAddress(t, peer)))(t)
	if record == nil {
		return nil
	}

	return must(record.Serialize())(t)
}

func (p *pureParty) createSKDM(t *testing.T, distributionID uuid.UUID) []byte {
	t.Helper()

	skdm := must(groups.CreateSenderKeyDistributionMessage(
		t.Context(), pureAddress(t, p), distributionID, p.senderKeys, rand.Reader))(t)

	return skdm.Serialized()
}

func (p *pureParty) processSKDM(t *testing.T, sender diffParty, skdm []byte) {
	t.Helper()

	message := must(protocol.DeserializeSenderKeyDistributionMessage(skdm))(t)
	check(t, groups.ProcessSenderKeyDistributionMessage(t.Context(), pureAddress(t, sender), message, p.senderKeys))
}

func (p *pureParty) groupEncrypt(t *testing.T, distributionID uuid.UUID, plaintext []byte) []byte {
	t.Helper()

	message := must(groups.Encrypt(
		t.Context(), pureAddress(t, p), distributionID, plaintext, p.senderKeys, rand.Reader))(t)

	return message.Serialized()
}

func (p *pureParty) groupDecrypt(t *testing.T, sender diffParty, ciphertext []byte) []byte {
	t.Helper()

	return must(groups.Decrypt(t.Context(), pureAddress(t, sender), ciphertext, p.senderKeys))(t)
}

func (p *pureParty) senderKey(t *testing.T, sender diffParty, distributionID uuid.UUID) []byte {
	t.Helper()

	return must(p.senderKeys.LoadSenderKey(t.Context(), pureAddress(t, sender), distributionID))(t)
}

func (p *pureParty) sealedEncrypt(t *testing.T, peer diffParty, trust diffTrust, plaintext []byte, sealV2 bool) []byte {
	t.Helper()

	serverCert := must(sealedsender.DeserializeServerCertificate(trust.serverCert))(t)
	serverKey := must(curve.DeserializePrivateKey(trust.serverKey))(t)
	e164 := "+491701234567"
	cert := must(sealedsender.NewSenderCertificate(p.id.String(), &e164, p.identity.PublicKey, diffDeviceID,
		time.Now().Add(time.Hour), serverCert, serverKey, rand.Reader))(t)

	ciphertext, messageType := p.encryptMessage(t, peer, plaintext)
	hint := sealedsender.ContentHint(libsignalgo.UnidentifiedSenderMessageContentHintResendable)
	usmc := must(sealedsender.NewUnidentifiedSenderMessageContent(messageType, cert, ciphertext, hint, nil))(t)

	theirIdentity := must(curve.DeserializePublicKey(peer.identityKey(t)))(t)
	if !sealV2 {
		return must(sealedsender.SealV1(usmc, p.identity, theirIdentity, rand.Reader))(t)
	}

	record := must(p.sessions.LoadSession(t.Context(), pureAddress(t, peer)))(t)
	recipient := sealedsender.SealV2Recipient{
		ServiceID:      address.NewACI(peer.aci()),
		IdentityKey:    theirIdentity,
		DeviceID:       diffDeviceID,
		RegistrationID: record.CurrentState().RemoteRegistrationID(),
	}

	return must(sealedsender.SealV2(usmc, []sealedsender.SealV2Recipient{recipient}, p.identity, rand.Reader))(t).
		Serialized()
}

func (p *pureParty) unseal(t *testing.T, trust diffTrust, sealed []byte) diffUnsealed {
	t.Helper()

	usmc := must(sealedsender.DecryptToUSMC(sealed, p.identity))(t)
	root := must(curve.DeserializePublicKey(trust.root))(t)

	return diffUnsealed{
		messageType: usmc.MessageType(),
		contents:    usmc.Contents(),
		senderUUID:  usmc.Sender().SenderUUID(),
		certValid:   must(usmc.Sender().Validate(root, sealedsender.WithClock(time.Now())))(t),
	}
}
