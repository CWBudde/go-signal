package signal

import (
	"encoding/hex"
	"errors"
	"strings"
)

var (
	// ErrInvalidSticker means the reference, image or content combination is invalid.
	ErrInvalidSticker = errors.New("invalid sticker")
	// ErrStickerNotFound means the pack or requested sticker does not exist.
	ErrStickerNotFound = errors.New("sticker not found")
	// ErrStickerTooLarge means a sticker response exceeds the allowed size.
	ErrStickerTooLarge = errors.New("sticker response is too large")
)

// StickerReference identifies one item in an existing Signal sticker pack.
type StickerReference struct {
	PackID    string
	PackKey   []byte
	StickerID uint32
}

// StickerData contains a verified image fetched from a sticker pack.
type StickerData struct {
	Emoji string
	Image OutgoingAttachment
}

// OutgoingSticker refers to an image uploaded on the client that sends it.
type OutgoingSticker struct {
	Reference StickerReference
	Emoji     string
	Image     UploadedAttachment
}

// Check validates canonical pack identity and secret key length.
func (ref StickerReference) Check() error {
	if len(ref.PackID) != 32 || len(ref.PackKey) != 32 || strings.ToLower(ref.PackID) != ref.PackID {
		return ErrInvalidSticker
	}

	_, err := hex.DecodeString(ref.PackID)
	if err != nil {
		return ErrInvalidSticker
	}

	return nil
}
