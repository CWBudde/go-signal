package signal

import (
	"crypto/ecdh"
	"crypto/hpke"
	"errors"
	"fmt"
)

// libsignal frames an HPKE ciphertext as a type byte, the encapsulated key and the AEAD output.
// Type 1 is base mode with DHKEM(X25519, HKDF-SHA256), HKDF-SHA256 and AES-256-GCM
// (rust/crypto/src/hpke.rs). Keys use libsignal's serialization: a 32-byte private key, and a
// public key with the 0x05 (Djb) type prefix.
const (
	hpkeTypeX25519AES256GCM = 1
	x25519KeyLen            = 32
	djbKeyType              = 0x05
)

var errHPKE = errors.New("hpke")

// stdHPKEOpen is hpkeOpen implemented with crypto/hpke. The purego build uses it; the cgo build
// checks it against libsignal in the differential tests.
func stdHPKEOpen(privateKey, ciphertext, info, associatedData []byte) ([]byte, error) {
	if len(ciphertext) < 1+x25519KeyLen || ciphertext[0] != hpkeTypeX25519AES256GCM {
		return nil, fmt.Errorf("%w: unsupported or short ciphertext", errHPKE)
	}

	priv, err := ecdh.X25519().NewPrivateKey(privateKey)
	if err != nil {
		return nil, fmt.Errorf("%w: private key: %w", errHPKE, err)
	}

	key, err := hpke.NewDHKEMPrivateKey(priv)
	if err != nil {
		return nil, fmt.Errorf("%w: private key: %w", errHPKE, err)
	}

	enc, sealed := ciphertext[1:1+x25519KeyLen], ciphertext[1+x25519KeyLen:]

	recipient, err := hpke.NewRecipient(enc, key, hpke.HKDFSHA256(), hpke.AES256GCM(), info)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errHPKE, err)
	}

	plain, err := recipient.Open(associatedData, sealed)
	if err != nil {
		return nil, fmt.Errorf("%w: open: %w", errHPKE, err)
	}

	return plain, nil
}

// stdHPKESeal is hpkeSeal implemented with crypto/hpke.
func stdHPKESeal(publicKey, plaintext, info, associatedData []byte) ([]byte, error) {
	if len(publicKey) != 1+x25519KeyLen || publicKey[0] != djbKeyType {
		return nil, fmt.Errorf("%w: unsupported public key", errHPKE)
	}

	pub, err := ecdh.X25519().NewPublicKey(publicKey[1:])
	if err != nil {
		return nil, fmt.Errorf("%w: public key: %w", errHPKE, err)
	}

	key, err := hpke.NewDHKEMPublicKey(pub)
	if err != nil {
		return nil, fmt.Errorf("%w: public key: %w", errHPKE, err)
	}

	enc, sender, err := hpke.NewSender(key, hpke.HKDFSHA256(), hpke.AES256GCM(), info)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errHPKE, err)
	}

	sealed, err := sender.Seal(associatedData, plaintext)
	if err != nil {
		return nil, fmt.Errorf("%w: seal: %w", errHPKE, err)
	}

	out := make([]byte, 0, 1+len(enc)+len(sealed))
	out = append(out, hpkeTypeX25519AES256GCM)
	out = append(out, enc...)

	return append(out, sealed...), nil
}
