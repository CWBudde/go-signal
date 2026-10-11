//go:build cgo || libsignal_go

package accountimport_test

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"

	"github.com/cwbudde/mautrix-signal/pkg/libsignalgo"
	mstore "github.com/cwbudde/mautrix-signal/pkg/signalmeow/store"
	"github.com/google/uuid"
)

type exchangeState struct {
	Pending                        messageStep
	Replay                         *messageStep
	Iteration, JavaRound, JavaStep int
	Distribution                   string
	AwaitingAck                    bool
}

//nolint:tagliatelle // These ID spellings are required by the approved Java exchange JSON API.
type javaRequest struct {
	Version           int       `json:"version"`
	ScenarioID        string    `json:"scenarioID"`
	Step              int       `json:"step"`
	SenderServiceID   serviceID `json:"senderServiceID"`
	ReceiverServiceID serviceID `json:"receiverServiceID"`
	MessageType       string    `json:"messageType"`
	Ciphertext        []byte    `json:"ciphertext"`
	ExpectedPlaintext []byte    `json:"expectedPlaintext"`
}
type javaResponse struct {
	Version                            int
	ScenarioID                         string
	Step                               int
	SenderServiceID, ReceiverServiceID serviceID
	MessageType                        string
	Ciphertext, ExpectedPlaintext      []byte
	PeerStateSHA256                    string
}

func compatibilityStep(t *testing.T, dir, id, action string) {
	t.Helper()
	corpus := loadJavaCorpus(t)
	scenario := corpus.scenario(t, id)

	statePath := filepath.Join(dir, "exchange.json")
	if action == "init" {
		initializeExchange(t, dir, scenario, corpus)
		return
	}

	var state exchangeState
	readJSON(t, statePath, &state)

	source := openJavaAccount(t, dir, scenario.SourceAccountID)
	defer source.close(t)

	peer := openJavaAccount(t, dir, scenario.PeerAccountID)
	defer peer.close(t)

	switch action {
	case "advance":
		advanceExchange(t, source, peer, scenario, &state)
	case "export-java":
		exportJavaExchange(t, dir, source, scenario, corpus, &state)
	case "consume-java":
		consumeJavaExchange(t, dir, source, scenario, &state)
	default:
		t.Fatal("unknown compatibility action")
	}

	writeJSON(t, statePath, state)
}

func initializeExchange(t *testing.T, dir string, scenario javaScenario, corpus javaCorpus) {
	t.Helper()
	check(t, os.MkdirAll(dir, 0o700))
	statePath := filepath.Join(dir, "exchange.json")

	_, err := os.Stat(statePath)
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatal("compatibility directory already initialized")
	}

	restoreJavaAccount(t, dir, scenario.SourceAccountID)
	restoreJavaAccount(t, dir, scenario.PeerAccountID)
	writeJSON(t, statePath, exchangeState{Pending: scenario.Queued[0]})
	check(t, os.Mkdir(filepath.Join(dir, "java"), 0o700))
	writeJSON(t, filepath.Join(dir, "java", "seed.json"), javaSeed(scenario, corpus.account(t, scenario.PeerAccountID)))
}

func consumePending(t *testing.T, source protocolAccount, scenario javaScenario, state *exchangeState) {
	t.Helper()
	rejectTamper(t, source, scenario, state.Pending)
	plain := must(decryptStep(t, source, scenario.LocalServiceID, scenario.RemoteServiceID, state.Pending))(t)
	equal(t, plain, state.Pending.ExpectedPlaintext)
	consumed := state.Pending
	state.Replay = &consumed
}

func advanceExchange(t *testing.T, source, peer protocolAccount, scenario javaScenario, state *exchangeState) {
	t.Helper()

	if state.Replay != nil {
		rejectReplay(t, source, scenario, *state.Replay)
	}

	consumePending(t, source, scenario, state)

	if scenario.ConsumedECID != nil {
		checkConsumption(t, source, scenario)
	}

	plain := []byte(scenario.ID + "/go/" + strconv.Itoa(state.Iteration))

	peerPlain := []byte(scenario.ID + "/peer/" + strconv.Itoa(state.Iteration))
	if scenario.Kind == scenarioGroupSkipped {
		advanceGroupExchange(t, source, peer, scenario, state, plain, peerPlain)
	} else {
		outgoing := directEncrypt(t, source, scenario.LocalServiceID, scenario.RemoteServiceID, plain)
		equal(t, must(decryptStep(t, peer, scenario.RemoteServiceID, scenario.LocalServiceID, outgoing))(t), plain)
		state.Pending = directEncrypt(t, peer, scenario.RemoteServiceID, scenario.LocalServiceID, peerPlain)
	}

	state.Iteration++
}

func newGoDistribution(
	t *testing.T, source protocolAccount, local serviceID, state *exchangeState,
) *libsignalgo.SenderKeyDistributionMessage {
	t.Helper()

	state.Distribution = uuid.NewString()

	return must(libsignalgo.NewSenderKeyDistributionMessage(
		t.Context(), local.address(t), uuid.MustParse(state.Distribution), source.device.SenderKeyStore,
	))(t)
}

func advanceGroupExchange(
	t *testing.T, source, peer protocolAccount, scenario javaScenario, state *exchangeState, plain, peerPlain []byte,
) {
	t.Helper()

	if state.Distribution == "" {
		message := newGoDistribution(t, source, scenario.LocalServiceID, state)
		check(t, libsignalgo.ProcessSenderKeyDistributionMessage(
			t.Context(), message, scenario.LocalServiceID.address(t), peer.device.SenderKeyStore,
		))
	}

	outgoing := groupEncrypt(t, source, scenario.LocalServiceID, state.Distribution, plain)
	equal(t, must(libsignalgo.GroupDecrypt(
		t.Context(), outgoing.Ciphertext, scenario.LocalServiceID.address(t), peer.device.SenderKeyStore,
	))(t), plain)
	state.Pending = groupEncrypt(t, peer, scenario.RemoteServiceID, *scenario.DistributionID, peerPlain)
}

func exportJavaExchange(
	t *testing.T, dir string, source protocolAccount, scenario javaScenario, corpus javaCorpus, state *exchangeState,
) {
	t.Helper()
	outgoing := outgoingJavaMessage(t, source, scenario, corpus, state)
	request := javaRequest{
		Version: 1, ScenarioID: scenario.ID, Step: state.JavaStep,
		SenderServiceID: scenario.LocalServiceID, ReceiverServiceID: scenario.RemoteServiceID,
		MessageType: outgoing.MessageType, Ciphertext: outgoing.Ciphertext, ExpectedPlaintext: outgoing.ExpectedPlaintext,
	}
	writeJSON(t, filepath.Join(dir, "java", "request.json"), request)
}

func outgoingJavaMessage(
	t *testing.T, source protocolAccount, scenario javaScenario, corpus javaCorpus, state *exchangeState,
) messageStep {
	t.Helper()

	if scenario.Kind == scenarioGroupSkipped && state.JavaRound == 0 {
		// Incoming signing state never authorizes an outgoing chain.
		message := newGoDistribution(t, source, scenario.LocalServiceID, state)
		state.AwaitingAck = true

		return messageStep{"sender-key-distribution", must(message.Serialize())(t), []byte{}}
	}
	// Authenticate a reply before encrypting from an old pending source session.
	// Stored seconds are preserved, never rewritten.
	if state.Replay != nil {
		rejectReplay(t, source, scenario, *state.Replay)
	}

	if state.JavaRound == 0 || scenario.Kind == scenarioGroupSkipped {
		consumePending(t, source, scenario, state)
	}

	plain := []byte(scenario.ID + "/go-java/" + strconv.Itoa(state.JavaRound))
	if scenario.Kind == scenarioGroupSkipped {
		return groupEncrypt(t, source, scenario.LocalServiceID, state.Distribution, plain)
	}

	if state.JavaRound > 0 {
		freshTowardJava(t, source, scenario, corpus.account(t, scenario.PeerAccountID))
	}

	return directEncrypt(t, source, scenario.LocalServiceID, scenario.RemoteServiceID, plain)
}

func consumeJavaExchange(
	t *testing.T, dir string, source protocolAccount, scenario javaScenario, state *exchangeState,
) {
	t.Helper()

	var response javaResponse
	readJSON(t, filepath.Join(dir, "java", "response.json"), &response)
	verifyJavaResponse(t, dir, response, scenario, state.JavaStep)

	if state.AwaitingAck {
		if response.MessageType != "ack" || len(response.Ciphertext) != 0 || len(response.ExpectedPlaintext) != 0 {
			t.Fatal("missing Java distribution acknowledgement")
		}

		state.AwaitingAck = false
	} else {
		incoming := messageStep{response.MessageType, response.Ciphertext, response.ExpectedPlaintext}
		rejectTamper(t, source, scenario, incoming)
		plain := must(decryptStep(t, source, scenario.LocalServiceID, scenario.RemoteServiceID, incoming))(t)
		equal(t, plain, []byte(scenario.ID+"/java/"+strconv.Itoa(state.JavaStep)))
		state.Replay = &incoming
	}

	state.JavaRound++
	state.JavaStep++
}

func verifyJavaResponse(t *testing.T, dir string, response javaResponse, scenario javaScenario, step int) {
	t.Helper()

	if response.Version != 1 || response.ScenarioID != scenario.ID || response.Step != step ||
		response.SenderServiceID != scenario.RemoteServiceID || response.ReceiverServiceID != scenario.LocalServiceID {
		t.Fatal("invalid Java response identity/step")
	}

	if response.PeerStateSHA256 != fixtureHash(must(os.ReadFile(filepath.Join(dir, "java", "peer-state.json")))(t)) {
		t.Fatal("Java state hash differs")
	}
}

func decryptStep(t *testing.T, p protocolAccount, local, remote serviceID, msg messageStep) ([]byte, error) {
	t.Helper()

	sessions, identity, keys := p.scopes(local.Type)

	var plain []byte

	var err error

	switch msg.MessageType {
	case "prekey":
		message, decodeErr := libsignalgo.DeserializePreKeyMessage(msg.Ciphertext)
		if decodeErr != nil {
			return nil, fmt.Errorf("decode prekey message: %w", decodeErr)
		}

		plain, err = libsignalgo.DecryptPreKey(
			t.Context(), message, remote.address(t), local.address(t), sessions, identity, keys, keys, keys,
		)
	case "signal":
		message, decodeErr := libsignalgo.DeserializeMessage(msg.Ciphertext)
		if decodeErr != nil {
			return nil, fmt.Errorf("decode signal message: %w", decodeErr)
		}

		plain, err = libsignalgo.Decrypt(t.Context(), message, remote.address(t), local.address(t), sessions, identity)
	case "sender-key":
		plain, err = libsignalgo.GroupDecrypt(t.Context(), msg.Ciphertext, remote.address(t), p.device.SenderKeyStore)
	default:
		t.Fatal("invalid message type")
	}

	if err != nil {
		return nil, fmt.Errorf("decrypt %s message: %w", msg.MessageType, err)
	}

	return plain, nil
}

func directEncrypt(t *testing.T, p protocolAccount, local, remote serviceID, plain []byte) messageStep {
	t.Helper()

	sessions, identity, _ := p.scopes(local.Type)
	msg := must(libsignalgo.Encrypt(t.Context(), plain, remote.address(t), local.address(t), sessions, identity))(t)

	kind := "signal"
	if must(msg.MessageType())(t) == libsignalgo.CiphertextMessageTypePreKey {
		kind = "prekey"
	}

	return messageStep{kind, must(msg.Serialize())(t), plain}
}

func groupEncrypt(t *testing.T, p protocolAccount, local serviceID, dist string, plain []byte) messageStep {
	t.Helper()
	msg := must(libsignalgo.GroupEncrypt(
		t.Context(), plain, local.address(t), uuid.MustParse(dist), p.device.SenderKeyStore,
	))(t)

	return messageStep{"sender-key", must(msg.Serialize())(t), plain}
}

func checkConsumption(t *testing.T, p protocolAccount, s javaScenario) {
	t.Helper()

	_, _, keys := p.scopes(s.Namespace)
	if must(keys.LoadPreKey(t.Context(), *s.ConsumedECID))(t) != nil {
		t.Fatal("EC one-time key not consumed")
	}

	kyber := must(keys.LoadKyberPreKey(t.Context(), *s.UsedKyberID))(t)
	if (kyber != nil) != s.RetainKyber {
		t.Fatal("Kyber consumption differs")
	}

	if must(keys.LoadSignedPreKey(t.Context(), 2))(t) == nil {
		t.Fatal("signed key consumed")
	}
}

func protocolSnapshot(t *testing.T, p protocolAccount, s javaScenario) map[string]string {
	t.Helper()

	sessions, _, keys := p.scopes(s.Namespace)

	values := snapshotStoredKeys(t, keys, p.fixture.SourceRows[s.Namespace])
	if record := must(sessions.LoadSession(t.Context(), s.RemoteServiceID.address(t)))(t); record != nil {
		values["session"] = fixtureHash(must(record.Serialize())(t))
	}

	identity := must(p.device.IdentityKeyStore.GetIdentityKey(t.Context(), s.RemoteServiceID.value(t)))(t)
	if identity != nil {
		values["identity"] = fixtureHash(must(identity.Serialize())(t))
	}

	if s.DistributionID != nil {
		record := must(p.device.SenderKeyStore.LoadSenderKey(
			t.Context(), s.RemoteServiceID.address(t), uuid.MustParse(*s.DistributionID),
		))(t)
		if record != nil {
			values["sender-key"] = fixtureHash(must(record.Serialize())(t))
		}
	}

	return values
}

func snapshotStoredKeys(t *testing.T, keys mstore.PreKeyStore, rows sourceRows) map[string]string {
	t.Helper()

	values := map[string]string{}

	for _, row := range rows.PreKeys {
		if record := must(keys.LoadPreKey(t.Context(), row.ID))(t); record != nil {
			values["ec"+strconv.FormatUint(uint64(row.ID), 10)] = fixtureHash(must(record.Serialize())(t))
		}
	}

	for _, row := range rows.SignedPreKeys {
		if record := must(keys.LoadSignedPreKey(t.Context(), row.ID))(t); record != nil {
			values["signed"+strconv.FormatUint(uint64(row.ID), 10)] = fixtureHash(must(record.Serialize())(t))
		}
	}

	for _, row := range rows.KyberPreKeys {
		if record := must(keys.LoadKyberPreKey(t.Context(), row.ID))(t); record != nil {
			values["kyber"+strconv.FormatUint(uint64(row.ID), 10)] = fixtureHash(must(record.Serialize())(t))
		}
	}

	return values
}

func rejectTamper(t *testing.T, p protocolAccount, s javaScenario, msg messageStep) {
	t.Helper()
	before := protocolSnapshot(t, p, s)
	bad := msg

	bad.Ciphertext = bytes.Clone(msg.Ciphertext)
	if len(bad.Ciphertext) == 0 {
		t.Fatal("empty ciphertext")
	}

	bad.Ciphertext[len(bad.Ciphertext)-1] ^= 1

	_, err := decryptStep(t, p, s.LocalServiceID, s.RemoteServiceID, bad)
	if err == nil {
		t.Fatal("tampered message accepted")
	}

	if !reflect.DeepEqual(before, protocolSnapshot(t, p, s)) {
		t.Fatal("tamper changed stored protocol state")
	}
}

func rejectReplay(t *testing.T, p protocolAccount, s javaScenario, msg messageStep) {
	t.Helper()
	before := protocolSnapshot(t, p, s)

	_, err := decryptStep(t, p, s.LocalServiceID, s.RemoteServiceID, msg)
	if !errors.Is(err, libsignalgo.ErrorCodeDuplicatedMessage) {
		t.Fatalf("replay: %v", err)
	}

	if !reflect.DeepEqual(before, protocolSnapshot(t, p, s)) {
		t.Fatal("replay changed stored protocol state")
	}
}

func freshTowardJava(t *testing.T, p protocolAccount, s javaScenario, peer javaAccount) {
	t.Helper()

	sessions, identity, _ := p.scopes(s.Namespace)
	record := must(sessions.LoadSession(t.Context(), s.RemoteServiceID.address(t)))(t)
	check(t, record.ArchiveCurrentState())
	check(t, sessions.StoreSession(t.Context(), s.RemoteServiceID.address(t), record))
	snapshot := peer.Stores[s.Namespace]
	ec := must(libsignalgo.DeserializePreKeyRecord(snapshot.PreKeys["5"]))(t)
	signed := must(libsignalgo.DeserializeSignedPreKeyRecord(snapshot.SignedPreKeys["2"]))(t)
	kyber := must(libsignalgo.DeserializeKyberPreKeyRecord(snapshot.KyberPreKeys["4"]))(t)
	bundle := must(libsignalgo.NewPreKeyBundle(
		uint32(snapshot.RegistrationID), 2, 5, must(ec.GetPublicKey())(t),
		2, must(signed.GetPublicKey())(t), must(signed.GetSignature())(t),
		4, must(kyber.GetPublicKey())(t), must(kyber.GetSignature())(t),
		must(libsignalgo.DeserializeIdentityKey(peer.SourceRows[s.Namespace].IdentityPublic))(t),
	))(t)
	check(t, libsignalgo.ProcessPreKeyBundle(
		t.Context(), bundle, s.RemoteServiceID.address(t), s.LocalServiceID.address(t), sessions, identity,
	))
}
