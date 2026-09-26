//go:build cgo && !purego

package signal_test

import (
	"bytes"
	"crypto/aes"
	"crypto/rand"
	"fmt"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/libsignal-go/accountkeys"
	"github.com/cwbudde/libsignal-go/address"
	"github.com/cwbudde/libsignal-go/aes256gcmsiv"
	"github.com/cwbudde/libsignal-go/curve"
	"github.com/cwbudde/libsignal-go/fingerprint"
	"github.com/cwbudde/libsignal-go/groups"
	"github.com/cwbudde/libsignal-go/identity"
	"github.com/cwbudde/libsignal-go/kem"
	"github.com/cwbudde/libsignal-go/protocol"
	"github.com/cwbudde/libsignal-go/session"
	"github.com/cwbudde/libsignal-go/usernames"
	"github.com/google/uuid"
	"go.mau.fi/mautrix-signal/pkg/libsignalgo"
)

// Differential tests (PLAN.md 7.3): the cgo backend (libsignal FFI) and the pure-Go code the
// purego build uses (libsignal-go) must agree on the same inputs: equal serialized records, and
// messages that one encrypts the other decrypts, in both directions. The protocol flows run
// between a cgoParty and a pureParty (purego_diff_parties_test.go).

func TestDiffUsernameHash(t *testing.T) {
	t.Parallel()

	names := []string{
		"he110.01", "He110.01", "HE110.01", "jimio.42", "a_b_c.999", "signal_user.1234567890",
		"_underscore.55", "abcdefghijklmnopqrstuvwxyz0123456789_.12",
		// Invalid: both must refuse them.
		"0zerostart.42", "no_discriminator", "🦀.42", "zero.00", "short.1", "a.", ".42", "",
		"nickname.042", "nickname.9999999999999999999999",
	}

	for _, name := range names {
		cgoHash, cgoErr := signal.UsernameHash(name)
		pureHash, pureErr := usernames.Hash(name)

		switch {
		case (cgoErr == nil) != (pureErr == nil):
			t.Errorf("%q: cgo error %v, purego error %v", name, cgoErr, pureErr)
		case cgoErr == nil && !bytes.Equal(cgoHash, pureHash[:]):
			t.Errorf("%q: cgo %x, purego %x", name, cgoHash, pureHash)
		}
	}
}

func TestDiffHPKE(t *testing.T) {
	t.Parallel()

	public, private := serializeKeys(t, identityKeys(t))
	info, aad := []byte("deviceCreatedAt"), []byte{3, 0, 0, 0, 7}

	for _, plaintext := range [][]byte{{}, []byte("x"), bytes.Repeat([]byte("0123456789"), 100)} {
		sealed, err := signal.HPKESeal(public, plaintext, info, aad)
		if err != nil {
			t.Fatal(err)
		}

		plain, err := signal.StdHPKEOpen(private, sealed, info, aad)
		if err != nil || !bytes.Equal(plain, plaintext) {
			t.Errorf("libsignal seal, crypto/hpke open: %q, %v", plain, err)
		}

		sealed, err = signal.StdHPKESeal(public, plaintext, info, aad)
		if err != nil {
			t.Fatal(err)
		}

		plain, err = signal.HPKEOpen(private, sealed, info, aad)
		if err != nil || !bytes.Equal(plain, plaintext) {
			t.Errorf("crypto/hpke seal, libsignal open: %q, %v", plain, err)
		}
	}
}

func TestDiffKeys(t *testing.T) {
	t.Parallel()

	message := []byte("the same key on both backends")

	cgoKey := must(libsignalgo.GeneratePrivateKey())(t)
	cgoPrivate := must(cgoKey.Serialize())(t)
	cgoPublic := must(must(cgoKey.GetPublicKey())(t).Serialize())(t)

	pureKey := must(curve.GenerateKeyPair(rand.Reader))(t)

	// Keys cross over and serialize back to the same bytes.
	fromCGO := must(curve.DeserializePrivateKey(cgoPrivate))(t)
	requireEqual(t, "cgo private key in libsignal-go", fromCGO.Serialize(), cgoPrivate)
	requireEqual(t, "public key of a cgo private key", must(fromCGO.PublicKey())(t).Serialize(), cgoPublic)

	fromPure := must(libsignalgo.DeserializePrivateKey(pureKey.PrivateKey.Serialize()))(t)
	requireEqual(t, "libsignal-go private key in cgo", must(fromPure.Serialize())(t), pureKey.PrivateKey.Serialize())
	requireEqual(t, "public key of a libsignal-go private key",
		must(must(fromPure.GetPublicKey())(t).Serialize())(t), pureKey.PublicKey.Serialize())

	// XEdDSA signatures verify on the other backend.
	pureOfCGO := must(curve.DeserializePublicKey(cgoPublic))(t)
	if !pureOfCGO.VerifySignature(must(cgoKey.Sign(message))(t), message) {
		t.Error("libsignal-go rejects a cgo signature")
	}

	cgoOfPure := must(libsignalgo.DeserializePublicKey(pureKey.PublicKey.Serialize()))(t)
	if !must(cgoOfPure.Verify(message, must(pureKey.PrivateKey.CalculateSignature(rand.Reader, message))(t)))(t) {
		t.Error("cgo rejects a libsignal-go signature")
	}

	// Both sides of a key agreement compute the same secret.
	requireEqual(t, "agreement",
		must(cgoKey.Agree(cgoOfPure))(t), must(pureKey.PrivateKey.CalculateAgreement(pureOfCGO))(t))

	// Identity key pairs serialize alike, and alternate-identity signatures verify across.
	cgoIdentity := identityKeys(t)
	pureIdentity := must(identity.DeserializeKeyPair(must(cgoIdentity.Serialize())(t)))(t)
	requireEqual(t, "identity key pair", identity.SerializeKeyPair(pureIdentity), must(cgoIdentity.Serialize())(t))

	other := identityKeys(t)
	otherPublic := must(curve.DeserializePublicKey(must(other.GetPublicKey().Serialize())(t)))(t)
	signature := must(cgoIdentity.SignAlternateIdentity(other.GetIdentityKey()))(t)

	if !identity.VerifyAlternateIdentity(pureIdentity.PublicKey, otherPublic, signature) {
		t.Error("libsignal-go rejects a cgo alternate-identity signature")
	}

	signature = must(identity.SignAlternateIdentity(pureIdentity.PrivateKey, otherPublic, rand.Reader))(t)
	if !must(cgoIdentity.GetIdentityKey().VerifyAlternateIdentity(other.GetIdentityKey(), signature))(t) {
		t.Error("cgo rejects a libsignal-go alternate-identity signature")
	}
}

func TestDiffPreKeyRecords(t *testing.T) {
	t.Parallel()

	timestamp := time.UnixMilli(1_700_000_000_123)
	cgoKey := must(libsignalgo.GeneratePrivateKey())(t)
	pureKey := must(curve.DeserializePrivateKey(must(cgoKey.Serialize())(t)))(t)
	pair := must(curve.KeyPairFromPrivateKey(pureKey))(t)
	signature := must(identityKeys(t).GetPrivateKey().Sign([]byte("signed pre-key")))(t)

	// The same key material gives the same records.
	cgoPreKey := must(must(libsignalgo.NewPreKeyRecordFromPrivateKey(diffPreKeyID, cgoKey))(t).Serialize())(t)
	purePreKey := must(session.NewPreKeyRecord(diffPreKeyID, pair).Serialize())(t)
	requireEqual(t, "pre-key record", purePreKey, cgoPreKey)
	requireEqual(t, "cgo pre-key record in libsignal-go",
		must(must(session.DeserializePreKeyRecord(cgoPreKey))(t).Serialize())(t), cgoPreKey)

	cgoSigned := must(must(libsignalgo.NewSignedPreKeyRecordFromPrivateKey(
		diffSignedPreKeyID, timestamp, cgoKey, signature))(t).Serialize())(t)
	pureSigned := must(session.NewSignedPreKeyRecord(diffSignedPreKeyID, timestamp, pair, signature).Serialize())(t)
	requireEqual(t, "signed pre-key record", pureSigned, cgoSigned)
	requireEqual(t, "cgo signed pre-key record in libsignal-go",
		must(must(session.DeserializeSignedPreKeyRecord(cgoSigned))(t).Serialize())(t), cgoSigned)

	// The cgo API can't export a Kyber secret key, so Kyber records only cross over: each
	// backend's record loads in the other and serializes back to the same bytes.
	cgoKyber := must(must(libsignalgo.NewKyberPreKeyRecord(diffKyberPreKeyID, timestamp,
		must(libsignalgo.KyberKeyPairGenerate())(t), signature))(t).Serialize())(t)
	requireEqual(t, "cgo Kyber pre-key record in libsignal-go",
		must(must(session.DeserializeKyberPreKeyRecord(cgoKyber))(t).Serialize())(t), cgoKyber)

	kyberPair := must(kem.GenerateKeyPair(kem.KeyTypeKyber1024, rand.Reader))(t)
	pureKyber := must(session.NewKyberPreKeyRecord(diffKyberPreKeyID, timestamp, kyberPair, signature).Serialize())(t)
	kyberRecord := must(libsignalgo.DeserializeKyberPreKeyRecord(pureKyber))(t)
	requireEqual(t, "libsignal-go Kyber pre-key record in cgo", must(kyberRecord.Serialize())(t), pureKyber)
	requireEqual(t, "Kyber public key",
		must(must(kyberRecord.GetPublicKey())(t).Serialize())(t), kyberPair.PublicKey.Serialize())
}

// diffPairs returns the two directions every flow runs in: cgo initiates, and libsignal-go
// initiates.
func diffPairs(t *testing.T) [][2]diffParty {
	t.Helper()

	return [][2]diffParty{
		{newCGOParty(t, 1111), newPureParty(t, 2222)},
		{newPureParty(t, 3333), newCGOParty(t, 4444)},
	}
}

// establish runs a pre-key exchange from a to b and b's answer, which completes the session.
func establish(t *testing.T, a, b diffParty) {
	t.Helper()

	a.processBundle(t, b, b.bundle(t))

	for i := range 2 {
		plaintext := fmt.Appendf(nil, "%s to %s, pre-key message %d", a.backend(), b.backend(), i)

		ciphertext, preKey := a.encrypt(t, b, plaintext)
		if !preKey {
			t.Fatalf("message %d before an answer is not a pre-key message", i)
		}

		requireEqual(t, "pre-key message", b.decrypt(t, a, ciphertext, true), plaintext)
	}

	exchange(t, b, a, "answer")
}

// exchange sends one message from a to b, which must be a normal (not pre-key) message.
func exchange(t *testing.T, a, b diffParty, label string) {
	t.Helper()

	plaintext := fmt.Appendf(nil, "%s: %s to %s", label, a.backend(), b.backend())

	ciphertext, preKey := a.encrypt(t, b, plaintext)
	if preKey {
		t.Fatalf("%s: unexpected pre-key message", label)
	}

	requireEqual(t, label, b.decrypt(t, a, ciphertext, false), plaintext)
}

func TestDiffSessions(t *testing.T) {
	t.Parallel()

	for _, pair := range diffPairs(t) {
		a, b := pair[0], pair[1]
		t.Run(a.backend()+" initiates", func(t *testing.T) {
			t.Parallel()

			establish(t, a, b)

			for i := range 3 {
				exchange(t, a, b, fmt.Sprintf("round %d", i))
				exchange(t, b, a, fmt.Sprintf("round %d reply", i))
			}

			// Several messages in a row, delivered out of order.
			first, _ := a.encrypt(t, b, []byte("first"))
			second, _ := a.encrypt(t, b, []byte("second"))
			requireEqual(t, "second", b.decrypt(t, a, second, false), []byte("second"))
			requireEqual(t, "first", b.decrypt(t, a, first, false), []byte("first"))

			// Each side's session record loads in the other backend and serializes back to the
			// same bytes.
			for _, party := range []diffParty{a, b} {
				peer := b
				if party == b {
					peer = a
				}

				record := party.session(t, peer)
				requireEqual(t, party.backend()+" session in libsignal-go",
					must(must(session.DeserializeSessionRecord(record))(t).Serialize())(t), record)
				requireEqual(t, party.backend()+" session in cgo",
					must(must(libsignalgo.DeserializeSessionRecord(record))(t).Serialize())(t), record)
			}
		})
	}
}

func TestDiffGroupCipher(t *testing.T) {
	t.Parallel()

	for _, pair := range diffPairs(t) {
		sender, receiver := pair[0], pair[1]
		t.Run(sender.backend()+" sends", func(t *testing.T) {
			t.Parallel()

			distributionID := uuid.New()
			receiver.processSKDM(t, sender, sender.createSKDM(t, distributionID))

			ciphertexts := make([][]byte, 3)
			for i := range ciphertexts {
				ciphertexts[i] = sender.groupEncrypt(t, distributionID, fmt.Appendf(nil, "group message %d", i))
			}

			// In order, then one that was skipped.
			for _, i := range []int{0, 2, 1} {
				requireEqual(t, fmt.Sprintf("group message %d", i),
					receiver.groupDecrypt(t, sender, ciphertexts[i]), fmt.Appendf(nil, "group message %d", i))
			}

			// Both sides' sender key records load in the other backend and serialize back to the
			// same bytes.
			for _, party := range []diffParty{sender, receiver} {
				record := party.senderKey(t, sender, distributionID)
				requireEqual(t, party.backend()+" sender key in libsignal-go",
					must(must(groups.DeserializeSenderKeyRecord(record))(t).Serialize())(t), record)
				requireEqual(t, party.backend()+" sender key in cgo",
					must(must(libsignalgo.DeserializeSenderKeyRecord(record))(t).Serialize())(t), record)
			}
		})
	}
}

func TestDiffSealedSender(t *testing.T) {
	t.Parallel()

	for _, pair := range diffPairs(t) {
		a, b := pair[0], pair[1]
		t.Run(a.backend()+" seals", func(t *testing.T) {
			t.Parallel()

			trust := newDiffTrust(t)
			establish(t, a, b)

			for _, sealV2 := range []bool{false, true} {
				plaintext := fmt.Appendf(nil, "sealed (v2 %v) from %s", sealV2, a.backend())

				sealed := a.sealedEncrypt(t, b, trust, plaintext, sealV2)
				if sealV2 {
					sealed = receivedV2(t, sealed)
				}

				unsealed := b.unseal(t, trust, sealed)
				if !unsealed.certValid {
					t.Errorf("v2 %v: sender certificate doesn't validate", sealV2)
				}

				if unsealed.senderUUID != a.aci().String() {
					t.Errorf("v2 %v: sender %s, want %s", sealV2, unsealed.senderUUID, a.aci())
				}

				if unsealed.messageType != uint8(libsignalgo.CiphertextMessageTypeWhisper) {
					t.Fatalf("v2 %v: message type %d", sealV2, unsealed.messageType)
				}

				requireEqual(t, fmt.Sprintf("v2 %v", sealV2), b.decrypt(t, a, unsealed.contents, false), plaintext)
			}
		})
	}
}

func TestDiffDecryptionErrorMessage(t *testing.T) {
	t.Parallel()

	const timestamp, deviceID = uint64(1_700_000_000_456), uint32(3)

	for _, pair := range diffPairs(t) {
		a, b := pair[0], pair[1]
		t.Run(a.backend()+" original", func(t *testing.T) {
			t.Parallel()

			a.processBundle(t, b, b.bundle(t))
			preKey, _ := a.encrypt(t, b, []byte("pre-key message"))
			distributionID := uuid.New()
			a.createSKDM(t, distributionID)

			originals := map[libsignalgo.CiphertextMessageType][]byte{
				libsignalgo.CiphertextMessageTypePreKey:    preKey,
				libsignalgo.CiphertextMessageTypeSenderKey: a.groupEncrypt(t, distributionID, []byte("group")),
			}

			for messageType, original := range originals {
				cgo := must(libsignalgo.DecryptionErrorMessageForOriginalMessage(
					original, messageType, timestamp, uint(deviceID)))(t)
				pure := must(protocol.DecryptionErrorMessageForOriginal(
					original, uint8(messageType), timestamp, deviceID))(t)
				requireEqual(t, fmt.Sprintf("decryption error message for type %d", messageType),
					must(cgo.Serialize())(t), pure.Serialized())

				cgoContent := must(libsignalgo.PlaintextContentFromDecryptionErrorMessage(cgo))(t)
				pureContent := must(protocol.NewPlaintextContentFromDecryptionError(pure))(t)
				requireEqual(t, "plaintext content", must(cgoContent.Serialize())(t), pureContent.Serialized())

				// Each side extracts the message from the other's plaintext content body.
				fromPure := must(libsignalgo.DecryptionErrorMessageFromSerializedContent(pureContent.Body()))(t)
				requireEqual(t, "decryption error message from a libsignal-go body",
					must(fromPure.Serialize())(t), pure.Serialized())

				fromCGO := must(protocol.ExtractDecryptionErrorMessageFromSerializedContent(
					must(cgoContent.GetBody())(t)))(t)
				requireEqual(t, "decryption error message from a cgo body",
					fromCGO.Serialized(), must(cgo.Serialize())(t))
			}
		})
	}
}

func TestDiffServiceIDs(t *testing.T) {
	t.Parallel()

	rawUUID := uuid.New()
	pairs := []struct {
		cgo  libsignalgo.ServiceID
		pure address.ServiceID
	}{
		{libsignalgo.NewACIServiceID(rawUUID), address.NewACI(rawUUID)},
		{libsignalgo.NewPNIServiceID(rawUUID), address.NewPNI(rawUUID)},
	}

	for _, pair := range pairs {
		if pair.cgo.String() != pair.pure.ServiceIDString() {
			t.Errorf("string: cgo %s, libsignal-go %s", pair.cgo, pair.pure.ServiceIDString())
		}

		requireEqual(t, "binary", pair.cgo.Bytes(), pair.pure.ServiceIDBinary())

		fixed := pair.pure.ServiceIDFixedWidthBinary()
		requireEqual(t, "fixed-width binary", pair.cgo.FixedBytes()[:], fixed[:])

		parsed := must(address.ParseServiceIDString(pair.cgo.String()))(t)
		if parsed != pair.pure {
			t.Errorf("libsignal-go parses %s as %s", pair.cgo, parsed.ServiceIDString())
		}

		if cgo := must(libsignalgo.ServiceIDFromString(pair.pure.ServiceIDString()))(t); cgo != pair.cgo {
			t.Errorf("cgo parses %s as %s", pair.pure.ServiceIDString(), cgo)
		}
	}
}

// receivedV2 turns a sealed sender v2 message sent to one device into the form that device
// receives from the server (sealed_sender_multi_recipient_message_for_single_recipient):
//
//	sent:     0x23 || count=1 || service ID[17] || device || registration ID[2] || C[32] || AT[16] || rest
//	received: 0x22 || C[32] || AT[16] || rest
func receivedV2(t *testing.T, sent []byte) []byte {
	t.Helper()

	const (
		header  = 1 + 1 + address.ServiceIDFixedWidthBinaryLen + 1 + 2
		keyAuth = 32 + 16
	)

	if len(sent) < header+keyAuth || sent[0] != 0x23 || sent[1] != 1 {
		t.Fatalf("not a single-recipient sealed sender v2 message: %x", sent[:min(len(sent), 4)])
	}

	return append([]byte{0x22}, sent[header:]...)
}

func TestDiffFingerprint(t *testing.T) {
	t.Parallel()

	local, remote := identityKeys(t).GetPublicKey(), identityKeys(t).GetPublicKey()
	localID, remoteID := []byte(uuid.New().String()), []byte(uuid.New().String())
	cgo := must(libsignalgo.NewFingerprint(5200, libsignalgo.FingerprintVersionV2,
		localID, local, remoteID, remote))(t)
	pure := must(fingerprint.New(2, 5200,
		localID, must(curve.DeserializePublicKey(must(local.Serialize())(t)))(t),
		remoteID, must(curve.DeserializePublicKey(must(remote.Serialize())(t)))(t)))(t)

	if cgoDisplay := must(cgo.DisplayString())(t); cgoDisplay != pure.DisplayString() {
		t.Errorf("display: cgo %s, libsignal-go %s", cgoDisplay, pure.DisplayString())
	}

	requireEqual(t, "scannable", must(pure.Scannable.Serialize())(t), must(cgo.ScannableEncoding())(t))
}

func TestDiffAES256GCMSIV(t *testing.T) {
	t.Parallel()

	key, nonce, aad := randomBytes(t, 32), randomBytes(t, 12), []byte("associated")
	cgo := must(libsignalgo.NewAES256_GCM_SIV(key))(t)
	pure := must(aes256gcmsiv.New(key))(t)

	for _, plaintext := range [][]byte{{}, []byte("x"), bytes.Repeat([]byte("0123456789"), 100)} {
		ciphertext := must(cgo.Encrypt(plaintext, nonce, aad))(t)
		requireEqual(t, "ciphertext", must(pure.Encrypt(plaintext, nonce, aad))(t), ciphertext)
		requireEqual(t, "plaintext", must(pure.Decrypt(ciphertext, nonce, aad))(t), plaintext)
	}
}

func TestDiffAccessKey(t *testing.T) {
	t.Parallel()

	// The purego build derives the access key with crypto/aes (profilekey_purego.go):
	// AES-256 under the profile key of the block 0...02.
	profileKey := libsignalgo.ProfileKey(randomBytes(t, libsignalgo.ProfileKeyLength))
	block := must(aes.NewCipher(profileKey[:]))(t)

	var in, want libsignalgo.AccessKey

	in[len(in)-1] = 2
	block.Encrypt(want[:], in[:])
	requireEqual(t, "access key", must(profileKey.DeriveAccessKey())(t)[:], want[:])
}

func TestDiffAccountEntropyPool(t *testing.T) {
	t.Parallel()

	pool := must(accountkeys.GenerateAccountEntropyPool())(t)
	cgoPool := libsignalgo.AccountEntropyPool(pool.String())
	aci := uuid.New()
	pureACI := address.NewACI(aci)
	backupKey := accountkeys.DeriveBackupKey(pool)
	svrKey := pool.DeriveSVRKey()

	requireEqual(t, "SVR key", must(cgoPool.DeriveSVRKey())(t), svrKey[:])
	requireEqual(t, "backup key", must(cgoPool.DeriveBackupKey())(t), backupKey[:])

	cgoBackupKey := libsignalgo.BytesToBackupKey(backupKey[:])
	backupID := backupKey.DeriveBackupID(pureACI)
	requireEqual(t, "backup ID",
		must(cgoBackupKey.DeriveBackupID(libsignalgo.NewACIServiceID(aci)))(t)[:], backupID[:])
	requireEqual(t, "backup EC key",
		must(must(cgoBackupKey.DeriveECKey(libsignalgo.NewACIServiceID(aci)))(t).Serialize())(t),
		must(backupKey.DeriveECKey(pureACI))(t).Serialize())

	mediaID := backupKey.DeriveMediaID("media")
	requireEqual(t, "media ID", must(cgoBackupKey.DeriveMediaID("media"))(t)[:], mediaID[:])

	hmacKey, aesKey := backupKey.DeriveMessageBackupKey(backupID)
	messageBackupKey := must(libsignalgo.MessageBackupKeyFromAccountEntropyPool(
		cgoPool, libsignalgo.NewACIServiceID(aci)))(t)
	cgoHMAC, cgoAES := must(messageBackupKey.GetHMACKey())(t), must(messageBackupKey.GetAESKey())(t)
	requireEqual(t, "message backup HMAC key", cgoHMAC[:], hmacKey[:])
	requireEqual(t, "message backup AES key", cgoAES[:], aesKey[:])
}

func requireEqual(t *testing.T, what string, got, want []byte) {
	t.Helper()

	if !bytes.Equal(got, want) {
		t.Fatalf("%s: got %x, want %x", what, got, want)
	}
}

func randomBytes(t *testing.T, n int) []byte {
	t.Helper()

	b := make([]byte, n)
	must(rand.Read(b))(t)

	return b
}
