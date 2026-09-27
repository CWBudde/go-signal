//go:build libsignal_go

package libsignalgo_test

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"testing"
	"time"

	"github.com/cwbudde/libsignal-go/attest/shimtest"
	"github.com/cwbudde/libsignal-go/noise"
	"go.mau.fi/mautrix-signal/pkg/libsignalgo"
	"google.golang.org/protobuf/encoding/protowire"
)

// These tests are injected into a temporary copy of the pinned shim. The only
// attestation with a known private key needs a test-only TCB-number exception;
// production rejection is asserted on both sides of the scoped exception.
func TestCDSIIntegration(t *testing.T) {
	fixture := cdsiKnownKeyFixture(t)
	reject := func(t *testing.T) {
		t.Helper()

		state, err := libsignalgo.NewCDS2ClientState(fixture.mrenclave, fixture.msg, fixture.now)
		if state != nil {
			_ = state.Destroy()
			t.Fatal("production policy accepted the obsolete fixture")
		}

		wantSGXCode(t, err, libsignalgo.ErrorCodeInvalidMessage)
	}

	reject(t)
	t.Run("expired fixture enabled only for this scope", func(t *testing.T) {
		shimtest.EnableExpiredFixture(t)
		t.Run("transport", func(t *testing.T) { cdsiTransport(t, fixture) })
		t.Run("reject and recover", func(t *testing.T) { cdsiRejectTransport(t, fixture) })
		t.Run("failed later chunk", func(t *testing.T) { cdsiRejectPartialChunk(t, fixture) })
		t.Run("failed handshakes", func(t *testing.T) { cdsiRejectHandshake(t, fixture) })
	})
	reject(t)
}

func cdsiKnownKeyFixture(t *testing.T) cdsiFixture {
	t.Helper()

	msg := protowire.AppendTag(nil, 2, protowire.BytesType)
	msg = protowire.AppendBytes(msg, readSGXTestdata(t, "cds2_test.evidence"))
	msg = protowire.AppendTag(msg, 3, protowire.BytesType)
	msg = protowire.AppendBytes(msg, readSGXTestdata(t, "cds2_test.endorsements"))

	return cdsiFixture{
		mrenclave: cdsiValue(hex.DecodeString(string(bytes.TrimSpace(readSGXTestdata(t, "cds2_test.mrenclave")))))(t),
		msg:       msg, now: time.UnixMilli(1655857680000),
	}
}

func cdsiResponder(t *testing.T, client *libsignalgo.SGXClientState, payload []byte) ([]byte, *noise.Transport) {
	t.Helper()

	key := cdsiValue(hex.DecodeString(string(bytes.TrimSpace(readSGXTestdata(t, "cds2_test.privatekey")))))(t)
	server := cdsiValue(noise.NewResponder(noise.NKhfs, key, nil))(t)
	initial := cdsiValue(client.InitialRequest())(t)
	if len(initial) != noise.KeySize+noise.KEMPublicKeySize+2*noise.TagSize {
		t.Fatalf("not a hybrid initial request: %d bytes", len(initial))
	}

	if plain := cdsiValue(server.ReadMessage(initial))(t); len(plain) != 0 {
		t.Fatalf("unexpected handshake payload: %x", plain)
	}

	reply := cdsiValue(server.WriteMessage(payload))(t)

	return reply, cdsiValue(server.Transport())(t)
}

func cdsiConnect(t *testing.T, fixture cdsiFixture) (*libsignalgo.SGXClientState, *noise.Transport) {
	t.Helper()

	client := fixture.newState(t)
	reply, server := cdsiResponder(t, client, nil)
	if err := client.CompleteHandshake(reply); err != nil {
		t.Fatal(err)
	}

	return client, server
}

func cdsiTransport(t *testing.T, fixture cdsiFixture) {
	t.Helper()

	client, server := cdsiConnect(t, fixture)
	_, err := client.InitialRequest()
	wantSGXCode(t, err, libsignalgo.ErrorCodeInvalidState)
	// A repeated completion is invalid, but SGX (unlike HSM) keeps the channel.
	wantSGXCode(t, client.CompleteHandshake(nil), libsignalgo.ErrorCodeInvalidState)

	for _, size := range []int{
		0, 1, noise.MaxPayloadSize - 1, noise.MaxPayloadSize,
		noise.MaxPayloadSize + 1, 2 * noise.MaxPayloadSize, 2*noise.MaxPayloadSize + 1,
	} {
		t.Run(fmt.Sprintf("%d bytes", size), func(t *testing.T) {
			plain := bytes.Repeat([]byte{0x42}, size)
			ct := cdsiValue(client.EstablishedSend(plain))(t)
			chunks := (size + noise.MaxPayloadSize - 1) / noise.MaxPayloadSize
			if len(ct) != size+chunks*noise.TagSize {
				t.Fatalf("ciphertext length %d for %d bytes", len(ct), size)
			}

			cdsiEqual(t, cdsiValue(server.Recv(ct))(t), plain)
			ct = cdsiValue(server.Send(plain))(t)
			cdsiEqual(t, cdsiValue(client.EstablishedReceive(ct))(t), plain)
		})
	}

	if err := client.Destroy(); err != nil {
		t.Fatal(err)
	}

	wantSGXUnusable(t, client)
}

func cdsiRejectTransport(t *testing.T, fixture cdsiFixture) {
	t.Helper()

	for _, mutation := range []string{"tag", "truncated", "wrong channel"} {
		t.Run(mutation, func(t *testing.T) {
			client, server := cdsiConnect(t, fixture)
			plain := []byte("authenticated response")
			valid := cdsiValue(server.Send(plain))(t)
			bad := bytes.Clone(valid)

			switch mutation {
			case "tag":
				bad[len(bad)-1] ^= 1
			case "truncated":
				bad = bad[:len(bad)-1]
			case "wrong channel":
				_, other := cdsiConnect(t, fixture)
				bad = cdsiValue(other.Send(plain))(t)
			}

			got, err := client.EstablishedReceive(bad)
			wantSGXCode(t, err, libsignalgo.ErrorCodeInvalidMessage)
			if len(got) != 0 {
				t.Fatal("unauthenticated plaintext returned")
			}

			// A failed single-chunk receive must not consume its nonce.
			cdsiEqual(t, cdsiValue(client.EstablishedReceive(valid))(t), plain)
			_, err = client.EstablishedReceive(valid)
			wantSGXCode(t, err, libsignalgo.ErrorCodeInvalidMessage)
			cdsiEqual(t, cdsiValue(client.EstablishedReceive(cdsiValue(server.Send(plain))(t)))(t), plain)
		})
	}
}

func cdsiRejectHandshake(t *testing.T, fixture cdsiFixture) {
	t.Helper()

	for _, mutation := range []string{"tag", "truncated", "payload", "wrong transcript"} {
		t.Run(mutation, func(t *testing.T) {
			client := fixture.newState(t)
			reply, _ := cdsiResponder(t, client, nil)
			switch mutation {
			case "tag":
				reply[len(reply)-1] ^= 1
			case "truncated":
				reply = reply[:len(reply)-1]
			case "payload":
				reply, _ = cdsiResponder(t, client, []byte{1})
			case "wrong transcript":
				reply, _ = cdsiResponder(t, fixture.newState(t), nil)
			}

			wantSGXCode(t, client.CompleteHandshake(reply), libsignalgo.ErrorCodeInvalidMessage)
			wantSGXUnusable(t, client)
		})
	}
}

func cdsiRejectPartialChunk(t *testing.T, fixture cdsiFixture) {
	t.Helper()

	client, server := cdsiConnect(t, fixture)
	plain := bytes.Repeat([]byte{0x42}, noise.MaxPayloadSize+1)
	valid := cdsiValue(server.Send(plain))(t)
	bad := bytes.Clone(valid)
	bad[len(bad)-1] ^= 1

	got, err := client.EstablishedReceive(bad)
	wantSGXCode(t, err, libsignalgo.ErrorCodeInvalidMessage)
	if len(got) != 0 {
		t.Fatal("failed multi-chunk receive returned partial plaintext")
	}

	// Like libsignal, the transport consumes authenticated chunks before a
	// later chunk fails. Only the failed chunk's nonce remains available.
	_, err = client.EstablishedReceive(valid)
	wantSGXCode(t, err, libsignalgo.ErrorCodeInvalidMessage)
	cdsiEqual(t, cdsiValue(client.EstablishedReceive(valid[noise.MaxMessageSize:]))(t), plain[noise.MaxPayloadSize:])
	cdsiEqual(t, cdsiValue(client.EstablishedReceive(cdsiValue(server.Send([]byte("next")))(t)))(t), []byte("next"))
}

func cdsiEqual(t *testing.T, got, want []byte) {
	t.Helper()
	if !bytes.Equal(got, want) {
		t.Fatalf("plaintext mismatch: got %d bytes, want %d bytes", len(got), len(want))
	}
}

func cdsiValue[T any](value T, err error) func(*testing.T) T {
	return func(t *testing.T) T {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
}
