package app_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/go-signal/internal/signal/signaltest"
)

const (
	aliceKey    = "05aaaa"
	aliceNewKey = "05eeee"
)

// identityFake knows the keys of alice (trusted on first use) and bob (untrusted after a
// change); alice is on Signal by number.
func identityFake() *signaltest.Fake {
	first := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)

	return &signaltest.Fake{
		Linked:    []signal.Account{testAccount()},
		Directory: []signal.Recipient{{ACI: aliceACI, Number: aliceNumber}},
		Identities: []signal.Identity{
			{
				Recipient: signal.Recipient{ACI: bobACI}, Fingerprint: "05bbbb", Trust: signal.TrustUntrusted,
				FirstSeen: first, ChangedAt: first.Add(time.Hour),
			},
			{
				Recipient: signal.Recipient{ACI: aliceACI}, Fingerprint: aliceKey, Trust: signal.TrustUnverified,
				FirstSeen: first,
			},
		},
	}
}

func TestIdentitiesList(t *testing.T) {
	t.Parallel()

	ids, err := open(t, identityFake()).IdentitiesList(t.Context(), app.IdentitiesListRequest{})
	if err != nil {
		t.Fatalf("IdentitiesList: %v", err)
	}

	if len(ids) != 2 || ids[0].Recipient.ACI != aliceACI || ids[1].Recipient.ACI != bobACI {
		t.Errorf("IdentitiesList = %+v, want alice and bob by ACI", ids)
	}

	ids, err = open(t, identityFake()).IdentitiesList(t.Context(), app.IdentitiesListRequest{Recipient: bobACI})
	if err != nil || len(ids) != 1 || ids[0].Trust != signal.TrustUntrusted {
		t.Errorf("IdentitiesList(bob) = %+v, %v", ids, err)
	}
}

func TestIdentitiesInvalidRecipient(t *testing.T) {
	t.Parallel()

	tests := []struct {
		arg  string
		want error
	}{
		{app.GroupPrefix + groupID, app.ErrInvalidRecipient},
		{app.SelfRecipient, app.ErrInvalidRecipient},
		{testAccount().Number, app.ErrInvalidRecipient},
		{"nobody", app.ErrInvalidRecipient},
		// Numbers are looked up only while connected, and identities doesn't connect.
		{aliceNumber, signal.ErrNotConnected},
	}

	for _, test := range tests {
		a := open(t, identityFake())

		_, err := a.IdentitiesList(t.Context(), app.IdentitiesListRequest{Recipient: test.arg})
		if !errors.Is(err, test.want) {
			t.Errorf("list %s: got %v, want %v", test.arg, err, test.want)
		}

		_, err = a.IdentitiesShow(t.Context(), test.arg)
		if !errors.Is(err, test.want) {
			t.Errorf("show %s: got %v, want %v", test.arg, err, test.want)
		}

		_, err = a.IdentitiesTrust(t.Context(), app.IdentitiesTrustRequest{Recipient: test.arg})
		if !errors.Is(err, test.want) {
			t.Errorf("trust %s: got %v, want %v", test.arg, err, test.want)
		}
	}
}

func TestIdentitiesShow(t *testing.T) {
	t.Parallel()

	fake := identityFake()

	number, err := open(t, fake).IdentitiesShow(t.Context(), aliceACI)
	if err != nil {
		t.Fatalf("IdentitiesShow: %v", err)
	}

	want := signaltest.SafetyNumberOf(testAccount().ACI, fake.Identities[1])
	if number.Number != want || len(number.Scannable) == 0 || number.Identity.Fingerprint != aliceKey {
		t.Errorf("IdentitiesShow = %+v, want number %s", number, want)
	}

	_, err = open(t, fake).IdentitiesShow(t.Context(), carolACI)
	if !errors.Is(err, signal.ErrUnknownIdentity) {
		t.Errorf("unknown user: got %v, want ErrUnknownIdentity", err)
	}
}

func TestIdentitiesTrust(t *testing.T) {
	t.Parallel()

	fake := identityFake()
	a := open(t, fake)

	// Without a safety number the key is trusted, unverified.
	ident, err := a.IdentitiesTrust(t.Context(), app.IdentitiesTrustRequest{Recipient: bobACI})
	if err != nil || ident.Trust != signal.TrustUnverified {
		t.Fatalf("trust bob = %+v, %v", ident, err)
	}

	// A wrong or malformed number changes nothing.
	for number, want := range map[string]error{
		strings.Repeat("1", 60): signal.ErrSafetyNumberMismatch,
		"12345":                 signal.ErrInvalidSafetyNumber,
	} {
		_, err = a.IdentitiesTrust(t.Context(), app.IdentitiesTrustRequest{Recipient: aliceACI, SafetyNumber: number})
		if !errors.Is(err, want) {
			t.Errorf("trust with %s: got %v, want %v", number, err, want)
		}
	}

	// The right one, in the grouped form the apps show, verifies it.
	number := strings.Join(signal.GroupSafetyNumber(signaltest.SafetyNumberOf(testAccount().ACI, fake.Identities[1])), " ")

	ident, err = a.IdentitiesTrust(t.Context(), app.IdentitiesTrustRequest{Recipient: aliceACI, SafetyNumber: number})
	if err != nil || ident.Trust != signal.TrustVerified {
		t.Fatalf("verify alice = %+v, %v", ident, err)
	}

	// Trusting again without a number keeps it verified.
	ident, err = a.IdentitiesTrust(t.Context(), app.IdentitiesTrustRequest{Recipient: aliceACI})
	if err != nil || ident.Trust != signal.TrustVerified {
		t.Errorf("trust alice again = %+v, %v", ident, err)
	}
}

// TestSendUntrustedIdentity walks through the policy: an identity change arrives, sending fails
// for that recipient until the new key is trusted. Each step uses a client of its own, like the
// commands do.
func TestSendUntrustedIdentity(t *testing.T) {
	t.Parallel()

	fake := identityFake()
	fake.Incoming = []signal.Event{&signal.IdentityChanged{
		Recipient: signal.Recipient{ACI: aliceACI}, OldFingerprint: aliceKey, NewFingerprint: aliceNewKey,
		Time: time.Date(2026, 9, 21, 8, 0, 0, 0, time.UTC),
	}}
	send := app.SendRequest{Recipients: []string{aliceNumber}, Body: "hi"}
	step := &steps{t: t, fake: fake}

	res, err := step.next().Send(t.Context(), send)
	if !errors.Is(err, app.ErrSendFailed) || len(res.Results) != 1 ||
		!errors.Is(res.Results[0].Err, signal.ErrUntrustedIdentity) {
		t.Fatalf("send after change = %+v, %v; want ErrUntrustedIdentity", res, err)
	}

	ident, err := step.next().IdentitiesTrust(t.Context(), app.IdentitiesTrustRequest{Recipient: aliceACI})
	if err != nil || ident.Fingerprint != aliceNewKey || ident.Trust != signal.TrustUnverified {
		t.Fatalf("trust = %+v, %v", ident, err)
	}

	fake.Incoming = nil

	_, err = step.next().Send(t.Context(), send)
	if err != nil {
		t.Errorf("send after trust: %v", err)
	}
}

// steps opens a client of fake per step of a test, like separate commands: next closes the
// previous one first, so that the next step can connect.
type steps struct {
	t    *testing.T
	fake *signaltest.Fake
	prev signal.Client
}

func (s *steps) next() *app.App {
	s.t.Helper()

	if s.prev != nil {
		err := s.prev.Close()
		if err != nil {
			s.t.Errorf("close: %v", err)
		}
	}

	client, err := s.fake.Factory(s.t.Context(), signal.Options{})
	if err != nil {
		s.t.Fatalf("open: %v", err)
	}

	s.t.Cleanup(func() { _ = client.Close() })
	s.prev = client

	return app.New(client)
}

func TestIdentitiesClientError(t *testing.T) {
	t.Parallel()

	fake := identityFake()
	fake.IdentitiesErr = errBoom

	_, err := open(t, fake).IdentitiesList(t.Context(), app.IdentitiesListRequest{})
	if !errors.Is(err, errBoom) || !strings.HasPrefix(err.Error(), "identities list: ") {
		t.Errorf("got %v, want the client error prefixed with the command", err)
	}
}
