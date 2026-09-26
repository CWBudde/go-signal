//go:build purego

package signal

// hpkeOpen decrypts ciphertext sealed to the serialized private key (libsignal's
// PrivateKey.open).
func hpkeOpen(privateKey, ciphertext, info, associatedData []byte) ([]byte, error) {
	return stdHPKEOpen(privateKey, ciphertext, info, associatedData)
}
