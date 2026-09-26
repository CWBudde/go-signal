//go:build cgo && !purego

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
	"fmt"
	"unsafe"
)

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
