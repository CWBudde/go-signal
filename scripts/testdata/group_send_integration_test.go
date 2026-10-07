// Injected into signalmeow by test-zkgroup-integration.sh; no production hooks.
package signalmeow

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	storagepb "github.com/cwbudde/libsignal-go/proto"
	"github.com/cwbudde/mautrix-signal/pkg/libsignalgo"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow/protobuf/signalpb"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow/store"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow/types"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow/web"
	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"google.golang.org/protobuf/proto"
)

type groupSendStore struct {
	store.SessionStore
	store.SenderKeyStore
	store.RecipientStore
	libsignalgo.IdentityKeyStore
	identity   *libsignalgo.IdentityKeyPair
	identities map[libsignalgo.ServiceID]*libsignalgo.IdentityKey
	sessions   map[libsignalgo.ServiceID][]store.SessionAddressTuple
	senderKey  *libsignalgo.SenderKeyRecord
	info       *store.SenderKeyInfo
}

func (s *groupSendStore) GetIdentityKeyPair(context.Context) (*libsignalgo.IdentityKeyPair, error) {
	return s.identity, nil
}

func (s *groupSendStore) GetIdentityKey(_ context.Context, id libsignalgo.ServiceID) (*libsignalgo.IdentityKey, error) {
	b, err := s.identities[id].Serialize()
	if err != nil {
		return nil, err
	}
	// The CGO callback transfers ownership, just as the SQL store returns a fresh key.
	return libsignalgo.DeserializeIdentityKey(b)
}

func (s *groupSendStore) IsTrustedIdentity(_ context.Context, id libsignalgo.ServiceID, key *libsignalgo.IdentityKey, direction libsignalgo.SignalDirection) (bool, error) {
	if direction == libsignalgo.SignalDirectionReceiving {
		return true, nil
	}
	known := s.identities[id]
	if known == nil {
		return false, nil
	}
	return known.Equal(key)
}

func (s *groupSendStore) AllSessionsForServiceID(_ context.Context, id libsignalgo.ServiceID) ([]store.SessionAddressTuple, error) {
	return s.sessions[id], nil
}

func (s *groupSendStore) LoadProfileKey(context.Context, uuid.UUID) (*libsignalgo.ProfileKey, error) {
	return &libsignalgo.ProfileKey{1}, nil
}

func (s *groupSendStore) LoadSenderKey(context.Context, *libsignalgo.Address, uuid.UUID) (*libsignalgo.SenderKeyRecord, error) {
	return s.senderKey, nil
}

func (s *groupSendStore) StoreSenderKey(_ context.Context, _ *libsignalgo.Address, _ uuid.UUID, key *libsignalgo.SenderKeyRecord) error {
	s.senderKey = key
	return nil
}

func (s *groupSendStore) GetSenderKeyInfo(context.Context, types.GroupIdentifier) (*store.SenderKeyInfo, error) {
	return s.info, nil
}

func TestZKGroupIntegrationEndorsementSend(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	f := groupSendLoad(t)
	master := libsignalgo.GroupMasterKey(groupSendBytes(t, f.Params.MasterKey))
	group, e := master.SecretParams()
	integrationCheck(t, e)
	gid, e := master.GroupIdentifier()
	integrationCheck(t, e)
	ids := []libsignalgo.ServiceID{libsignalgo.NewACIServiceID(uuid.New()), libsignalgo.NewACIServiceID(uuid.New()), libsignalgo.NewACIServiceID(uuid.New())}
	response, key, expiry := groupSendFresh(t, ids, &group, [32]byte(groupSendBytes(t, f.Params.Seed)))
	spp, e := libsignalgo.DeserializeServerPublicParams(groupSendBytes(t, f.Result.Server))
	integrationCheck(t, e)
	originalParams := prodServerPublicParams
	prodServerPublicParams = spp
	defer func() { prodServerPublicParams = originalParams }()
	cache := NewGroupCache(ids[0])
	data := &Group{GroupIdentifier: types.GroupIdentifier(gid.String()), GroupMasterKey: types.SerializedGroupMasterKey(base64.StdEncoding.EncodeToString(master[:])), Revision: 1}
	for _, id := range ids {
		data.Members = append(data.Members, &GroupMember{ACI: id.UUID})
	}
	integrationCheck(t, cache.Put(data, response))
	_, sec, ok := cache.Get(data.GroupIdentifier)
	if !ok {
		t.Fatal("verified endorsements not cached")
	}
	cachedToken, e := sec.GetToken()
	integrationCheck(t, e)
	groupSendVerify(t, key, cachedToken, ids[1:])
	if !sec.Expiration.Equal(expiry) {
		t.Fatal("cached expiry changed")
	}
	sender := &groupSendStore{identities: make(map[libsignalgo.ServiceID]*libsignalgo.IdentityKey), sessions: make(map[libsignalgo.ServiceID][]store.SessionAddressTuple), info: &store.SenderKeyInfo{DistributionID: uuid.New(), CreatedAt: time.Now(), SharedWith: make(map[libsignalgo.ServiceID][]int)}}
	sender.identity, e = libsignalgo.GenerateIdentityKeyPair()
	integrationCheck(t, e)
	senderAddr, e := ids[0].Address(1)
	integrationCheck(t, e)
	// Establish the sender key locally and mark it already distributed. Session
	// metadata is sufficient for the sealed-sender envelope (no pairwise ratchet
	// encryption occurs on this path); the actual sender-key encryption is real.
	skdm, e := libsignalgo.NewSenderKeyDistributionMessage(ctx, senderAddr, sender.info.DistributionID, sender)
	integrationCheck(t, e)
	receivers := make(map[libsignalgo.ServiceID]*groupSendStore)
	for _, id := range ids[1:] {
		receiver := &groupSendStore{}
		receiver.identity, e = libsignalgo.GenerateIdentityKeyPair()
		integrationCheck(t, e)
		sender.identities[id], e = libsignalgo.NewIdentityKeyFromPublicKey(receiver.identity.GetPublicKey())
		integrationCheck(t, e)
		addr, e := id.Address(1)
		integrationCheck(t, e)
		raw, e := proto.Marshal(&storagepb.RecordStructure{CurrentSession: &storagepb.SessionStructure{SessionVersion: 3, RemoteRegistrationId: 123}})
		integrationCheck(t, e)
		record, e := libsignalgo.DeserializeSessionRecord(raw)
		integrationCheck(t, e)
		sender.sessions[id] = []store.SessionAddressTuple{{ServiceID: id, DeviceID: 1, Address: addr, Record: record}}
		sender.info.SharedWith[id] = []int{1}
		integrationCheck(t, libsignalgo.ProcessSenderKeyDistributionMessage(ctx, skdm, senderAddr, receiver))
		receivers[id] = receiver
	}
	sender.sessions[ids[0]] = []store.SessionAddressTuple{{ServiceID: ids[0], DeviceID: 1, Address: senderAddr}}
	signing, e := libsignalgo.GeneratePrivateKey()
	integrationCheck(t, e)
	signingPub, e := signing.GetPublicKey()
	integrationCheck(t, e)
	serverCert, e := libsignalgo.NewServerCertificate(1, signingPub, signing)
	integrationCheck(t, e)
	cert, e := libsignalgo.NewSenderCertificate(libsignalgo.NewSealedSenderAddress("", ids[0].UUID, 1), sender.identity.GetPublicKey(), time.Now().Add(72*time.Hour), serverCert, signing)
	integrationCheck(t, e)
	cli := NewClient(&store.Device{DeviceData: store.DeviceData{ACI: ids[0].UUID, DeviceID: 1}, ACISessionStore: sender, ACIIdentityStore: sender, SenderKeyStore: sender, RecipientStore: sender}, zerolog.Nop(), nil)
	cli.senderCertificateNoE164 = cert
	checked := make(chan *signalpb.WebSocketRequestMessage, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.CloseNow()
		for {
			_, payload, err := conn.Read(ctx)
			if err != nil {
				return
			}
			var message signalpb.WebSocketMessage
			if err = proto.Unmarshal(payload, &message); err != nil {
				t.Error(err)
				return
			}
			req := message.GetRequest()
			checked <- req
			body := []byte(`{"uuids404":[]}`)
			reply := &signalpb.WebSocketMessage{Type: signalpb.WebSocketMessage_RESPONSE.Enum(), Response: &signalpb.WebSocketResponseMessage{Id: req.Id, Status: proto.Uint32(200), Body: body}}
			payload, err = proto.Marshal(reply)
			if err != nil {
				t.Error(err)
				return
			}
			if err = conn.Write(ctx, websocket.MessageBinary, payload); err != nil {
				return
			}
		}
	}))
	defer server.Close()
	target, e := url.Parse(server.URL)
	integrationCheck(t, e)
	originalTransport := web.SignalHTTPClient.Transport
	web.SignalHTTPClient.Transport = integrationTransport{target, http.DefaultTransport}
	defer func() { web.SignalHTTPClient.Transport = originalTransport }()
	ws := web.NewSignalWebsocket(nil)
	status := ws.Connect(ctx, nil)
	defer func() { cancel(); _ = ws.Close() }()
	select {
	case s := <-status:
		if s.Event != web.SignalWebsocketConnectionEventConnected {
			t.Fatalf("connect: %+v", s)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	cli.UnauthedWS = ws
	cli.AuthedWS = ws
	content := &signalpb.Content{Content: &signalpb.Content_DataMessage{DataMessage: &signalpb.DataMessage{Body: proto.String("Offline endorsed group send")}}}
	result, e := cli.sendToGroupWithSenderKey(ctx, gid, ids, *sec, content, 123456789, 0)
	integrationCheck(t, e)
	if len(result.SuccessfullySentTo) != 2 || len(result.FailedToSendTo) != 0 {
		t.Fatalf("group result: %+v", result)
	}
	for _, sent := range result.SuccessfullySentTo {
		if !sent.Unidentified {
			t.Fatal("sent without sealed sender")
		}
	}
	// A group request and an empty sync to the single local device are expected.
	// Any per-recipient fallback request fails this assertion.
	multi := 0
	for len(checked) > 0 {
		req := <-checked
		if strings.HasPrefix(req.GetPath(), "/v1/messages/multi_recipient?") {
			multi++
			if req.GetVerb() != http.MethodPut {
				t.Fatal("wrong verb")
			}
			headers := http.Header{}
			for _, h := range req.Headers {
				k, v, ok := strings.Cut(h, ":")
				if ok {
					headers.Add(k, strings.TrimSpace(v))
				}
			}
			if headers.Get("Unidentified-Access-Key") != "" || headers.Get("Content-Type") != string(web.ContentTypeMultiRecipientMessage) {
				t.Fatal("wrong group auth/content type")
			}
			full, e := base64.StdEncoding.DecodeString(headers.Get("Group-Send-Token"))
			integrationCheck(t, e)
			groupSendVerify(t, key, full, ids[1:])
			gotExpiry, e := libsignalgo.GroupSendFullToken(full).GetExpiration()
			integrationCheck(t, e)
			if !gotExpiry.Equal(expiry) {
				t.Fatal("request token expiry differs")
			}
			groupSendDecrypt(t, req.Body, receivers, senderAddr)
		} else if req.GetPath() == "/v1/messages/"+ids[0].String() {
			var sync MyMessages
			integrationCheck(t, json.Unmarshal(req.Body, &sync))
			if len(sync.Messages) != 0 {
				t.Fatal("unexpected sync payload")
			}
		} else {
			t.Fatalf("unexpected request/fallback: %s", req.GetPath())
		}
	}
	if multi != 1 {
		t.Fatalf("multi-recipient requests: %d", multi)
	}
	// Cache refuses near-expiry entries, forcing a future group refresh.
	cache.data[data.GroupIdentifier].Expiration = time.Now().Add(4 * time.Minute)
	if _, _, ok = cache.Get(data.GroupIdentifier); ok {
		t.Fatal("near-expiry cache reused")
	}
}

func groupSendDecrypt(t *testing.T, b []byte, receivers map[libsignalgo.ServiceID]*groupSendStore, sender *libsignalgo.Address) {
	t.Helper()
	if len(b) < 2 || b[0] != 0x23 {
		t.Fatal("not a v2 multi-recipient envelope")
	}
	n, offset := binary.Uvarint(b[1:])
	if offset <= 0 || int(n) != len(receivers) {
		t.Fatal("recipient count")
	}
	rest := b[1+offset:]
	type entry struct {
		id   libsignalgo.ServiceID
		keys []byte
	}
	var entries []entry
	for range n {
		if len(rest) < 68 {
			t.Fatal("short recipient")
		}
		id := libsignalgo.ServiceID{Type: libsignalgo.ServiceIDType(rest[0]), UUID: uuid.UUID(rest[1:17])}
		if rest[17] != 1 || binary.BigEndian.Uint16(rest[18:20]) != 123 {
			t.Fatal("device metadata")
		}
		entries = append(entries, entry{id, rest[20:68]})
		rest = rest[68:]
	}
	if len(rest) < 32 {
		t.Fatal("short envelope tail")
	}
	for _, entry := range entries {
		receiver, ok := receivers[entry.id]
		if !ok {
			t.Fatal("unexpected envelope recipient")
		}
		received := append(append([]byte{0x22}, entry.keys...), rest...)
		usmc, e := libsignalgo.SealedSenderDecryptToUSMC(t.Context(), received, receiver)
		integrationCheck(t, e)
		ciphertext, e := usmc.GetContents()
		integrationCheck(t, e)
		plaintext, e := libsignalgo.GroupDecrypt(t.Context(), ciphertext, sender, receiver)
		integrationCheck(t, e)
		plaintext, e = stripPadding(plaintext)
		integrationCheck(t, e)
		var content signalpb.Content
		integrationCheck(t, proto.Unmarshal(plaintext, &content))
		if content.GetDataMessage().GetBody() != "Offline endorsed group send" {
			t.Fatal("recipient plaintext differs")
		}
	}
}
