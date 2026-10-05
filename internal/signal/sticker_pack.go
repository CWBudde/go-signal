package signal

// StickerPack is a complete account-local pack, including verified image bytes.
// Reference contains the secret key; renderers must only expose PackID.
type StickerPack struct {
	Reference StickerReference
	Title     string
	Author    string
	CoverID   *uint32
	Stickers  []StickerPackItem
}

// StickerPackItem holds a manifest item and its verified image.
type StickerPackItem struct {
	ID   uint32
	Data StickerData
}

const (
	// MaxStickerPackItems bounds installation work, including a cover-only item.
	MaxStickerPackItems   = 200
	stickerPackImageLimit = 100 << 20
	// IV, MAC and at most one padding block.
	stickerCipherOverhead = 16 + 32 + 16
)

// Check validates a complete, bounded cache before storage or use.
func (pack StickerPack) Check() error {
	err := pack.Reference.Check()
	if err != nil {
		return err
	}

	if len(pack.Stickers) == 0 || len(pack.Stickers) > MaxStickerPackItems {
		return ErrInvalidSticker
	}

	seen := make(map[uint32]bool)
	total := int64(0)

	for _, item := range pack.Stickers {
		if seen[item.ID] || !validStickerData(item.Data) {
			return ErrInvalidSticker
		}

		seen[item.ID] = true

		total += int64(len(item.Data.Image.Data))
		if total > stickerPackImageLimit {
			return ErrStickerTooLarge
		}
	}

	if pack.CoverID != nil && !seen[*pack.CoverID] {
		return ErrInvalidSticker
	}

	return nil
}

func validStickerData(data StickerData) bool {
	return len(data.Image.Data) > 0 && stickerMIME(data.Image.ContentType)
}

func stickerMIME(mime string) bool {
	return mime == "image/webp" || mime == "image/png" || mime == "image/apng" || mime == "image/gif"
}
