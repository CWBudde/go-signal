//go:build cgo || libsignal_go

package signal

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/cwbudde/mautrix-signal/pkg/signalmeow/protobuf/signalpb"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow/web"
	"google.golang.org/protobuf/proto"
)

const (
	stickerFetchTimeout   = 2 * time.Minute
	stickerManifestLimit  = 1 << 20
	stickerImageLimit     = 100 << 20
	stickerKeySize        = 32
	stickerDerivedKeySize = 64
)

func (c *meowClient) FetchSticker(ctx context.Context, ref StickerReference) (StickerData, error) {
	if !c.begin(&c.sending) {
		return StickerData{}, ErrClosed
	}
	defer c.sending.Done()

	client := *web.SignalHTTPClient
	client.Timeout = stickerFetchTimeout
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }

	return fetchSticker(ctx, ref, &client)
}

func fetchSticker(ctx context.Context, ref StickerReference, client *http.Client) (StickerData, error) {
	err := ref.Check()
	if err != nil {
		return StickerData{}, err
	}

	manifestPath := "/stickers/" + ref.PackID + "/manifest.proto"

	manifest, err := fetchStickerBlob(ctx, client, manifestPath, ref.PackKey, stickerManifestLimit)
	if err != nil {
		return StickerData{}, err
	}

	var pack signalpb.Pack

	err = proto.Unmarshal(manifest, &pack)
	if err != nil {
		return StickerData{}, ErrInvalidSticker
	}

	selected, err := selectSticker(&pack, ref.StickerID)
	if err != nil {
		return StickerData{}, err
	}

	mime := selected.GetContentType()
	if mime != "" && !supportedStickerMIME(mime) {
		return StickerData{}, ErrInvalidSticker
	}

	path := fmt.Sprintf("/stickers/%s/full/%d", ref.PackID, ref.StickerID)

	image, err := fetchStickerBlob(ctx, client, path, ref.PackKey, stickerImageLimit)
	if err != nil {
		return StickerData{}, err
	}

	if mime == "" {
		mime = http.DetectContentType(image)
		if !supportedStickerMIME(mime) {
			return StickerData{}, ErrInvalidSticker
		}
	}

	return StickerData{Emoji: selected.GetEmoji(), Image: OutgoingAttachment{Data: image, ContentType: mime}}, nil
}

func supportedStickerMIME(mime string) bool {
	return mime == "image/webp" || mime == "image/png" || mime == "image/apng" || mime == "image/gif"
}

func fetchStickerBlob(ctx context.Context, client *http.Client, path string, key []byte, limit int64) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://cdn.signal.org"+path, nil)
	if err != nil {
		return nil, fmt.Errorf("sticker request: %w", err)
	}

	request.Header.Set("User-Agent", web.UserAgent)
	request.Header.Set("X-Signal-Agent", web.SignalAgent)

	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("sticker request: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode == http.StatusNotFound {
		return nil, ErrStickerNotFound
	}

	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: HTTP status %d", ErrInvalidSticker, response.StatusCode)
	}

	if response.ContentLength > limit {
		return nil, ErrStickerTooLarge
	}

	blob, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("sticker read: %w", err)
	}

	if int64(len(blob)) > limit {
		return nil, ErrStickerTooLarge
	}

	return decryptStickerBlob(key, blob)
}

func decryptStickerBlob(key, blob []byte) ([]byte, error) {
	if len(key) != stickerKeySize || len(blob) < aes.BlockSize*2+sha256.Size ||
		(len(blob)-sha256.Size-aes.BlockSize)%aes.BlockSize != 0 {
		return nil, ErrInvalidSticker
	}

	keys, err := hkdf.Key(sha256.New, key, make([]byte, stickerKeySize), "Sticker Pack", stickerDerivedKeySize)
	if err != nil {
		return nil, ErrInvalidSticker
	}

	ciphertext := blob[:len(blob)-sha256.Size]
	mac := hmac.New(sha256.New, keys[32:])
	mac.Write(ciphertext)

	if !hmac.Equal(mac.Sum(nil), blob[len(blob)-sha256.Size:]) {
		return nil, ErrInvalidSticker
	}

	block, err := aes.NewCipher(keys[:32])
	if err != nil {
		return nil, ErrInvalidSticker
	}

	plain := make([]byte, len(ciphertext)-aes.BlockSize)
	cipher.NewCBCDecrypter(block, ciphertext[:aes.BlockSize]).CryptBlocks(plain, ciphertext[aes.BlockSize:])

	return unpadSticker(plain)
}

func unpadSticker(plain []byte) ([]byte, error) {
	padding := int(plain[len(plain)-1])
	if padding < 1 || padding > aes.BlockSize || padding > len(plain) {
		return nil, ErrInvalidSticker
	}

	for _, value := range plain[len(plain)-padding:] {
		if int(value) != padding {
			return nil, ErrInvalidSticker
		}
	}

	return plain[:len(plain)-padding], nil
}

func selectSticker(pack *signalpb.Pack, id uint32) (*signalpb.Pack_Sticker, error) {
	var selected *signalpb.Pack_Sticker

	for _, item := range pack.GetStickers() {
		if item == nil || item.Id == nil || item.GetId() != id {
			continue
		}

		if selected != nil {
			return nil, ErrInvalidSticker
		}

		selected = item
	}

	if selected == nil {
		return nil, ErrStickerNotFound
	}

	return selected, nil
}
