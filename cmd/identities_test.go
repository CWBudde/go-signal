package cmd_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/cmd"
	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/go-signal/internal/signal/signaltest"
)

const (
	identitiesCmd    = "identities"
	trustCmd         = "trust"
	safetyNumberFlag = "--safety-number"
	aliceKey         = "05a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8f90"
	aliceNewKey      = "05ffeeddccbbaa99887766554433221100ffeeddccbbaa99887766554433221100"
	bobKey           = "05b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0"
)

// identityFake knows alice's key (trusted on first use) and bob's (verified).
func identityFake() *signaltest.Fake {
	first := time.Date(2026, 9, 20, 12, 30, 0, 0, time.UTC)

	fake := sendFake()
	fake.Identities = []signal.Identity{
		{Recipient: signal.Recipient{ACI: aliceACI}, Fingerprint: aliceKey, Trust: signal.TrustUnverified, FirstSeen: first},
		{
			Recipient: signal.Recipient{ACI: bobACI}, Fingerprint: bobKey, Trust: signal.TrustVerified,
			FirstSeen: first.Add(-24 * time.Hour),
		},
	}

	return fake
}

// aliceChanged is the event of alice's key changing.
func aliceChanged() *signal.IdentityChanged {
	return &signal.IdentityChanged{
		Recipient: signal.Recipient{ACI: aliceACI}, OldFingerprint: aliceKey, NewFingerprint: aliceNewKey,
		Time: time.Date(2026, 9, 21, 8, 15, 0, 0, time.UTC),
	}
}

func TestIdentitiesList(t *testing.T) {
	t.Parallel()

	for _, format := range []string{formatPlain, formatJSON} {
		t.Run(format, func(t *testing.T) {
			t.Parallel()

			fake := identityFake()
			// A change seen by an earlier receive.
			fake.Identities[0] = signal.Identity{
				Recipient: signal.Recipient{ACI: aliceACI}, Fingerprint: aliceNewKey, Trust: signal.TrustUntrusted,
				FirstSeen: fake.Identities[0].FirstSeen, ChangedAt: aliceChanged().Time,
			}

			out, err := run(t, fake, "-o", format, identitiesCmd, "list")
			if err != nil {
				t.Fatalf("identities list: %v", err)
			}

			golden(t, "identities_list_"+format, out)

			if len(fake.Connects()) != 0 {
				t.Error("identities list connected")
			}
		})
	}
}

func TestIdentitiesListRecipient(t *testing.T) {
	t.Parallel()

	out, err := run(t, identityFake(), "-o", formatJSON, identitiesCmd, "list", bobACI)
	if err != nil {
		t.Fatalf("identities list: %v", err)
	}

	if strings.Contains(out, aliceACI) || !strings.Contains(out, bobACI) {
		t.Errorf("list %s = %s, want only bob", bobACI, out)
	}

	_, err = run(t, identityFake(), identitiesCmd, "list", app.GroupPrefix+groupID)
	if !errors.Is(err, app.ErrInvalidRecipient) {
		t.Errorf("list of a group: got %v, want ErrInvalidRecipient", err)
	}
}

func TestIdentitiesShow(t *testing.T) {
	t.Parallel()

	for _, format := range []string{formatPlain, formatJSON} {
		t.Run(format, func(t *testing.T) {
			t.Parallel()

			out, err := run(t, identityFake(), "-o", format, identitiesCmd, "show", aliceACI)
			if err != nil {
				t.Fatalf("identities show: %v", err)
			}

			golden(t, "identities_show_"+format, out)
		})
	}
}

func TestIdentitiesShowUnknown(t *testing.T) {
	t.Parallel()

	_, err := run(t, identityFake(), identitiesCmd, "show", carolACI)
	if !errors.Is(err, signal.ErrUnknownIdentity) {
		t.Errorf("got %v, want ErrUnknownIdentity", err)
	}
}

func TestIdentitiesTrust(t *testing.T) {
	t.Parallel()

	fake := identityFake()
	number := signaltest.SafetyNumberOf(testAccount().ACI, fake.Identities[0])

	// In order, on the same fake: trust (plain, JSON), two failed verifications, a verification.
	tests := []struct {
		golden string
		args   []string
		want   error
	}{
		{"identities_trust_plain", []string{identitiesCmd, trustCmd, aliceACI}, nil},
		{"identities_trust_json", []string{"-o", formatJSON, identitiesCmd, trustCmd, aliceACI}, nil},
		{
			"",
			[]string{identitiesCmd, trustCmd, aliceACI, safetyNumberFlag, strings.Repeat("0", 60)},
			signal.ErrSafetyNumberMismatch,
		},
		{"", []string{identitiesCmd, trustCmd, aliceACI, safetyNumberFlag, "123"}, signal.ErrInvalidSafetyNumber},
		{"identities_verify_plain", []string{identitiesCmd, trustCmd, aliceACI, safetyNumberFlag, number}, nil},
	}

	for _, test := range tests {
		out, err := run(t, fake, test.args...)
		if !errors.Is(err, test.want) {
			t.Fatalf("%v: got %v, want %v", test.args, err, test.want)
		}

		if test.golden != "" {
			golden(t, test.golden, out)
		}
	}
}

// TestIdentityChangeBlocksSend is the policy end to end: receive reports the change, send fails
// for alice with a hint, and works again after identities trust.
func TestIdentityChangeBlocksSend(t *testing.T) {
	t.Parallel()

	fake := identityFake()
	fake.Incoming = []signal.Event{aliceChanged()}

	for _, format := range []string{formatPlain, formatJSON} {
		out, err := run(t, fake, "-o", format, receiveCmd, "--max", "1")
		if err != nil {
			t.Fatalf("receive: %v", err)
		}

		golden(t, "receive_identity_changed_"+format, out)
	}

	fake.Incoming = nil

	for _, format := range []string{formatPlain, formatJSON} {
		out, err := runSend(t, fake, "", "-o", format, sendCmd, "-m", "hello", aliceNumber, "@bob.42")
		if !errors.Is(err, app.ErrSendFailed) || cmd.ExitCode(err) != cmd.ExitFailure {
			t.Fatalf("send to alice: got %v, want ErrSendFailed", err)
		}

		golden(t, "send_untrusted_"+format, out)
	}

	_, err := run(t, fake, identitiesCmd, trustCmd, aliceACI)
	if err != nil {
		t.Fatalf("trust: %v", err)
	}

	_, err = runSend(t, fake, "", sendCmd, "-m", "hello", aliceNumber)
	if err != nil {
		t.Errorf("send after trust: %v", err)
	}
}
