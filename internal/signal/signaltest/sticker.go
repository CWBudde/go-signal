package signaltest

import (
	"context"
	"fmt"
	"slices"

	"github.com/cwbudde/go-signal/internal/signal"
)

func (c *client) FetchSticker(ctx context.Context, ref signal.StickerReference) (signal.StickerData, error) {
	c.fake.mu.Lock()
	defer c.fake.mu.Unlock()

	if c.closed {
		return signal.StickerData{}, signal.ErrClosed
	}

	err := ctx.Err()
	if err != nil {
		return signal.StickerData{}, fmt.Errorf("fetch sticker: %w", err)
	}

	err = ref.Check()
	if err != nil {
		return signal.StickerData{}, fmt.Errorf("fetch sticker: %w", err)
	}

	data, found, err := c.cachedSticker(ref)
	if found || err != nil {
		return data, err
	}

	ref.PackKey = slices.Clone(ref.PackKey)

	c.fake.fetchedStickers = append(c.fake.fetchedStickers, ref)
	if c.fake.FetchStickerErr != nil {
		return signal.StickerData{}, c.fake.FetchStickerErr
	}

	data, ok := c.fake.Stickers[fmt.Sprintf("%s:%d", ref.PackID, ref.StickerID)]
	if !ok {
		return signal.StickerData{}, signal.ErrStickerNotFound
	}

	data.Image.Data = slices.Clone(data.Image.Data)

	return data, nil
}

// FetchedStickers returns references requested by FetchSticker.
func (f *Fake) FetchedStickers() []signal.StickerReference {
	f.mu.Lock()
	defer f.mu.Unlock()

	out := slices.Clone(f.fetchedStickers)
	for i := range out {
		out[i].PackKey = slices.Clone(out[i].PackKey)
	}

	return out
}

func (c *client) cachedSticker(ref signal.StickerReference) (signal.StickerData, bool, error) {
	acc, err := c.fake.account(c.opts)
	if err != nil {
		return signal.StickerData{}, false, nil //nolint:nilerr // standalone CDN fixtures need no account.
	}

	for _, pack := range c.fake.installedStickerPacks[acc.ACI] {
		if pack.Reference.PackID != ref.PackID || !sameFakeStickerKey(pack.Reference, ref) {
			continue
		}

		for _, item := range pack.Stickers {
			if item.ID == ref.StickerID {
				data := item.Data
				data.Image.Data = slices.Clone(data.Image.Data)

				return data, true, nil
			}
		}

		return signal.StickerData{}, false, signal.ErrStickerNotFound
	}

	return signal.StickerData{}, false, nil
}
