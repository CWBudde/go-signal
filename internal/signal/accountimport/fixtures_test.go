//go:build cgo || libsignal_go

package accountimport_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/internal/store"
	"github.com/cwbudde/mautrix-signal/pkg/libsignalgo"
	mstore "github.com/cwbudde/mautrix-signal/pkg/signalmeow/store"
	"github.com/google/uuid"
	"github.com/rs/zerolog"
)

const (
	namespaceACI         = "ACI"
	namespacePNI         = "PNI"
	scenarioGroupSkipped = "group-skipped"
)

type serviceID struct {
	Type string `json:"type"`
	UUID string `json:"uuid"`
}

func (s serviceID) value(t *testing.T) libsignalgo.ServiceID {
	t.Helper()

	id := must(uuid.Parse(s.UUID))(t)
	switch s.Type {
	case namespaceACI:
		return libsignalgo.NewACIServiceID(id)
	case namespacePNI:
		return libsignalgo.NewPNIServiceID(id)
	default:
		t.Fatal("unsupported service namespace")
		return libsignalgo.ServiceID{}
	}
}

func (s serviceID) address(t *testing.T) *libsignalgo.Address {
	t.Helper()
	return must(s.value(t).Address(2))(t)
}

type javaSnapshot struct {
	Identity                             []byte
	RegistrationID                       int
	PreKeys, SignedPreKeys, KyberPreKeys map[string][]byte
	LastResortIDs                        []uint32
	Sessions, Identities, SenderKeys     map[string][]byte
}
type ecRow struct {
	ID                    uint32
	PublicKey, PrivateKey []byte
}
type signedRow struct {
	ID                               uint32
	PublicKey, PrivateKey, Signature []byte
	TimestampMilliseconds            int64
}
type kyberRow struct {
	ID                    uint32
	Serialized            []byte
	LastResort            bool
	TimestampMilliseconds int64
}
type sourceRows struct {
	IdentityPublic, IdentityPrivate []byte
	PreKeys                         []ecRow
	SignedPreKeys                   []signedRow
	KyberPreKeys                    []kyberRow
}
type javaAccount struct {
	ID, ACI, PNI, Namespace string
	DeviceID                int
	Stores                  map[string]javaSnapshot
	SourceRows              map[string]sourceRows
}
type messageStep struct {
	MessageType                   string
	Ciphertext, ExpectedPlaintext []byte
}
type javaScenario struct {
	ID, Kind, Namespace, SourceAccountID, PeerAccountID string
	LocalServiceID, RemoteServiceID                     serviceID
	Queued                                              []messageStep
	ConsumedECID, UsedKyberID                           *uint32
	RetainKyber                                         bool
	DistributionID                                      *string
	DistributionMessage                                 []byte
}
type javaCorpus struct {
	Version          int
	Accounts         []javaAccount
	Scenarios        []javaScenario
	ExpectedMetadata map[string]libsignalgo.SessionRecordInspection
}

func must[T any](value T, err error) func(*testing.T) T {
	return func(t *testing.T) T { t.Helper(); check(t, err); return value }
}

func check(t *testing.T, err error) {
	t.Helper()

	if err != nil {
		t.Fatal(err)
	}
}

func equal(t *testing.T, got, want []byte) {
	t.Helper()

	if !bytes.Equal(got, want) {
		t.Fatal("byte values differ")
	}
}

func readJSON(t *testing.T, path string, value any) {
	t.Helper()

	raw := must(os.ReadFile(path))(t)
	if len(raw) > 32<<20 {
		t.Fatal("fixture file too large")
	}

	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	check(t, dec.Decode(value))

	err := dec.Decode(new(any))
	if err != io.EOF {
		t.Fatal("trailing JSON")
	}
}

func writeJSON(t *testing.T, path string, value any) {
	t.Helper()
	raw := must(json.MarshalIndent(value, "", "  "))(t)
	check(t, os.WriteFile(path, raw, 0o600))
}

func loadJavaCorpus(t *testing.T) javaCorpus {
	t.Helper()

	var corpus javaCorpus
	readJSON(t, "testdata/java-0.103.0/corpus.json", &corpus)

	if corpus.Version != 1 || len(corpus.Scenarios) != 12 || len(corpus.Accounts) != 24 {
		t.Fatal("invalid/incomplete corpus")
	}

	seen := map[string]bool{}
	for _, account := range corpus.Accounts {
		validateJavaAccount(t, account, seen)
		seen[account.ID] = true
	}

	scenarios := map[string]bool{}
	for _, scenario := range corpus.Scenarios {
		validateJavaScenario(t, scenario, seen, scenarios)
		scenarios[scenario.ID] = true
	}

	return corpus
}

func validateJavaAccount(t *testing.T, account javaAccount, seen map[string]bool) {
	t.Helper()

	if seen[account.ID] || account.DeviceID != 2 || len(account.Stores) != 2 || len(account.SourceRows) != 2 {
		t.Fatal("invalid/duplicate account")
	}

	for _, snapshot := range account.Stores {
		validateSnapshotBounds(t, snapshot)
	}
}

func validateSnapshotBounds(t *testing.T, snapshot javaSnapshot) {
	t.Helper()

	maps := []map[string][]byte{
		snapshot.PreKeys, snapshot.SignedPreKeys, snapshot.KyberPreKeys,
		snapshot.Sessions, snapshot.Identities, snapshot.SenderKeys,
	}
	for _, records := range maps {
		for _, raw := range records {
			if len(raw) > 1<<20 {
				t.Fatal("protocol record exceeds bound")
			}
		}
	}
}

func validateJavaScenario(t *testing.T, scenario javaScenario, accounts, seen map[string]bool) {
	t.Helper()

	if seen[scenario.ID] || !accounts[scenario.SourceAccountID] || !accounts[scenario.PeerAccountID] {
		t.Fatal("invalid/duplicate scenario")
	}

	if len(scenario.Queued) != 1 || scenario.Namespace != scenario.LocalServiceID.Type ||
		scenario.Namespace != scenario.RemoteServiceID.Type {
		t.Fatal("invalid/duplicate scenario")
	}

	_ = scenario.LocalServiceID.value(t)
	_ = scenario.RemoteServiceID.value(t)
}

func (c javaCorpus) account(t *testing.T, id string) javaAccount {
	t.Helper()

	for _, a := range c.Accounts {
		if a.ID == id {
			return a
		}
	}

	t.Fatal("missing corpus account")

	return javaAccount{}
}

func (c javaCorpus) scenario(t *testing.T, id string) javaScenario {
	t.Helper()

	for _, s := range c.Scenarios {
		if s.ID == id {
			return s
		}
	}

	t.Fatal("missing corpus scenario")

	return javaScenario{}
}

type protocolAccount struct {
	data    *store.Store
	device  *mstore.Device
	fixture javaAccount
}

func (a protocolAccount) close(t *testing.T) { t.Helper(); check(t, a.data.Close()) }
func (a protocolAccount) scopes(kind string) (mstore.SessionStore, libsignalgo.IdentityKeyStore, mstore.PreKeyStore) {
	if kind == namespaceACI {
		return a.device.ACISessionStore, a.device.ACIIdentityStore, a.device.ACIPreKeyStore
	}

	return a.device.PNISessionStore, a.device.PNIIdentityStore, a.device.PNIPreKeyStore
}

func openJavaAccount(t *testing.T, path, id string) protocolAccount {
	t.Helper()
	a := loadJavaCorpus(t).account(t, id)
	dir := must(store.OpenDir(filepath.Join(path, id), slog.New(slog.DiscardHandler)))(t)
	data := must(dir.OpenAccount(t.Context(), a.ACI, zerolog.Nop()))(t)

	device := must(data.Devices.DeviceByACI(t.Context(), uuid.MustParse(a.ACI)))(t)
	if device == nil {
		check(t, data.Close())
		t.Fatal("missing restored device")
	}

	return protocolAccount{data, device, a}
}

func keyPair(t *testing.T, public, private []byte) (*libsignalgo.PublicKey, *libsignalgo.PrivateKey) {
	t.Helper()
	pub := must(libsignalgo.DeserializePublicKey(public))(t)
	priv := must(libsignalgo.DeserializePrivateKey(private))(t)
	derived := must(priv.GetPublicKey())(t)
	equal(t, must(derived.Serialize())(t), public)

	return pub, priv
}

func identityPair(t *testing.T, r sourceRows) *libsignalgo.IdentityKeyPair {
	t.Helper()
	pub, priv := keyPair(t, r.IdentityPublic, r.IdentityPrivate)

	return must(libsignalgo.NewIdentityKeyPair(pub, priv))(t)
}

func restoreJavaAccount(t *testing.T, path, id string) {
	t.Helper()
	account := loadJavaCorpus(t).account(t, id)
	dir := must(store.OpenDir(filepath.Join(path, id), slog.New(slog.DiscardHandler)))(t)

	data := must(dir.OpenAccount(t.Context(), account.ACI, zerolog.Nop()))(t)
	defer func() { check(t, data.Close()) }()

	check(t, data.Devices.PutDevice(t.Context(), restoredDeviceData(t, account)))
	device := must(data.Devices.DeviceByACI(t.Context(), uuid.MustParse(account.ACI)))(t)

	restored := protocolAccount{data, device, account}
	for _, kind := range []string{namespaceACI, namespacePNI} {
		sessions, _, keys := restored.scopes(kind)
		snapshot := account.Stores[kind]
		rows := account.SourceRows[kind]
		restorePreKeys(t, keys, rows, snapshot)
		restoreProtocolRecords(t, restored, sessions, snapshot)
	}
}

func restoredDeviceData(t *testing.T, account javaAccount) *mstore.DeviceData {
	t.Helper()

	return &mstore.DeviceData{
		ACI: uuid.MustParse(account.ACI), PNI: uuid.MustParse(account.PNI), DeviceID: account.DeviceID,
		Number: "+12025550111", Password: "synthetic-offline-password-never-authenticate",
		ACIIdentityKeyPair: identityPair(t, account.SourceRows[namespaceACI]),
		PNIIdentityKeyPair: identityPair(t, account.SourceRows[namespacePNI]),
		ACIRegistrationID:  account.Stores[namespaceACI].RegistrationID,
		PNIRegistrationID:  account.Stores[namespacePNI].RegistrationID,
	}
}

func restorePreKeys(t *testing.T, keys mstore.PreKeyStore, rows sourceRows, snapshot javaSnapshot) {
	t.Helper()

	identity := must(libsignalgo.DeserializePublicKey(rows.IdentityPublic))(t)
	for _, row := range rows.PreKeys {
		pub, priv := keyPair(t, row.PublicKey, row.PrivateKey)
		record := must(libsignalgo.NewPreKeyRecord(row.ID, pub, priv))(t)
		equal(t, must(record.Serialize())(t), snapshot.PreKeys[strconv.FormatUint(uint64(row.ID), 10)])
		check(t, keys.StorePreKey(t.Context(), row.ID, record))
	}

	for _, row := range rows.SignedPreKeys {
		restoreSignedPreKey(t, keys, identity, row, snapshot)
	}

	for _, row := range rows.KyberPreKeys {
		restoreKyberPreKey(t, keys, identity, row)
	}
}

func restoreSignedPreKey(
	t *testing.T, keys mstore.PreKeyStore, identity *libsignalgo.PublicKey, row signedRow, snapshot javaSnapshot,
) {
	t.Helper()

	pub, priv := keyPair(t, row.PublicKey, row.PrivateKey)
	if !must(identity.Verify(row.PublicKey, row.Signature))(t) {
		t.Fatal("invalid signed prekey signature")
	}

	record := must(libsignalgo.NewSignedPreKeyRecord(
		row.ID, time.UnixMilli(row.TimestampMilliseconds), pub, priv, row.Signature,
	))(t)
	equal(t, must(record.Serialize())(t), snapshot.SignedPreKeys[strconv.FormatUint(uint64(row.ID), 10)])
	check(t, keys.StoreSignedPreKey(t.Context(), row.ID, record))
}

func restoreKyberPreKey(t *testing.T, keys mstore.PreKeyStore, identity *libsignalgo.PublicKey, row kyberRow) {
	t.Helper()

	record := must(libsignalgo.DeserializeKyberPreKeyRecord(row.Serialized))(t)
	if must(record.GetID())(t) != row.ID || must(record.GetTimestamp())(t).UnixMilli() != row.TimestampMilliseconds {
		t.Fatal("Kyber row metadata differs")
	}

	pub := must(record.GetPublicKey())(t)
	if !must(identity.Verify(must(pub.Serialize())(t), must(record.GetSignature())(t)))(t) {
		t.Fatal("invalid Kyber signature")
	}

	if row.LastResort {
		check(t, keys.StoreLastResortKyberPreKey(t.Context(), row.ID, record))
	} else {
		check(t, keys.StoreKyberPreKey(t.Context(), row.ID, record))
	}
}

func restoreProtocolRecords(
	t *testing.T, account protocolAccount, sessions mstore.SessionStore, snapshot javaSnapshot,
) {
	t.Helper()

	for address, raw := range snapshot.Sessions {
		_, err := libsignalgo.InspectSessionRecord(raw)
		check(t, err)
		record := must(libsignalgo.DeserializeSessionRecord(raw))(t)
		check(t, sessions.StoreSession(t.Context(), parseAddress(t, address), record))
	}

	for address, raw := range snapshot.Identities {
		service := must(libsignalgo.ServiceIDFromString(address))(t)
		identity := must(libsignalgo.DeserializeIdentityKey(raw))(t)
		_, err := account.device.IdentityKeyStore.SaveIdentityKey(t.Context(), service, identity)
		check(t, err)
	}

	for key, raw := range snapshot.SenderKeys {
		parts := strings.Split(key, "/")
		if len(parts) != 3 {
			t.Fatal("invalid sender key fixture")
		}

		address := parseAddress(t, strings.Join(parts[:2], "/"))
		record := must(libsignalgo.DeserializeSenderKeyRecord(raw))(t)
		check(t, account.device.SenderKeyStore.StoreSenderKey(t.Context(), address, uuid.MustParse(parts[2]), record))
	}
}

func parseAddress(t *testing.T, text string) *libsignalgo.Address {
	t.Helper()

	parts := strings.Split(text, "/")
	if len(parts) != 2 {
		t.Fatal("invalid fixture address")
	}

	sid := must(libsignalgo.ServiceIDFromString(parts[0]))(t)
	device := must(strconv.ParseUint(parts[1], 10, 32))(t)

	return must(sid.Address(uint(device)))(t)
}

func verifyRestored(t *testing.T, p protocolAccount, account javaAccount, corpus javaCorpus) {
	t.Helper()

	for _, kind := range []string{namespaceACI, namespacePNI} {
		sessions, identities, keys := p.scopes(kind)
		snapshot := account.Stores[kind]
		equal(t, must(must(identities.GetIdentityKeyPair(t.Context()))(t).Serialize())(t), snapshot.Identity)

		if int(must(identities.GetLocalRegistrationID(t.Context()))(t)) != snapshot.RegistrationID {
			t.Fatal("registration scope differs")
		}

		for address, raw := range snapshot.Sessions {
			want := corpus.ExpectedMetadata[account.ID+"/"+kind+"/"+address]
			verifyRestoredSession(t, sessions, address, raw, want)
		}

		verifyRestoredPreKeys(t, keys, account.SourceRows[kind])
	}
}

func verifyRestoredSession(
	t *testing.T, sessions mstore.SessionStore, address string, raw []byte, want libsignalgo.SessionRecordInspection,
) {
	t.Helper()
	record := must(sessions.LoadSession(t.Context(), parseAddress(t, address)))(t)

	stored := must(record.Serialize())(t)
	if sha256.Sum256(raw) != sha256.Sum256(stored) {
		t.Fatal("reopening changed source record")
	}

	got := must(libsignalgo.InspectSessionRecord(stored))(t)

	if len(want.Archived) == 0 {
		want.Archived = nil
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("session metadata differs: got %+v; expected %+v", got, want)
	}
}

func verifyRestoredPreKeys(t *testing.T, keys mstore.PreKeyStore, rows sourceRows) {
	t.Helper()

	for _, row := range rows.SignedPreKeys {
		record := must(keys.LoadSignedPreKey(t.Context(), row.ID))(t)
		if must(record.GetTimestamp())(t).UnixMilli() != row.TimestampMilliseconds {
			t.Fatal("signed timestamp unit changed")
		}
	}

	for _, row := range rows.KyberPreKeys {
		record := must(keys.LoadKyberPreKey(t.Context(), row.ID))(t)
		equal(t, must(record.Serialize())(t), row.Serialized)

		if must(keys.IsKyberPreKeyLastResort(t.Context(), row.ID))(t) != row.LastResort {
			t.Fatal("last-resort flag changed")
		}
	}
}

func fixtureHash(raw []byte) string { hash := sha256.Sum256(raw); return hex.EncodeToString(hash[:]) }

func javaSeed(s javaScenario, p javaAccount) map[string]any {
	stores := map[string]any{}
	for kind, snapshot := range p.Stores {
		stores[kind] = map[string]any{
			"identity": snapshot.Identity, "registrationID": snapshot.RegistrationID,
			"preKeys": snapshot.PreKeys, "signedPreKeys": snapshot.SignedPreKeys, "kyberPreKeys": snapshot.KyberPreKeys,
			"lastResortIDs": snapshot.LastResortIDs, "sessions": snapshot.Sessions,
			"identities": snapshot.Identities, "senderKeys": snapshot.SenderKeys,
		}
	}

	return map[string]any{
		"scenario": map[string]any{
			"id": s.ID, "kind": s.Kind, "namespace": s.Namespace,
			"sourceAccountID": s.SourceAccountID, "peerAccountID": s.PeerAccountID,
			"localServiceID": s.LocalServiceID, "remoteServiceID": s.RemoteServiceID, "queued": []any{},
			"consumedECID": s.ConsumedECID, "usedKyberID": s.UsedKyberID, "retainKyber": s.RetainKyber,
			"distributionID": s.DistributionID, "distributionMessage": s.DistributionMessage,
		},
		"peer": map[string]any{
			"id": p.ID, "aci": p.ACI, "pni": p.PNI, "deviceID": p.DeviceID, "namespace": p.Namespace,
			"stores": stores, "sourceRows": map[string]any{},
		},
	}
}
