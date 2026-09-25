//go:build cgo

package signal

/*
#include <stddef.h>
#include <stdint.h>

// Declarations copied from libsignal's signal_ffi.h, limited to what HPKE needs; libsignalgo
// links the library but doesn't wrap these calls.
typedef struct SignalFfiError SignalFfiError;
typedef struct SignalPrivateKey SignalPrivateKey;
typedef struct SignalPublicKey SignalPublicKey;
typedef const int8_t* SignalCStringPtr;
typedef struct { const uint8_t* base; size_t length; } SignalBorrowedBuffer;
typedef struct { uint8_t* base; size_t length; } SignalOwnedBuffer;
typedef struct { SignalPrivateKey* raw; } SignalMutPointerPrivateKey;
typedef struct { const SignalPrivateKey* raw; } SignalConstPointerPrivateKey;
typedef struct { SignalPublicKey* raw; } SignalMutPointerPublicKey;
typedef struct { const SignalPublicKey* raw; } SignalConstPointerPublicKey;

SignalFfiError* signal_privatekey_deserialize(SignalMutPointerPrivateKey* out, SignalBorrowedBuffer data);
SignalFfiError* signal_privatekey_destroy(SignalMutPointerPrivateKey p);
SignalFfiError* signal_privatekey_hpke_open(SignalOwnedBuffer* out, SignalConstPointerPrivateKey sk,
	SignalBorrowedBuffer ciphertext, SignalBorrowedBuffer info, SignalBorrowedBuffer associated_data);
SignalFfiError* signal_publickey_deserialize(SignalMutPointerPublicKey* out, SignalBorrowedBuffer data);
SignalFfiError* signal_publickey_destroy(SignalMutPointerPublicKey p);
SignalFfiError* signal_publickey_hpke_seal(SignalOwnedBuffer* out, SignalConstPointerPublicKey pk,
	SignalBorrowedBuffer plaintext, SignalBorrowedBuffer info, SignalBorrowedBuffer associated_data);
SignalFfiError* signal_error_get_message(SignalCStringPtr* out, const SignalFfiError* err);
void signal_error_free(SignalFfiError* err);
void signal_free_buffer(const uint8_t* buf, size_t buf_len);
void signal_free_string(const int8_t* buf);
*/
import "C"

import (
	"errors"
	"fmt"
	"unsafe"
)

var errHPKE = errors.New("libsignal hpke")

// hpkeOpen decrypts ciphertext sealed to the serialized private key (libsignal's
// PrivateKey.open).
func hpkeOpen(privateKey, ciphertext, info, associatedData []byte) ([]byte, error) {
	var key C.SignalMutPointerPrivateKey

	err := ffiError(C.signal_privatekey_deserialize(&key, borrow(privateKey)))
	if err != nil {
		return nil, fmt.Errorf("deserialize private key: %w", err)
	}

	defer C.signal_privatekey_destroy(key)

	var out C.SignalOwnedBuffer

	err = ffiError(C.signal_privatekey_hpke_open(&out, C.SignalConstPointerPrivateKey(key),
		borrow(ciphertext), borrow(info), borrow(associatedData)))
	if err != nil {
		return nil, fmt.Errorf("open: %w", err)
	}

	return own(out), nil
}

// hpkeSeal encrypts plaintext to the serialized public key (libsignal's PublicKey.seal). The
// server does this for us; tests use it to produce input for hpkeOpen.
func hpkeSeal(publicKey, plaintext, info, associatedData []byte) ([]byte, error) {
	var key C.SignalMutPointerPublicKey

	err := ffiError(C.signal_publickey_deserialize(&key, borrow(publicKey)))
	if err != nil {
		return nil, fmt.Errorf("deserialize public key: %w", err)
	}

	defer C.signal_publickey_destroy(key)

	var out C.SignalOwnedBuffer

	err = ffiError(C.signal_publickey_hpke_seal(&out, C.SignalConstPointerPublicKey(key),
		borrow(plaintext), borrow(info), borrow(associatedData)))
	if err != nil {
		return nil, fmt.Errorf("seal: %w", err)
	}

	return own(out), nil
}

// borrow passes data to libsignal without copying. cgo pins it for the duration of the call.
func borrow(data []byte) C.SignalBorrowedBuffer {
	if len(data) == 0 {
		return C.SignalBorrowedBuffer{}
	}

	return C.SignalBorrowedBuffer{
		base:   (*C.uint8_t)(unsafe.Pointer(&data[0])),
		length: C.size_t(len(data)),
	}
}

// own copies a buffer returned by libsignal into Go memory and frees it.
func own(buf C.SignalOwnedBuffer) []byte {
	defer C.signal_free_buffer(buf.base, buf.length)

	return C.GoBytes(unsafe.Pointer(buf.base), C.int(buf.length))
}

// ffiError converts and frees a libsignal error.
func ffiError(ffiErr *C.SignalFfiError) error {
	if ffiErr == nil {
		return nil
	}

	defer C.signal_error_free(ffiErr)

	var msg C.SignalCStringPtr
	if C.signal_error_get_message(&msg, ffiErr) != nil || msg == nil {
		return errHPKE
	}

	defer C.signal_free_string(msg)

	return fmt.Errorf("%w: %s", errHPKE, C.GoString((*C.char)(unsafe.Pointer(msg))))
}
