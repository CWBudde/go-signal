//go:build cgo || libsignal_go

package store_test

import (
	"bytes"
	"encoding/base64"
	"errors"
	"log/slog"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/internal/store"
	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"go.mau.fi/mautrix-signal/pkg/libsignalgo"
	mstore "go.mau.fi/mautrix-signal/pkg/signalmeow/store"
)

// The ordinary suite checks restarts with its own backend. test-backend-switch.sh
// runs the same steps in separate cgo and purego binaries against the same files,
// covering both SQLite drivers, the shim adapters and continued protocol state.
func TestProtocolStateSurvivesReopen(t *testing.T) {
	t.Parallel()

	path := t.TempDir()
	protocolStateStep(t, path, true)

	for range 3 {
		protocolStateStep(t, path, false)
	}
}

func TestBackendSwitchStep(t *testing.T) {
	t.Parallel()

	path := os.Getenv("GOSIGNAL_TEST_BACKEND_DIR")
	if path == "" {
		t.Skip("run through scripts/test-backend-switch.sh")
	}

	action := os.Getenv("GOSIGNAL_TEST_BACKEND_ACTION")
	if action != "init" && action != "advance" {
		t.Fatalf("unknown backend test action %q", action)
	}

	protocolStateStep(t, path, action == "init")
}

type protocolAccount struct {
	data   *store.Store
	device *mstore.Device
	addr   *libsignalgo.Address
}

func protocolStateStep(t *testing.T, path string, initialize bool) {
	t.Helper()

	dir := stateValue(store.OpenDir(path, slog.New(slog.DiscardHandler)))(t)
	accounts := make([]protocolAccount, 2)

	for i, aci := range []string{testACI, secondACI} {
		data := stateValue(dir.OpenAccount(t.Context(), aci, zerolog.Nop()))(t)
		defer func() { stateCheck(t, data.Close()) }()

		if initialize {
			device := newDevice(t, uuid.MustParse(aci))
			device.ACIRegistrationID = 123 + i
			device.PNIRegistrationID = 456 + i
			stateCheck(t, data.Devices.PutDevice(t.Context(), device))
			statePut(t, data, "aci-key", stateValue(device.ACIIdentityKeyPair.Serialize())(t))
			statePut(t, data, "pni-key", stateValue(device.PNIIdentityKeyPair.Serialize())(t))
		}

		device := stateValue(data.Devices.DeviceByACI(t.Context(), uuid.MustParse(aci)))(t)
		if device == nil || !device.IsDeviceLoggedIn() {
			t.Fatal("persisted device is missing or logged out")
		}

		stateEqual(t, stateValue(device.ACIIdentityKeyPair.Serialize())(t), stateGet(t, data, "aci-key"))
		stateEqual(t, stateValue(device.PNIIdentityKeyPair.Serialize())(t), stateGet(t, data, "pni-key"))
		accounts[i] = protocolAccount{
			data: data, device: device,
			addr: stateValue(device.ACIServiceID().Address(uint(device.DeviceID)))(t),
		}
	}

	alice, bob := accounts[0], accounts[1]
	if initialize {
		initializeProtocolState(t, alice, bob)
		return
	}

	step := stateValue(strconv.Atoi(string(stateGet(t, alice.data, "step"))))(t)
	advanceSessionState(t, accounts, step)

	distribution := uuid.MustParse(string(stateGet(t, alice.data, "distribution")))
	if step > 0 {
		// This message was consumed before the previous process closed its DB.
		_, err := libsignalgo.GroupDecrypt(t.Context(), stateGet(t, alice.data, "group-replay"),
			alice.addr, bob.device.SenderKeyStore)
		if !errors.Is(err, libsignalgo.ErrorCodeDuplicatedMessage) {
			t.Fatalf("group replay after reopening: got %v, want duplicated message", err)
		}
	}

	groupCiphertext := stateGet(t, alice.data, "group-pending")
	rejectPendingGroupTampering(t, alice, bob, distribution, groupCiphertext)
	plain := stateValue(libsignalgo.GroupDecrypt(t.Context(), groupCiphertext, alice.addr, bob.device.SenderKeyStore))(t)
	stateEqual(t, plain, []byte("group "+strconv.Itoa(step)))
	statePut(t, alice.data, "group-replay", groupCiphertext)
	queueGroupMessage(t, alice, bob, distribution, step+1)
	statePut(t, alice.data, "step", []byte(strconv.Itoa(step+1)))
}

func advanceSessionState(t *testing.T, accounts []protocolAccount, step int) {
	t.Helper()

	data := accounts[0].data

	sender, receiver := accounts[step%2], accounts[(step+1)%2]
	if step > 1 {
		// The previous step received in the opposite direction. Its replay state
		// must survive closing the DB and changing the backend.
		message := stateValue(libsignalgo.DeserializeMessage(stateGet(t, data, "session-replay")))(t)

		_, err := libsignalgo.Decrypt(t.Context(), message, receiver.addr, sender.addr,
			sender.device.ACISessionStore, sender.device.ACIIdentityStore)
		if !errors.Is(err, libsignalgo.ErrorCodeDuplicatedMessage) {
			t.Fatalf("session replay after reopening: got %v, want duplicated message", err)
		}
	}

	ciphertext := stateGet(t, data, "pending")
	rejectPendingSessionTampering(t, sender, receiver, ciphertext, step == 0)

	var plain []byte

	if step == 0 {
		message := stateValue(libsignalgo.DeserializePreKeyMessage(ciphertext))(t)
		keys := receiver.device.ACIPreKeyStore
		plain = stateValue(libsignalgo.DecryptPreKey(t.Context(), message, sender.addr, receiver.addr,
			receiver.device.ACISessionStore, receiver.device.ACIIdentityStore, keys, keys, keys))(t)
		// The one-time keys must be consumed through the real store adapters.
		if stateValue(keys.LoadPreKey(t.Context(), 1))(t) != nil ||
			stateValue(keys.LoadKyberPreKey(t.Context(), 3))(t) != nil {
			t.Fatal("one-time prekeys survived successful decryption")
		}
	} else {
		message := stateValue(libsignalgo.DeserializeMessage(ciphertext))(t)
		plain = stateValue(libsignalgo.Decrypt(t.Context(), message, sender.addr, receiver.addr,
			receiver.device.ACISessionStore, receiver.device.ACIIdentityStore))(t)
	}

	stateEqual(t, plain, []byte("message "+strconv.Itoa(step)))
	statePut(t, data, "session-replay", ciphertext)
	queueSessionMessage(t, receiver, sender, data, step+1)
}

func initializeProtocolState(t *testing.T, alice, bob protocolAccount) {
	t.Helper()

	pre := stateValue(libsignalgo.GeneratePrivateKey())(t)
	signed := stateValue(libsignalgo.GeneratePrivateKey())(t)
	kyber := stateValue(libsignalgo.KyberKeyPairGenerate())(t)
	signedPublic := stateValue(signed.GetPublicKey())(t)
	kyberPublic := stateValue(kyber.GetPublicKey())(t)
	identity := bob.device.ACIIdentityKeyPair
	signedSig := stateValue(identity.GetPrivateKey().Sign(stateValue(signedPublic.Serialize())(t)))(t)
	kyberSig := stateValue(identity.GetPrivateKey().Sign(stateValue(kyberPublic.Serialize())(t)))(t)
	keys := bob.device.ACIPreKeyStore
	stateCheck(t, keys.StorePreKey(t.Context(), 1, stateValue(libsignalgo.NewPreKeyRecordFromPrivateKey(1, pre))(t)))
	stateCheck(t, keys.StoreSignedPreKey(t.Context(), 2,
		stateValue(libsignalgo.NewSignedPreKeyRecordFromPrivateKey(2, time.Now(), signed, signedSig))(t)))
	stateCheck(t, keys.StoreKyberPreKey(t.Context(), 3,
		stateValue(libsignalgo.NewKyberPreKeyRecord(3, time.Now(), kyber, kyberSig))(t)))

	bundle := stateValue(libsignalgo.NewPreKeyBundle(uint32(bob.device.ACIRegistrationID), uint32(bob.device.DeviceID),
		1, stateValue(pre.GetPublicKey())(t), 2, signedPublic, signedSig, 3, kyberPublic, kyberSig,
		identity.GetIdentityKey()))(t)
	stateCheck(t, libsignalgo.ProcessPreKeyBundle(t.Context(), bundle, bob.addr, alice.addr,
		alice.device.ACISessionStore, alice.device.ACIIdentityStore))
	queueSessionMessage(t, alice, bob, alice.data, 0)

	distribution := uuid.New()
	skdm := stateValue(libsignalgo.NewSenderKeyDistributionMessage(
		t.Context(), alice.addr, distribution, alice.device.SenderKeyStore))(t)
	stateCheck(t, libsignalgo.ProcessSenderKeyDistributionMessage(
		t.Context(), skdm, alice.addr, bob.device.SenderKeyStore))
	queueGroupMessage(t, alice, bob, distribution, 0)
	statePut(t, alice.data, "distribution", []byte(distribution.String()))
	statePut(t, alice.data, "step", []byte("0"))
}

func queueSessionMessage(t *testing.T, sender, receiver protocolAccount, data *store.Store, step int) {
	t.Helper()

	message := stateValue(libsignalgo.Encrypt(t.Context(), []byte("message "+strconv.Itoa(step)),
		receiver.addr, sender.addr, sender.device.ACISessionStore, sender.device.ACIIdentityStore))(t)

	wantType := libsignalgo.CiphertextMessageTypeWhisper
	if step == 0 {
		wantType = libsignalgo.CiphertextMessageTypePreKey
	}

	if got := stateValue(message.MessageType())(t); got != wantType {
		t.Fatalf("step %d: message type %v, want %v", step, got, wantType)
	}

	statePut(t, data, "pending", stateValue(message.Serialize())(t))

	if step > 0 {
		// Leave a skipped session key on disk too, including the first reply
		// that acknowledges the prekey exchange. The next backend must use it.
		later := stateValue(libsignalgo.Encrypt(t.Context(), []byte("later session message"),
			receiver.addr, sender.addr, sender.device.ACISessionStore, sender.device.ACIIdentityStore))(t)
		decoded := stateValue(libsignalgo.DeserializeMessage(stateValue(later.Serialize())(t)))(t)
		plain := stateValue(libsignalgo.Decrypt(t.Context(), decoded, sender.addr, receiver.addr,
			receiver.device.ACISessionStore, receiver.device.ACIIdentityStore))(t)
		stateEqual(t, plain, []byte("later session message"))
	}
}

func queueGroupMessage(t *testing.T, alice, bob protocolAccount, distribution uuid.UUID, step int) {
	t.Helper()

	message := stateValue(libsignalgo.GroupEncrypt(t.Context(), []byte("group "+strconv.Itoa(step)),
		alice.addr, distribution, alice.device.SenderKeyStore))(t)
	statePut(t, alice.data, "group-pending", stateValue(message.Serialize())(t))

	// Decrypt a later message now, leaving a skipped key in the persisted record
	// for the next process/backend to use.
	later := stateValue(libsignalgo.GroupEncrypt(t.Context(), []byte("later"),
		alice.addr, distribution, alice.device.SenderKeyStore))(t)
	plain := stateValue(libsignalgo.GroupDecrypt(t.Context(), stateValue(later.Serialize())(t),
		alice.addr, bob.device.SenderKeyStore))(t)
	stateEqual(t, plain, []byte("later"))
}

func statePut(t *testing.T, data *store.Store, key string, value []byte) {
	t.Helper()
	stateCheck(t, data.SetMeta(t.Context(), key, base64.StdEncoding.EncodeToString(value)))
}

func stateGet(t *testing.T, data *store.Store, key string) []byte {
	t.Helper()

	value, ok, err := data.Meta(t.Context(), key)
	stateCheck(t, err)

	if !ok {
		t.Fatalf("missing persisted %s", key)
	}

	return stateValue(base64.StdEncoding.DecodeString(value))(t)
}

func stateEqual(t *testing.T, got, want []byte) {
	t.Helper()

	if !bytes.Equal(got, want) {
		t.Fatalf("got %x, want %x", got, want)
	}
}

func stateCheck(t *testing.T, err error) {
	t.Helper()

	if err != nil {
		t.Fatal(err)
	}
}

func stateValue[T any](value T, err error) func(*testing.T) T {
	return func(t *testing.T) T {
		t.Helper()
		stateCheck(t, err)

		return value
	}
}
