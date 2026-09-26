//go:build cgo

package signal_test

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"net/http"
	"testing"

	"github.com/cwbudde/go-signal/internal/signal"
)

// encryptAttachment encrypts plaintext as Signal clients do before uploading it: AES-256-CBC
// with a random IV, then HMAC-SHA256 over IV and ciphertext. It returns the attachment for the
// uploaded blob with its CDN key.
func encryptAttachment(t *testing.T, plaintext []byte, cdnKey string) (signal.Attachment, []byte) {
	t.Helper()

	keys := make([]byte, 64) // AES key, then MAC key
	initVector := make([]byte, aes.BlockSize)
	_, _ = rand.Read(keys)
	_, _ = rand.Read(initVector)

	padLen := aes.BlockSize - len(plaintext)%aes.BlockSize
	padded := append(bytes.Clone(plaintext), bytes.Repeat([]byte{byte(padLen)}, padLen)...)

	block, err := aes.NewCipher(keys[:32])
	if err != nil {
		t.Fatal(err)
	}

	blob := append(bytes.Clone(initVector), make([]byte, len(padded))...)
	cipher.NewCBCEncrypter(block, initVector).CryptBlocks(blob[len(initVector):], padded)

	mac := hmac.New(sha256.New, keys[32:])
	mac.Write(blob)
	blob = mac.Sum(blob)

	digest := sha256.Sum256(blob)

	return signal.Attachment{
		ContentType: "text/plain",
		Size:        uint32(len(plaintext)), //nolint:gosec // test data is small
		Remote:      signal.RemoteAttachment{CDNKey: cdnKey, Key: keys, Digest: digest[:]},
	}, blob
}

// cdn serves blobs by CDN key; unknown keys get a 404.
func cdn(blobs map[string][]byte) http.HandlerFunc {
	return func(resp http.ResponseWriter, req *http.Request) {
		for key, blob := range blobs {
			if req.URL.Path == "/attachments/"+key {
				_, _ = resp.Write(blob)

				return
			}
		}

		resp.WriteHeader(http.StatusNotFound)
	}
}

func TestDownload(t *testing.T) { //nolint:paralleltest // replaces the HTTP transport
	plaintext := []byte("the rest of a long message")
	att, blob := encryptAttachment(t, plaintext, "good")
	tampered := bytes.Clone(blob)
	tampered[20] ^= 1

	fakeServer(t, cdn(map[string][]byte{"good": blob, "tampered": tampered}))

	client := openSeededClient(t, seedAccount(t))

	got, err := client.Download(t.Context(), att)
	if err != nil || !bytes.Equal(got, plaintext) {
		t.Errorf("Download = %q, %v; want %q", got, err, plaintext)
	}

	att.Remote.CDNKey = "tampered"

	_, err = client.Download(t.Context(), att)
	if !errors.Is(err, signal.ErrAttachmentInvalid) {
		t.Errorf("tampered: %v, want ErrAttachmentInvalid", err)
	}

	att.Remote.CDNKey = "expired"

	_, err = client.Download(t.Context(), att)
	if !errors.Is(err, signal.ErrAttachmentNotFound) {
		t.Errorf("missing: %v, want ErrAttachmentNotFound", err)
	}

	// Download checks the pointer before it asks the CDN.
	att.Remote.Digest = nil

	_, err = client.Download(t.Context(), att)
	if !errors.Is(err, signal.ErrAttachmentInvalid) {
		t.Errorf("without digest: %v, want ErrAttachmentInvalid", err)
	}
}

func TestDownloadServerError(t *testing.T) { //nolint:paralleltest // replaces the HTTP transport
	att, _ := encryptAttachment(t, []byte("x"), "key")
	fakeServer(t, func(resp http.ResponseWriter, _ *http.Request) {
		resp.WriteHeader(http.StatusBadGateway)
	})

	_, err := openSeededClient(t, seedAccount(t)).Download(t.Context(), att)
	if err == nil || errors.Is(err, signal.ErrAttachmentNotFound) || errors.Is(err, signal.ErrAttachmentInvalid) {
		t.Errorf("got %v, want a fetch error", err)
	}
}

func TestDownloadCancelled(t *testing.T) { //nolint:paralleltest // replaces the HTTP transport
	att, _ := encryptAttachment(t, []byte("x"), "slow")
	release := make(chan struct{})
	started := make(chan struct{})

	fakeServer(t, func(resp http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
		resp.WriteHeader(http.StatusNotFound)
	})
	// Registered after fakeServer, so it runs first: the abandoned transfer ends before the
	// transport is restored.
	t.Cleanup(func() { close(release) })

	ctx, cancel := context.WithCancel(t.Context())

	go func() {
		<-started
		cancel()
	}()

	// signalmeow ignores ctx; Download stops waiting for it anyway.
	_, err := openSeededClient(t, seedAccount(t)).Download(ctx, att)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("got %v, want context.Canceled", err)
	}
}
