package signal_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/cwbudde/go-signal/internal/signal"
)

func TestNormalizeSafetyNumber(t *testing.T) {
	t.Parallel()

	digits := strings.Repeat("0123456789", 6)
	grouped := strings.Join(signal.GroupSafetyNumber(digits), " ")

	for _, in := range []string{digits, grouped, " " + grouped + "\n", strings.ReplaceAll(grouped, " ", "\t")} {
		got, err := signal.NormalizeSafetyNumber(in)
		if err != nil || got != digits {
			t.Errorf("NormalizeSafetyNumber(%q) = %q, %v", in, got, err)
		}
	}

	for _, in := range []string{"", digits[:59], digits + "0", digits[:59] + "x", strings.Replace(digits, "0", "-", 1)} {
		_, err := signal.NormalizeSafetyNumber(in)
		if !errors.Is(err, signal.ErrInvalidSafetyNumber) {
			t.Errorf("NormalizeSafetyNumber(%q) error = %v, want ErrInvalidSafetyNumber", in, err)
		}
	}
}

func TestGroupSafetyNumber(t *testing.T) {
	t.Parallel()

	blocks := signal.GroupSafetyNumber(strings.Repeat("12345", 12))
	if len(blocks) != 12 || slices.ContainsFunc(blocks, func(b string) bool { return b != "12345" }) {
		t.Errorf("GroupSafetyNumber = %q, want 12 blocks of 5", blocks)
	}

	if got := signal.GroupSafetyNumber("1234567"); !slices.Equal(got, []string{"12345", "67"}) {
		t.Errorf("GroupSafetyNumber(1234567) = %q", got)
	}
}

func TestTrustLevel(t *testing.T) {
	t.Parallel()

	for _, level := range []signal.TrustLevel{signal.TrustUntrusted, signal.TrustUnverified, signal.TrustVerified} {
		if got := signal.ParseTrustLevel(level.String()); got != level {
			t.Errorf("ParseTrustLevel(%s) = %v", level, got)
		}
	}

	if signal.TrustUntrusted.Trusted() || !signal.TrustUnverified.Trusted() || !signal.TrustVerified.Trusted() {
		t.Error("only the trusted-* levels may be sent to")
	}

	if signal.ParseTrustLevel("bogus") != 0 || signal.TrustLevel(0).Trusted() {
		t.Error("unknown levels must not be trusted")
	}
}

func TestUntrustedError(t *testing.T) {
	t.Parallel()

	err := signal.UntrustedError(signal.Recipient{ACI: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"})
	if !errors.Is(err, signal.ErrUntrustedIdentity) ||
		!strings.Contains(err.Error(), "go-signal identities trust aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa") {
		t.Errorf("UntrustedError = %v", err)
	}
}
