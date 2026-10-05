package signaltest

import (
	"bytes"
	"context"
	"fmt"
	"slices"
	"sort"

	"github.com/cwbudde/go-signal/internal/signal"
)

func (c *client) InstallStickerPack(ctx context.Context, ref signal.StickerReference) (signal.StickerPack, error) {
	c.fake.mu.Lock()
	defer c.fake.mu.Unlock()

	if c.closed {
		return signal.StickerPack{}, signal.ErrClosed
	}

	err := ctx.Err()
	if err != nil {
		return signal.StickerPack{}, fmt.Errorf("install sticker pack: %w", err)
	}

	err = ref.Check()
	if err != nil {
		return signal.StickerPack{}, fmt.Errorf("install sticker pack: %w", err)
	}

	acc, err := c.fake.account(c.opts)
	if err != nil {
		return signal.StickerPack{}, err
	}

	for _, pack := range c.fake.installedStickerPacks[acc.ACI] {
		if sameFakeStickerPack(pack, ref) {
			return cloneStickerPack(pack), nil
		}
	}

	if c.fake.InstallStickerPackErr != nil {
		return signal.StickerPack{}, c.fake.InstallStickerPackErr
	}

	pack, err := c.stickerPackFixture(ref)
	if err != nil {
		return signal.StickerPack{}, err
	}

	if c.fake.installedStickerPacks == nil {
		c.fake.installedStickerPacks = make(map[string][]signal.StickerPack)
	}

	packs := c.fake.installedStickerPacks[acc.ACI]
	packs = slices.DeleteFunc(packs, func(p signal.StickerPack) bool { return p.Reference.PackID == ref.PackID })
	c.fake.installedStickerPacks[acc.ACI] = append(packs, cloneStickerPack(pack))

	return cloneStickerPack(pack), nil
}

func (c *client) StickerPacks(ctx context.Context) ([]signal.StickerPack, error) {
	c.fake.mu.Lock()
	defer c.fake.mu.Unlock()

	if c.closed {
		return nil, signal.ErrClosed
	}

	err := ctx.Err()
	if err != nil {
		return nil, fmt.Errorf("list sticker packs: %w", err)
	}

	acc, err := c.fake.account(c.opts)
	if err != nil {
		return nil, err
	}

	packs := make([]signal.StickerPack, 0, len(c.fake.installedStickerPacks[acc.ACI]))
	for _, pack := range c.fake.installedStickerPacks[acc.ACI] {
		packs = append(packs, cloneStickerPack(pack))
	}

	sort.Slice(packs, func(i, j int) bool { return packs[i].Reference.PackID < packs[j].Reference.PackID })

	return packs, nil
}

func cloneStickerPack(pack signal.StickerPack) signal.StickerPack {
	pack.Reference.PackKey = slices.Clone(pack.Reference.PackKey)
	if pack.CoverID != nil {
		pack.CoverID = new(*pack.CoverID)
	}

	pack.Stickers = slices.Clone(pack.Stickers)
	for i := range pack.Stickers {
		pack.Stickers[i].Data.Image.Data = slices.Clone(pack.Stickers[i].Data.Image.Data)
	}

	return pack
}

func sameFakeStickerPack(pack signal.StickerPack, ref signal.StickerReference) bool {
	return pack.Reference.PackID == ref.PackID && sameFakeStickerKey(pack.Reference, ref)
}

func sameFakeStickerKey(a, b signal.StickerReference) bool { return bytes.Equal(a.PackKey, b.PackKey) }

func (c *client) stickerPackFixture(ref signal.StickerReference) (signal.StickerPack, error) {
	pack, ok := c.fake.StickerPackFixtures[ref.PackID]
	if !ok {
		return signal.StickerPack{}, signal.ErrStickerNotFound
	}

	if !sameFakeStickerKey(pack.Reference, ref) {
		return signal.StickerPack{}, signal.ErrInvalidSticker
	}

	err := pack.Check()
	if err != nil {
		return signal.StickerPack{}, fmt.Errorf("install sticker pack: %w", err)
	}

	return pack, nil
}
