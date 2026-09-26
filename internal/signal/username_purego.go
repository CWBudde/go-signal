//go:build purego

package signal

import (
	"fmt"

	"github.com/cwbudde/libsignal-go/usernames"
)

// usernameHash returns libsignal's hash of username (nickname.discriminator), which is what the
// server knows it by. The nickname is case-insensitive.
func usernameHash(username string) ([]byte, error) {
	hash, err := usernames.Hash(username)
	if err != nil {
		return nil, fmt.Errorf("%w %q: %w", ErrInvalidUsername, username, err)
	}

	return hash[:], nil
}
