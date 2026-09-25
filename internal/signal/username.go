//go:build cgo

package signal

/*
#include <stdint.h>
#include <stdlib.h>

// Declaration copied from libsignal's signal_ffi.h (see hpke.go).
typedef struct SignalFfiError SignalFfiError;
typedef uint8_t SignalType_FixedArray32_uint8_t[32];

SignalFfiError* signal_username_hash(SignalType_FixedArray32_uint8_t* out, const int8_t* username);
*/
import "C"

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"unsafe"

	"github.com/google/uuid"
	"go.mau.fi/mautrix-signal/pkg/signalmeow/web"
)

// usernameHashPath looks up the ACI of a username hash. The server refuses it with credentials.
const usernameHashPath = "/v1/accounts/username_hash/"

// ErrInvalidUsername means that a username is not of the form nickname.discriminator.
var ErrInvalidUsername = errors.New("invalid username")

// usernameHash returns libsignal's hash of username (nickname.discriminator), which is what the
// server knows it by. The nickname is case-insensitive.
func usernameHash(username string) ([]byte, error) {
	name := C.CString(username)
	defer C.free(unsafe.Pointer(name))

	var out C.SignalType_FixedArray32_uint8_t

	err := ffiError(C.signal_username_hash(&out, (*C.int8_t)(unsafe.Pointer(name))))
	if err != nil {
		return nil, fmt.Errorf("%w %q: %w", ErrInvalidUsername, username, err)
	}

	return C.GoBytes(unsafe.Pointer(&out[0]), C.int(len(out))), nil
}

// lookupUsername returns the ACI of username.
func lookupUsername(ctx context.Context, username string) (uuid.UUID, error) {
	hash, err := usernameHash(username)
	if err != nil {
		return uuid.Nil, err
	}

	path := usernameHashPath + base64.RawURLEncoding.EncodeToString(hash)

	resp, err := web.SendHTTPRequest(ctx, web.APIHostname, http.MethodGet, path, nil)
	if err != nil {
		return uuid.Nil, fmt.Errorf("look up username: %w", err)
	}

	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return uuid.Nil, fmt.Errorf("look up username: read response: %w", err)
	}

	return aciFromUsernameResponse(resp.StatusCode, body)
}

// aciFromUsernameResponse decodes the server's answer to a username hash lookup.
func aciFromUsernameResponse(status int, body []byte) (uuid.UUID, error) {
	switch {
	case status == http.StatusNotFound:
		return uuid.Nil, ErrNotOnSignal
	case status < 200 || status >= 300:
		return uuid.Nil, fmt.Errorf("%w: username lookup: HTTP %d", errServerStatus, status)
	}

	var ident struct {
		UUID uuid.UUID `json:"uuid"`
	}

	err := json.Unmarshal(body, &ident)
	if err != nil {
		return uuid.Nil, fmt.Errorf("decode username lookup: %w", err)
	}

	if ident.UUID == uuid.Nil {
		return uuid.Nil, ErrNotOnSignal
	}

	return ident.UUID, nil
}
