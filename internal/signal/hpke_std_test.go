package signal_test

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/hex"
	"testing"

	"github.com/cwbudde/go-signal/internal/signal"
)

// A ciphertext sealed by libsignal (the cgo hpkeSeal) to a throwaway key, so that builds without
// libsignal still check the pure-Go HPKE against it.
const (
	libsignalHPKEPrivate = "a0f0b9159d46b0b4153c80b9aff8fe969fb21f2fe8bb2ca418e746f5feabc154"
	libsignalHPKESealed  = "01a49d48501d35f97ee2629997e695961074fac2a41963b59c01b2f8e31ff5d668b5e5" +
		"6888bebe1ce09bda9bc0eac7c364184bc5a194c60ba640141d1658646797cd04bda7b3"
	libsignalHPKEInfo = "deviceCreatedAt"
)

func TestStdHPKEOpensLibsignalCiphertext(t *testing.T) {
	t.Parallel()

	private, _ := hex.DecodeString(libsignalHPKEPrivate)
	sealed, _ := hex.DecodeString(libsignalHPKESealed)
	aad := []byte{2, 0, 0, 0x10, 0x92}

	plain, err := signal.StdHPKEOpen(private, sealed, []byte(libsignalHPKEInfo), aad)
	if err != nil || string(plain) != "libsignal sealed this" {
		t.Fatalf("open: %q, %v", plain, err)
	}
}

func TestStdHPKERoundTrip(t *testing.T) {
	t.Parallel()

	public, private := x25519Keys(t)
	info, aad := []byte("info"), []byte("aad")

	sealed, err := signal.StdHPKESeal(public, []byte("hello"), info, aad)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}

	plain, err := signal.StdHPKEOpen(private, sealed, info, aad)
	if err != nil || string(plain) != "hello" {
		t.Fatalf("open: %q, %v", plain, err)
	}

	wrongType := append([]byte{2}, sealed[1:]...)
	tampered := append([]byte{}, sealed...)
	tampered[len(tampered)-1] ^= 1

	for name, open := range map[string]func() ([]byte, error){
		"wrong associated data": func() ([]byte, error) { return signal.StdHPKEOpen(private, sealed, info, []byte("x")) },
		"wrong info":            func() ([]byte, error) { return signal.StdHPKEOpen(private, sealed, []byte("x"), aad) },
		"unknown type":          func() ([]byte, error) { return signal.StdHPKEOpen(private, wrongType, info, aad) },
		"tampered ciphertext":   func() ([]byte, error) { return signal.StdHPKEOpen(private, tampered, info, aad) },
		"short":                 func() ([]byte, error) { return signal.StdHPKEOpen(private, sealed[:20], info, aad) },
	} {
		_, err := open()
		if err == nil {
			t.Errorf("%s: open succeeded", name)
		}
	}

	_, err = signal.StdHPKESeal(public[1:], []byte("hello"), info, aad)
	if err == nil {
		t.Error("seal to a public key without the type byte succeeded")
	}
}

// x25519Keys returns a key pair in libsignal's serialization: the public key with the 0x05 type
// byte, the private key as the raw 32-byte scalar.
func x25519Keys(t *testing.T) ([]byte, []byte) {
	t.Helper()

	priv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	return append([]byte{0x05}, priv.PublicKey().Bytes()...), priv.Bytes()
}
