//go:build cgo && !purego

package signal_test

import (
	"bytes"
	"testing"

	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/libsignal-go/usernames"
)

// Differential tests (PLAN.md 7.3): the cgo backend (libsignal FFI) and the pure-Go code the
// purego build uses must agree on the same inputs.

func TestDiffUsernameHash(t *testing.T) {
	t.Parallel()

	names := []string{
		"he110.01", "He110.01", "HE110.01", "jimio.42", "a_b_c.999", "signal_user.1234567890",
		"_underscore.55", "abcdefghijklmnopqrstuvwxyz0123456789_.12",
		// Invalid: both must refuse them.
		"0zerostart.42", "no_discriminator", "🦀.42", "zero.00", "short.1", "a.", ".42", "",
		"nickname.042", "nickname.9999999999999999999999",
	}

	for _, name := range names {
		cgoHash, cgoErr := signal.UsernameHash(name)
		pureHash, pureErr := usernames.Hash(name)

		switch {
		case (cgoErr == nil) != (pureErr == nil):
			t.Errorf("%q: cgo error %v, purego error %v", name, cgoErr, pureErr)
		case cgoErr == nil && !bytes.Equal(cgoHash, pureHash[:]):
			t.Errorf("%q: cgo %x, purego %x", name, cgoHash, pureHash)
		}
	}
}

func TestDiffHPKE(t *testing.T) {
	t.Parallel()

	public, private := serializeKeys(t, identityKeys(t))
	info, aad := []byte("deviceCreatedAt"), []byte{3, 0, 0, 0, 7}

	for _, plaintext := range [][]byte{{}, []byte("x"), bytes.Repeat([]byte("0123456789"), 100)} {
		sealed, err := signal.HPKESeal(public, plaintext, info, aad)
		if err != nil {
			t.Fatal(err)
		}

		plain, err := signal.StdHPKEOpen(private, sealed, info, aad)
		if err != nil || !bytes.Equal(plain, plaintext) {
			t.Errorf("libsignal seal, crypto/hpke open: %q, %v", plain, err)
		}

		sealed, err = signal.StdHPKESeal(public, plaintext, info, aad)
		if err != nil {
			t.Fatal(err)
		}

		plain, err = signal.HPKEOpen(private, sealed, info, aad)
		if err != nil || !bytes.Equal(plain, plaintext) {
			t.Errorf("crypto/hpke seal, libsignal open: %q, %v", plain, err)
		}
	}
}
