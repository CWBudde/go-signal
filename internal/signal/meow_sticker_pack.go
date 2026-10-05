//go:build cgo || libsignal_go

package signal

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/cwbudde/go-signal/internal/store"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow/protobuf/signalpb"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow/web"
	"google.golang.org/protobuf/proto"
)

func stickerHTTPClient() *http.Client {
	client := *web.SignalHTTPClient
	client.Timeout = stickerFetchTimeout
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }

	return &client
}

func (c *meowClient) InstallStickerPack(ctx context.Context, ref StickerReference) (StickerPack, error) {
	err := ref.Check()
	if err != nil {
		return StickerPack{}, err
	}

	var pack StickerPack

	err = c.withStore(ctx, func(data *store.Store) error {
		old, found, err := cachedStickerPack(ctx, data, ref.PackID)
		if err != nil {
			return err
		}

		if found && sameStickerKey(old.Reference, ref) {
			pack = old
			return nil
		}

		pack, err = fetchStickerPack(ctx, ref, stickerHTTPClient())
		if err != nil {
			return err
		}

		blob, err := json.Marshal(pack)
		if err != nil {
			return fmt.Errorf("encode sticker pack: %w", err)
		}

		return data.PutStickerPack(ctx, ref.PackID, blob)
	})
	if err != nil {
		return StickerPack{}, fmt.Errorf("install sticker pack: %w", err)
	}

	return pack, nil
}

func (c *meowClient) StickerPacks(ctx context.Context) ([]StickerPack, error) {
	packs := make([]StickerPack, 0)

	err := c.withStore(ctx, func(data *store.Store) error {
		ids, err := data.StickerPackIDs(ctx)
		if err != nil {
			return fmt.Errorf("list cached sticker packs: %w", err)
		}

		for _, id := range ids {
			pack, _, err := cachedStickerPack(ctx, data, id)
			if err != nil {
				return err
			}

			packs = append(packs, pack)
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("list sticker packs: %w", err)
	}

	return packs, nil
}

func (c *meowClient) cachedSticker(ctx context.Context, ref StickerReference) (StickerData, bool, error) {
	var (
		out   StickerData
		found bool
	)

	err := c.withStore(ctx, func(data *store.Store) error {
		pack, exists, err := cachedStickerPack(ctx, data, ref.PackID)
		if err != nil {
			return err
		}

		if !exists || !sameStickerKey(pack.Reference, ref) {
			return nil
		}

		for _, item := range pack.Stickers {
			if item.ID == ref.StickerID {
				out = item.Data
				found = true

				return nil
			}
		}

		return ErrStickerNotFound
	})
	if errors.Is(err, ErrNotLinked) {
		return StickerData{}, false, nil
	}

	return out, found, err
}

func sameStickerKey(a, b StickerReference) bool {
	return subtle.ConstantTimeCompare(a.PackKey, b.PackKey) == 1
}

func cachedStickerPack(ctx context.Context, data *store.Store, packID string) (StickerPack, bool, error) {
	blob, found, err := data.StickerPack(ctx, packID)
	if err != nil {
		return StickerPack{}, false, fmt.Errorf("read cached sticker pack: %w", err)
	}

	if !found {
		return StickerPack{}, false, nil
	}

	var pack StickerPack
	if json.Unmarshal(blob, &pack) != nil || pack.Reference.PackID != packID {
		return StickerPack{}, false, ErrInvalidSticker
	}

	err = pack.Check()
	if err != nil {
		return StickerPack{}, false, err
	}

	return pack, true, nil
}

func fetchStickerPack(ctx context.Context, ref StickerReference, client *http.Client) (StickerPack, error) {
	path := "/stickers/" + ref.PackID + "/manifest.proto"

	blob, err := fetchStickerBlob(ctx, client, path, ref.PackKey, stickerManifestLimit)
	if err != nil {
		return StickerPack{}, err
	}

	var manifest signalpb.Pack
	if proto.Unmarshal(blob, &manifest) != nil {
		return StickerPack{}, ErrInvalidSticker
	}

	items, err := stickerPackItems(&manifest)
	if err != nil {
		return StickerPack{}, err
	}

	ref.PackKey = append([]byte(nil), ref.PackKey...)
	ref.StickerID = 0

	pack := StickerPack{
		Reference: ref,
		Title:     manifest.GetTitle(),
		Author:    manifest.GetAuthor(),
	}
	if manifest.GetCover() != nil {
		pack.CoverID = new(manifest.GetCover().GetId())
	}

	remaining := int64(stickerPackImageLimit)
	for _, item := range items {
		limit := min(remaining+stickerCipherOverhead, stickerImageLimit)

		data, err := fetchStickerImage(ctx, client, ref, item, limit)
		if err != nil {
			return StickerPack{}, err
		}

		remaining -= int64(len(data.Image.Data))
		if remaining < 0 {
			return StickerPack{}, ErrStickerTooLarge
		}

		pack.Stickers = append(pack.Stickers, StickerPackItem{ID: item.GetId(), Data: data})
	}

	return pack, pack.Check()
}

func stickerPackItems(pack *signalpb.Pack) ([]*signalpb.Pack_Sticker, error) {
	if len(pack.GetStickers()) == 0 || len(pack.GetStickers()) > MaxStickerPackItems {
		return nil, ErrInvalidSticker
	}

	items := append([]*signalpb.Pack_Sticker(nil), pack.GetStickers()...)

	seen := make(map[uint32]bool)
	for _, item := range items {
		if !validStickerManifestItem(item) || seen[item.GetId()] {
			return nil, ErrInvalidSticker
		}

		seen[item.GetId()] = true
	}

	if cover := pack.GetCover(); cover != nil {
		if !validStickerManifestItem(cover) {
			return nil, ErrInvalidSticker
		}

		if !seen[cover.GetId()] {
			items = append(items, cover)
		}
	}

	if len(items) > MaxStickerPackItems {
		return nil, ErrStickerTooLarge
	}

	return items, nil
}

func fetchStickerImage(
	ctx context.Context, client *http.Client, ref StickerReference, item *signalpb.Pack_Sticker, limit int64,
) (StickerData, error) {
	path := fmt.Sprintf("/stickers/%s/full/%d", ref.PackID, item.GetId())

	image, err := fetchStickerBlob(ctx, client, path, ref.PackKey, limit)
	if err != nil {
		return StickerData{}, err
	}

	mime := item.GetContentType()
	if mime == "" {
		mime = http.DetectContentType(image)
	}

	if !supportedStickerMIME(mime) || len(image) == 0 {
		return StickerData{}, ErrInvalidSticker
	}

	return StickerData{
		Emoji: item.GetEmoji(),
		Image: OutgoingAttachment{Data: image, ContentType: mime},
	}, nil
}

func validStickerManifestItem(item *signalpb.Pack_Sticker) bool {
	return item != nil && item.Id != nil && (item.GetContentType() == "" || supportedStickerMIME(item.GetContentType()))
}
