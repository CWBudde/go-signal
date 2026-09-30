//go:build cgo || libsignal_go

package signal_test

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"strings"
	"testing"

	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/mautrix-signal/pkg/libsignalgo"
)

func profileTestKey() libsignalgo.ProfileKey { return libsignalgo.ProfileKey{1, 2, 3, 4, 5} }

func independentProfileCipher(t *testing.T, plaintext []byte) []byte {
	t.Helper()

	key := profileTestKey()

	block, err := aes.NewCipher(key[:])
	if err != nil {
		t.Fatal(err)
	}

	aead, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}

	nonce := make([]byte, 12)

	return aead.Seal(nonce, nonce, plaintext, nil)
}

func TestProfileCryptoRoundTripAndBoundaries(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name, value string
		sizes       []int
		padded      int
	}{
		{"name53", strings.Repeat("x", 53), []int{53, 257}, 53},
		{"name54", strings.Repeat("x", 54), []int{53, 257}, 257},
		{"name257", strings.Repeat("x", 257), []int{53, 257}, 257},
		{"about128", strings.Repeat("x", 128), []int{128, 254, 512}, 128},
		{"about129", strings.Repeat("x", 129), []int{128, 254, 512}, 254},
		{"about254", strings.Repeat("x", 254), []int{128, 254, 512}, 254},
		{"about255", strings.Repeat("x", 255), []int{128, 254, 512}, 512},
		{"about512", strings.Repeat("x", 512), []int{128, 254, 512}, 512},
		{"emoji-boundary", strings.Repeat("🌊", 8), []int{32}, 32},
		{"family-only", "\x00Family Name", []int{53, 257}, 53},
		{"multiword", "Given Name\x00Family Name", []int{53, 257}, 53},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			key := profileTestKey()

			encrypted, err := signal.EncryptProfileText(key, testCase.value, testCase.sizes)
			if err != nil {
				t.Fatal(err)
			}

			if len(encrypted) != testCase.padded+28 {
				t.Fatalf("length=%d", len(encrypted))
			}

			block, _ := aes.NewCipher(key[:])
			aead, _ := cipher.NewGCM(block)

			plain, err := aead.Open(nil, encrypted[:12], encrypted[12:], nil)
			if err != nil {
				t.Fatal(err)
			}

			if string(bytes.TrimRight(plain, "\x00")) != testCase.value {
				t.Fatalf("independent plaintext=%q", plain)
			}

			got, err := signal.DecryptProfileText(key, encrypted, testCase.sizes)
			if err != nil || got != testCase.value {
				t.Fatalf("roundtrip=%q,%v", got, err)
			}

			second, err := signal.EncryptProfileText(key, testCase.value, testCase.sizes)
			if err != nil || bytes.Equal(second[:12], encrypted[:12]) {
				t.Fatalf("nonce reused: %v", err)
			}
		})
	}
}

func TestProfileCryptoInvalidCiphertext(t *testing.T) {
	t.Parallel()
	valid := independentProfileCipher(t, make([]byte, 53))
	tampered := bytes.Clone(valid)

	tampered[20] ^= 1
	for _, data := range [][]byte{
		nil, valid[:10], valid[:len(valid)-1], tampered, independentProfileCipher(t, make([]byte, 54)),
	} {
		got, err := signal.DecryptProfileText(profileTestKey(), data, []int{53, 257})
		if err == nil || got != "" {
			t.Fatalf("invalid cipher returned %q,%v", got, err)
		}
	}

	_, err := signal.EncryptProfileText(profileTestKey(), strings.Repeat("a", 258), []int{53, 257})
	if err == nil {
		t.Fatal("oversize accepted")
	}
}
