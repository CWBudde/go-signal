package app

import (
	"context"
	"fmt"

	"github.com/cwbudde/go-signal/internal/signal"
)

// StickerPackInstall installs a complete pack locally without changing phone installation state.
func (a *App) StickerPackInstall(ctx context.Context, link string) (signal.StickerPack, error) {
	ref, err := ParseStickerPackURL(link)
	if err != nil {
		return signal.StickerPack{}, fmt.Errorf("install sticker pack: %w", err)
	}

	pack, err := a.client.InstallStickerPack(ctx, ref)
	if err != nil {
		return signal.StickerPack{}, fmt.Errorf("install sticker pack: %w", err)
	}

	return pack, nil
}

// StickerPacks lists the selected account's installed packs offline.
func (a *App) StickerPacks(ctx context.Context) ([]signal.StickerPack, error) {
	packs, err := a.client.StickerPacks(ctx)
	if err != nil {
		return nil, fmt.Errorf("list sticker packs: %w", err)
	}

	return packs, nil
}
