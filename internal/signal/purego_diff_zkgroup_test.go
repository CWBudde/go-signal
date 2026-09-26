//go:build cgo && !purego

package signal_test

import (
	"crypto/rand"
	"testing"

	"github.com/cwbudde/libsignal-go/address"
	"github.com/cwbudde/libsignal-go/zkgroup"
	"github.com/google/uuid"
	"go.mau.fi/mautrix-signal/pkg/libsignalgo"
)

func TestDiffZKGroup(t *testing.T) {
	t.Parallel()

	for range 8 {
		var randomness [32]byte

		_, err := rand.Read(randomness[:])
		check(t, err)

		pure := zkgroup.GenerateGroupSecretParams(randomness)
		native := must(libsignalgo.GenerateGroupSecretParamsWithRandomness(libsignalgo.Randomness(randomness)))(t)
		requireEqual(t, "group parameters", pure.Bytes(), native[:])
		public := must(native.GetPublicParams())(t)
		requireEqual(t, "public parameters", pure.Public().Bytes(), public[:])
		identifier := must(libsignalgo.GetGroupIdentifier(*public))(t)
		pureID := pure.Public().Identifier()
		requireEqual(t, "group identifier", pureID[:], identifier[:])
		master := must(native.GetMasterKey())(t)
		requireEqual(t, "master-key derivation",
			zkgroup.DeriveGroupSecretParams(zkgroup.GroupMasterKey(*master)).Bytes(), native[:])

		aci := uuid.New()
		for _, serviceID := range []libsignalgo.ServiceID{
			libsignalgo.NewACIServiceID(aci), libsignalgo.NewPNIServiceID(aci),
		} {
			pureServiceID := must(address.ParseServiceIDBinary(serviceID.Bytes()))(t)
			ciphertext := must(native.EncryptServiceID(serviceID))(t)
			pureCiphertext := pure.EncryptServiceID(pureServiceID)
			requireEqual(t, "service ID ciphertext", pureCiphertext.Bytes(), ciphertext[:])
			decoded := must(pure.DecryptServiceID(must(zkgroup.ParseUUIDCiphertext(ciphertext[:]))(t)))(t)
			requireEqual(t, "CGO service ID in Go", decoded.ServiceIDBinary(), serviceID.Bytes())
			nativeDecoded := must(native.DecryptServiceID(libsignalgo.UUIDCiphertext(pureCiphertext.Bytes())))(t)
			requireEqual(t, "Go service ID in CGO", nativeDecoded.Bytes(), serviceID.Bytes())
		}

		diffZKProfileKey(t, pure, &native, aci)
		diffZKBlob(t, pure, &native, randomness)
	}
}

func diffZKProfileKey(
	t *testing.T, pure *zkgroup.GroupSecretParams, native *libsignalgo.GroupSecretParams, aci uuid.UUID,
) {
	t.Helper()

	var key zkgroup.ProfileKey

	_, err := rand.Read(key[:])
	check(t, err)

	nativeKey := libsignalgo.ProfileKey(key)
	ciphertext := must(native.EncryptProfileKey(nativeKey, aci))(t)
	pureCiphertext := pure.EncryptProfileKey(key, [16]byte(aci))
	requireEqual(t, "profile ciphertext", pureCiphertext.Bytes(), ciphertext[:])
	decoded := must(pure.DecryptProfileKey(must(zkgroup.ParseProfileKeyCiphertext(ciphertext[:]))(t), [16]byte(aci)))(t)
	requireEqual(t, "CGO profile in Go", decoded[:], key[:])
	nativeDecoded := must(native.DecryptProfileKey(libsignalgo.ProfileKeyCiphertext(pureCiphertext.Bytes()), aci))(t)
	requireEqual(t, "Go profile in CGO", nativeDecoded[:], key[:])
	commitment := key.Commitment([16]byte(aci))
	nativeCommitment := must(nativeKey.GetCommitment(aci))(t)
	requireEqual(t, "profile commitment", commitment[:], nativeCommitment[:])

	version := key.Version([16]byte(aci))
	nativeVersion := must(nativeKey.GetProfileKeyVersion(aci))(t)
	requireEqual(t, "profile version", version[:], nativeVersion[:])
}

func diffZKBlob(
	t *testing.T, pure *zkgroup.GroupSecretParams, native *libsignalgo.GroupSecretParams, randomness [32]byte,
) {
	t.Helper()

	for _, padding := range []uint32{0, 8, 64} {
		message := []byte("encrypted group title")
		pureBlob := must(pure.EncryptBlob(randomness, message, padding))(t)
		nativeBlob := must(native.EncryptBlobWithPaddingDeterministic(
			libsignalgo.Randomness(randomness), message, padding))(t)
		requireEqual(t, "padded blob", pureBlob, nativeBlob)
		requireEqual(t, "CGO blob in Go", must(pure.DecryptBlob(nativeBlob))(t), message)
		requireEqual(t, "Go blob in CGO", must(native.DecryptBlobWithPadding(pureBlob))(t), message)
	}
}
