//go:build purego

package signal

// hpkeOpen decrypts ciphertext sealed to the serialized private key (libsignal's
// PrivateKey.open).
func hpkeOpen(privateKey, ciphertext, info, associatedData []byte) ([]byte, error) {
	return stdHPKEOpen(privateKey, ciphertext, info, associatedData)
}

// hpkeSeal encrypts plaintext to the serialized public key (libsignal's PublicKey.seal).
func hpkeSeal(publicKey, plaintext, info, associatedData []byte) ([]byte, error) {
	return stdHPKESeal(publicKey, plaintext, info, associatedData)
}
