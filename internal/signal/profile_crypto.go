//go:build cgo || libsignal_go

package signal

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"fmt"
	"slices"

	"github.com/cwbudde/mautrix-signal/pkg/libsignalgo"
)

const profileCipherOverhead = 28

func encryptProfileText(key libsignalgo.ProfileKey, value string, sizes []int) ([]byte, error) {
	paddedSize := 0

	for _, size := range sizes {
		if len(value) <= size {
			paddedSize = size
			break
		}
	}

	if paddedSize == 0 {
		return nil, fmt.Errorf("%w: profile text exceeds padding limit", ErrInvalidProfileUpdate)
	}

	plain := make([]byte, paddedSize)
	copy(plain, value)

	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, fmt.Errorf("profile cipher: %w", err)
	}

	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("profile cipher: %w", err)
	}

	nonce := make([]byte, aead.NonceSize())

	_, err = rand.Read(nonce)
	if err != nil {
		return nil, fmt.Errorf("profile nonce: %w", err)
	}

	return aead.Seal(nonce, nonce, plain, nil), nil
}

func decryptProfileBytes(key libsignalgo.ProfileKey, encrypted []byte, sizes []int) ([]byte, error) {
	if !slices.Contains(sizes, len(encrypted)-profileCipherOverhead) {
		return nil, fmt.Errorf("%w: ciphertext length", ErrInvalidProfile)
	}

	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, fmt.Errorf("profile cipher: %w", err)
	}

	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("profile cipher: %w", err)
	}

	plain, err := aead.Open(nil, encrypted[:aead.NonceSize()], encrypted[aead.NonceSize():], nil)
	if err != nil {
		return nil, fmt.Errorf("%w: ciphertext authentication: %w", ErrInvalidProfile, err)
	}

	return plain, nil
}

func decryptProfileText(key libsignalgo.ProfileKey, encrypted []byte, sizes []int) (string, error) {
	plain, err := decryptProfileBytes(key, encrypted, sizes)
	if err != nil {
		return "", err
	}

	return string(bytes.TrimRight(plain, "\x00")), nil
}
