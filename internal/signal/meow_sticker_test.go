//go:build cgo || libsignal_go

//nolint:lll // Dense protocol fixtures keep input and expected results together.
package signal_test

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow/protobuf/signalpb"
	"google.golang.org/protobuf/proto"
)

type stickerTransport func(*http.Request) (*http.Response, error)

func (f stickerTransport) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func stickerBlob(t *testing.T, key, data []byte) []byte {
	t.Helper()

	keys, err := hkdf.Key(sha256.New, key, make([]byte, 32), "Sticker Pack", 64)
	if err != nil {
		t.Fatal(err)
	}

	att, blob := encryptAttachment(t, data, "unused")
	// Re-encrypt using the derived keys via helper below.
	_ = att

	return encryptStickerBytes(t, keys, data, blob[:16])
}

func TestFetchStickerProtocol(t *testing.T) { //nolint:funlen // protocol failure matrix
	t.Parallel()

	ref := signal.StickerReference{PackID: strings.Repeat("ab", 16), PackKey: make([]byte, 32), StickerID: 0}
	image := []byte("GIF89aimage")
	manifest, _ := proto.Marshal(&signalpb.Pack{Stickers: []*signalpb.Pack_Sticker{{Id: new(uint32(5))}, {Id: new(uint32(0)), Emoji: new("hi"), ContentType: new(stickerGIFType)}}})

	missing, _ := proto.Marshal(&signalpb.Pack{Stickers: []*signalpb.Pack_Sticker{{Id: new(uint32(3))}}})
	duplicate, _ := proto.Marshal(&signalpb.Pack{Stickers: []*signalpb.Pack_Sticker{{Id: new(uint32(0))}, {Id: new(uint32(0))}}})
	unsupported, _ := proto.Marshal(&signalpb.Pack{Stickers: []*signalpb.Pack_Sticker{{Id: new(uint32(0)), ContentType: new("image/jpeg")}}})
	sniff, _ := proto.Marshal(&signalpb.Pack{Stickers: []*signalpb.Pack_Sticker{{Id: new(uint32(0)), Emoji: new("hi")}}})
	badMAC := stickerBlob(t, ref.PackKey, image)
	badMAC[len(badMAC)-1] ^= 1
	keys, _ := hkdf.Key(sha256.New, ref.PackKey, make([]byte, 32), "Sticker Pack", 64)
	badPadding := stickerBlob(t, ref.PackKey, image)
	badPadding[15] ^= 5
	mac := hmac.New(sha256.New, keys[32:])
	mac.Write(badPadding[:len(badPadding)-32])
	copy(badPadding[len(badPadding)-32:], mac.Sum(nil))

	for _, testCase := range []struct {
		name            string
		manifest, image []byte
		status          int
		want            error
	}{
		{"authenticated GIF", stickerBlob(t, ref.PackKey, manifest), stickerBlob(t, ref.PackKey, image), 200, nil},
		{"short", []byte{1}, nil, 200, signal.ErrInvalidSticker},
		{"misaligned", make([]byte, 65), nil, 200, signal.ErrInvalidSticker},
		{"wrong key", stickerBlob(t, bytes.Repeat([]byte{1}, 32), manifest), nil, 200, signal.ErrInvalidSticker},
		{"missing ID", stickerBlob(t, ref.PackKey, missing), nil, 200, signal.ErrStickerNotFound},
		{"duplicate ID", stickerBlob(t, ref.PackKey, duplicate), nil, 200, signal.ErrInvalidSticker},
		{"unsupported MIME", stickerBlob(t, ref.PackKey, unsupported), nil, 200, signal.ErrInvalidSticker},
		{"sniff MIME", stickerBlob(t, ref.PackKey, sniff), stickerBlob(t, ref.PackKey, image), 200, nil},
		{"bad HMAC", stickerBlob(t, ref.PackKey, manifest), badMAC, 200, signal.ErrInvalidSticker},
		{"bad padding", stickerBlob(t, ref.PackKey, manifest), badPadding, 200, signal.ErrInvalidSticker},
		{"oversize manifest", make([]byte, (1<<20)+1), nil, 200, signal.ErrStickerTooLarge},
		{"not found", nil, nil, 404, signal.ErrStickerNotFound},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			client := &http.Client{Transport: stickerTransport(func(request *http.Request) (*http.Response, error) {
				if request.URL.Host != "cdn.signal.org" {
					t.Fatalf("host %s", request.URL.Host)
				}

				data := testCase.image
				if strings.HasSuffix(request.URL.Path, "manifest.proto") {
					data = testCase.manifest
				} else if !strings.HasSuffix(request.URL.Path, "/full/0") {
					t.Fatalf("path %s", request.URL.Path)
				}

				return &http.Response{StatusCode: testCase.status, Body: io.NopCloser(bytes.NewReader(data))}, nil
			})}

			got, err := signal.FetchStickerWithHTTP(context.Background(), ref, client)
			if testCase.want != nil {
				if !errors.Is(err, testCase.want) {
					t.Fatalf("error %v", err)
				}

				return
			}

			if err != nil || !bytes.Equal(got.Image.Data, image) || got.Emoji != "hi" {
				t.Fatalf("got %+v err %v", got, err)
			}
		})
	}
}

func encryptStickerBytes(t *testing.T, keys, data, initVector []byte) []byte {
	t.Helper()

	padding := aes.BlockSize - len(data)%aes.BlockSize
	padded := append(bytes.Clone(data), bytes.Repeat([]byte{byte(padding)}, padding)...)

	block, err := aes.NewCipher(keys[:32])
	if err != nil {
		t.Fatal(err)
	}

	blob := append(bytes.Clone(initVector), make([]byte, len(padded))...)
	cipher.NewCBCEncrypter(block, initVector).CryptBlocks(blob[16:], padded)

	mac := hmac.New(sha256.New, keys[32:])
	mac.Write(blob)

	return mac.Sum(blob)
}

func TestStickerDataMessage(t *testing.T) {
	t.Parallel()

	ref := signal.StickerReference{PackID: strings.Repeat("ab", 16), PackKey: make([]byte, 32), StickerID: 0}
	pointer := &signalpb.AttachmentPointer{ContentType: new(stickerWebPType), AttachmentIdentifier: &signalpb.AttachmentPointer_CdnKey{CdnKey: "embedded"}}

	msg, err := signal.DataMessage(signal.SendRequest{Sticker: &signal.OutgoingSticker{Reference: ref, Emoji: "hi"}}, []*signalpb.AttachmentPointer{pointer}, nil)
	if err != nil {
		t.Fatal(err)
	}

	if msg.GetSticker() == nil || msg.GetSticker().GetData() != pointer || len(msg.GetAttachments()) != 0 || msg.GetSticker().GetEmoji() != "hi" {
		t.Fatalf("message %v", msg)
	}
}

func TestStickerPreservesSupportedImages(t *testing.T) {
	t.Parallel()

	ref := signal.StickerReference{PackID: strings.Repeat("ab", 16), PackKey: make([]byte, 32)}

	for _, mime := range []string{stickerWebPType, pngType, "image/apng", stickerGIFType} {
		t.Run(mime, func(t *testing.T) {
			t.Parallel()

			image := []byte("original animated bytes")
			manifest, _ := proto.Marshal(&signalpb.Pack{Stickers: []*signalpb.Pack_Sticker{{Id: new(uint32(0)), ContentType: new(mime)}}})
			client := &http.Client{Transport: stickerTransport(func(request *http.Request) (*http.Response, error) {
				data := image
				if strings.HasSuffix(request.URL.Path, "manifest.proto") {
					data = manifest
				}

				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(stickerBlob(t, ref.PackKey, data)))}, nil
			})}

			got, err := signal.FetchStickerWithHTTP(context.Background(), ref, client)
			if err != nil || got.Image.ContentType != mime || !bytes.Equal(got.Image.Data, image) {
				t.Fatalf("got %+v, %v", got, err)
			}
		})
	}
}

func TestStickerFetchCancellationAndDeclaredSize(t *testing.T) {
	t.Parallel()

	ref := signal.StickerReference{PackID: strings.Repeat("ab", 16), PackKey: make([]byte, 32)}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	client := &http.Client{Transport: stickerTransport(func(request *http.Request) (*http.Response, error) { return nil, request.Context().Err() })}

	_, err := signal.FetchStickerWithHTTP(ctx, ref, client)
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}

	client.Transport = stickerTransport(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, ContentLength: (1 << 20) + 1, Body: io.NopCloser(strings.NewReader(""))}, nil
	})

	_, err = signal.FetchStickerWithHTTP(context.Background(), ref, client)
	if !errors.Is(err, signal.ErrStickerTooLarge) {
		t.Fatal(err)
	}
}

func TestStickerRealFetchRejectsRedirect(t *testing.T) { //nolint:paralleltest // replaces the HTTP transport
	calls := 0

	t.Cleanup(signal.SetSignalTransport(stickerTransport(func(request *http.Request) (*http.Response, error) {
		calls++

		if request.URL.Host != "cdn.signal.org" {
			t.Fatal("redirect followed")
		}

		return &http.Response{StatusCode: http.StatusFound, Header: http.Header{"Location": []string{"https://example.com/image"}}, Body: io.NopCloser(strings.NewReader(""))}, nil
	})))
	client := openSeededClient(t, t.TempDir())
	ref := signal.StickerReference{PackID: strings.Repeat("ab", 16), PackKey: make([]byte, 32)}

	_, err := client.FetchSticker(t.Context(), ref)
	if !errors.Is(err, signal.ErrInvalidSticker) || calls != 1 {
		t.Fatalf("calls %d error %v", calls, err)
	}
}

func TestStickerOversizedImage(t *testing.T) {
	t.Parallel()

	ref := signal.StickerReference{PackID: strings.Repeat("ab", 16), PackKey: make([]byte, 32)}
	manifest, _ := proto.Marshal(&signalpb.Pack{Stickers: []*signalpb.Pack_Sticker{{Id: new(uint32(0)), ContentType: new(stickerGIFType)}}})
	client := &http.Client{Transport: stickerTransport(func(request *http.Request) (*http.Response, error) {
		if strings.HasSuffix(request.URL.Path, "manifest.proto") {
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(stickerBlob(t, ref.PackKey, manifest)))}, nil
		}

		return &http.Response{StatusCode: http.StatusOK, ContentLength: (100 << 20) + 1, Body: io.NopCloser(strings.NewReader(""))}, nil
	})}

	_, err := signal.FetchStickerWithHTTP(t.Context(), ref, client)
	if !errors.Is(err, signal.ErrStickerTooLarge) {
		t.Fatal(err)
	}
}

func TestStickerRealUploadOwnershipAndClones(t *testing.T) {
	t.Parallel()
	dataDir := seedAccount(t)
	client := openOffline(t, dataDir, signal.SendOnly())
	uploaded := signal.AddUpload(client, signal.OutgoingAttachment{Data: []byte("image"), ContentType: stickerWebPType})
	req := signal.SendRequest{Recipients: []signal.Recipient{{ACI: otherACI}}, Sticker: &signal.OutgoingSticker{Reference: signal.StickerReference{PackID: strings.Repeat("ab", 16), PackKey: make([]byte, 32)}, Image: uploaded}}

	build, err := signal.BuildMessage(t.Context(), client, req)
	if err != nil {
		t.Fatal(err)
	}

	first := build()
	second := build()
	first.Sticker.PackKey[0] = 1

	first.Sticker.Data.ContentType = new("changed")

	if second.GetSticker().GetPackKey()[0] != 0 || second.GetSticker().GetData().GetContentType() != stickerWebPType {
		t.Fatal("messages share mutable sticker content")
	}

	req.Sticker.Image.ID = "unknown-client-upload"

	_, err = signal.BuildMessage(t.Context(), client, req)
	if !errors.Is(err, signal.ErrUnknownAttachment) {
		t.Fatal(err)
	}
}

type stickerZeroReader struct{}

func (stickerZeroReader) Read(buffer []byte) (int, error) { clear(buffer); return len(buffer), nil }

type stickerCancelReader struct {
	done    <-chan struct{}
	err     func() error
	started chan struct{}
}

func (reader stickerCancelReader) Read(_ []byte) (int, error) {
	close(reader.started)
	<-reader.done

	return 0, fmt.Errorf("sticker body: %w", reader.err())
}

func TestStickerOversizedUnknownLengthImage(t *testing.T) { //nolint:paralleltest // bounded large response allocates 100 MiB
	ref := signal.StickerReference{PackID: strings.Repeat("ab", 16), PackKey: make([]byte, 32)}
	manifest, _ := proto.Marshal(&signalpb.Pack{Stickers: []*signalpb.Pack_Sticker{{Id: new(uint32(0)), ContentType: new(stickerGIFType)}}})
	client := &http.Client{Transport: stickerTransport(func(request *http.Request) (*http.Response, error) {
		if strings.HasSuffix(request.URL.Path, "manifest.proto") {
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(stickerBlob(t, ref.PackKey, manifest)))}, nil
		}

		return &http.Response{StatusCode: http.StatusOK, ContentLength: -1, Body: io.NopCloser(io.LimitReader(stickerZeroReader{}, (100<<20)+1))}, nil
	})}

	_, err := signal.FetchStickerWithHTTP(t.Context(), ref, client)
	if !errors.Is(err, signal.ErrStickerTooLarge) {
		t.Fatal(err)
	}
}

func TestStickerCancellationDuringRead(t *testing.T) {
	t.Parallel()

	ref := signal.StickerReference{PackID: strings.Repeat("ab", 16), PackKey: make([]byte, 32)}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	started := make(chan struct{})
	client := &http.Client{Transport: stickerTransport(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(stickerCancelReader{done: ctx.Done(), err: ctx.Err, started: started})}, nil
	})}
	done := make(chan error, 1)

	go func() { _, err := signal.FetchStickerWithHTTP(ctx, ref, client); done <- err }()

	<-started
	cancel()

	err := <-done
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
