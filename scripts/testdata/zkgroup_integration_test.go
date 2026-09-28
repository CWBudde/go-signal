// This file is injected as a test-only overlay by test-zkgroup-integration.sh.
// It intentionally shares signalmeow's package to test private receive paths.
package signalmeow

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/cwbudde/mautrix-signal/pkg/libsignalgo"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow/protobuf/signalpb"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow/store"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow/types"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow/web"
	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"google.golang.org/protobuf/proto"
)

func integrationCheck(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

type integrationRecipients struct {
	store.RecipientStore
	keys map[uuid.UUID]libsignalgo.ProfileKey
}

func (r *integrationRecipients) StoreProfileKey(_ context.Context, id uuid.UUID, key libsignalgo.ProfileKey) error {
	r.keys[id] = key
	return nil
}

func TestZKGroupIntegrationResponse(t *testing.T) {
	aci := uuid.New()
	key := libsignalgo.ProfileKey{1, 2, 3}
	master := libsignalgo.GroupMasterKey{8, 7, 6}
	group, e := master.SecretParams()
	integrationCheck(t, e)
	recipients := &integrationRecipients{keys: make(map[uuid.UUID]libsignalgo.ProfileKey)}
	cli := NewClient(&store.Device{DeviceData: store.DeviceData{ACI: aci}, RecipientStore: recipients}, zerolog.Nop(), nil)
	plaintext := &Group{Title: "Offline group", Description: "Encrypted description", Revision: 42, DisappearingMessagesDuration: 3600}
	encrypted, e := cli.EncryptGroup(t.Context(), plaintext, group)
	integrationCheck(t, e)
	encrypted.Version = plaintext.Revision
	timer, e := encryptBlobIntoGroupProperty(group, &signalpb.GroupAttributeBlob{Content: &signalpb.GroupAttributeBlob_DisappearingMessagesDuration{DisappearingMessagesDuration: 3600}})
	integrationCheck(t, e)
	encrypted.DisappearingMessagesTimer = *timer
	uid, e := group.EncryptServiceID(libsignalgo.NewACIServiceID(aci))
	integrationCheck(t, e)
	profile, e := group.EncryptProfileKey(key, aci)
	integrationCheck(t, e)
	encrypted.Members = []*signalpb.Member{{UserId: uid[:], ProfileKey: profile[:], Role: signalpb.Member_ADMINISTRATOR}}
	body, e := proto.Marshal(&signalpb.GroupResponse{Group: encrypted, GroupSendEndorsementsResponse: make([]byte, 25)})
	integrationCheck(t, e)
	// The 25-byte endorsement encoding is structurally valid but expired:
	// reserved zero, two empty vectors, and expiration zero.
	// A mocked HTTP response traverses the real decrypt/store/cache path. Unsupported
	// endorsements must not prevent returning the successfully decrypted group.
	response := httptest.NewRecorder()
	_, e = response.Write(body)
	integrationCheck(t, e)
	httpResponse := response.Result()
	defer httpResponse.Body.Close()
	got, e := cli.parseGroupResponse(t.Context(), httpResponse, types.SerializedGroupMasterKey(base64.StdEncoding.EncodeToString(master[:])))
	integrationCheck(t, e)
	if got.Title != plaintext.Title || got.Description != plaintext.Description || got.Revision != 42 || got.DisappearingMessagesDuration != 3600 {
		t.Fatalf("group attributes: %+v", got)
	}
	if len(got.Members) != 1 || got.Members[0].ACI != aci || recipients.keys[aci] != key {
		t.Fatal("group member/profile key did not survive receive")
	}
	if _, _, cached := cli.GroupCache.Get(got.GroupIdentifier); cached {
		t.Fatal("invalid endorsements were cached")
	}
	// Corrupt an authenticated attribute: the error must propagate, not return a group.
	encrypted.Title[0] ^= 1
	body, e = proto.Marshal(&signalpb.GroupResponse{Group: encrypted})
	integrationCheck(t, e)
	response = httptest.NewRecorder()
	_, e = response.Write(body)
	integrationCheck(t, e)
	httpResponse = response.Result()
	defer httpResponse.Body.Close()
	if _, e = cli.parseGroupResponse(t.Context(), httpResponse, got.GroupMasterKey); e == nil {
		t.Fatal("accepted tampered group title")
	}
}

type integrationTransport struct {
	target *url.URL
	base   http.RoundTripper
}

func (r integrationTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.URL.Scheme = r.target.Scheme
	clone.URL.Host = r.target.Host
	clone.Host = r.target.Host
	return r.base.RoundTrip(clone)
}

func TestZKGroupIntegrationProfile(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	aci := uuid.New()
	key := libsignalgo.ProfileKey{2, 4, 6}
	version, e := key.GetProfileKeyVersion(aci)
	integrationCheck(t, e)
	access, e := key.DeriveAccessKey()
	integrationCheck(t, e)
	requestContext, e := libsignalgo.CreateProfileKeyCredentialRequestContext(prodServerPublicParams, aci, key)
	integrationCheck(t, e)
	request, e := requestContext.ProfileKeyCredentialRequestContextGetRequest()
	integrationCheck(t, e)
	encryptedAbout, e := encryptString(key, "Offline profile", 128)
	integrationCheck(t, e)
	body, e := json.Marshal(ProfileResponse{About: encryptedAbout})
	integrationCheck(t, e)
	expectedPath := "/v1/profile/" + aci.String() + "/" + version.String() + "/" + hex.EncodeToString(request[:]) + "?credentialType=expiringProfileKey"
	checked := make(chan bool, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.CloseNow()
		_, payload, err := conn.Read(ctx)
		if err != nil {
			t.Error(err)
			return
		}
		var message signalpb.WebSocketMessage
		if err = proto.Unmarshal(payload, &message); err != nil {
			t.Error(err)
			return
		}
		req := message.GetRequest()
		valid := req.GetVerb() == http.MethodGet && req.GetPath() == expectedPath
		found := false
		for _, header := range req.Headers {
			if strings.EqualFold(header, "Unidentified-Access-Key:"+base64.StdEncoding.EncodeToString(access[:])) || strings.EqualFold(header, "Unidentified-Access-Key: "+base64.StdEncoding.EncodeToString(access[:])) {
				found = true
			}
		}
		checked <- valid && found
		reply := &signalpb.WebSocketMessage{Type: signalpb.WebSocketMessage_RESPONSE.Enum(), Response: &signalpb.WebSocketResponseMessage{Id: req.Id, Status: proto.Uint32(200), Body: body}}
		payload, err = proto.Marshal(reply)
		if err != nil {
			t.Error(err)
			return
		}
		if err = conn.Write(ctx, websocket.MessageBinary, payload); err != nil {
			t.Error(err)
			return
		}
		<-ctx.Done()
	}))
	defer server.Close()
	target, e := url.Parse(server.URL)
	integrationCheck(t, e)
	original := web.SignalHTTPClient.Transport
	web.SignalHTTPClient.Transport = integrationTransport{target, http.DefaultTransport}
	defer func() { web.SignalHTTPClient.Transport = original }()
	ws := web.NewSignalWebsocket(nil)
	status := ws.Connect(ctx, nil)
	defer func() { cancel(); _ = ws.Close() }()
	select {
	case s := <-status:
		if s.Event != web.SignalWebsocketConnectionEventConnected {
			t.Fatalf("websocket: %+v", s)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	cli := &Client{UnauthedWS: ws}
	got, e := cli.fetchProfileWithRequestAndKey(ctx, aci, []byte(hex.EncodeToString(request[:])), &key)
	integrationCheck(t, e)
	if got.About != "Offline profile" || got.Key != key {
		t.Fatalf("profile: %+v", got)
	}
	if !<-checked {
		t.Fatal("profile request path or access key mismatch")
	}
}
