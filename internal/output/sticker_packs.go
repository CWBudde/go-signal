package output

import (
	"fmt"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/cwbudde/go-signal/internal/signal"
)

// StickerPackItemJSON exposes metadata without cached image bytes.
type StickerPackItemJSON struct {
	ID          uint32 `json:"id"`
	Emoji       string `json:"emoji,omitempty"`
	ContentType string `json:"contentType"`
	Size        int    `json:"size"`
}

// StickerPackJSON deliberately omits the secret pack key.
type StickerPackJSON struct {
	ID       string                `json:"id"`
	Title    string                `json:"title"`
	Author   string                `json:"author"`
	CoverID  *uint32               `json:"coverId,omitempty"`
	Stickers []StickerPackItemJSON `json:"stickers"`
}

// StickerPacksJSON is the account-local installation/listing output.
type StickerPacksJSON struct {
	Version int               `json:"version"`
	Packs   []StickerPackJSON `json:"stickerPacks"`
}

// NewStickerPacksJSON projects public metadata from private cache records.
func NewStickerPacksJSON(packs []signal.StickerPack) StickerPacksJSON {
	out := StickerPacksJSON{
		Version: SchemaVersion,
		Packs:   make([]StickerPackJSON, 0, len(packs)),
	}
	for _, pack := range packs {
		metadata := StickerPackJSON{
			ID:       pack.Reference.PackID,
			Title:    pack.Title,
			Author:   pack.Author,
			Stickers: make([]StickerPackItemJSON, 0, len(pack.Stickers)),
		}
		if pack.CoverID != nil {
			metadata.CoverID = new(*pack.CoverID)
		}

		for _, item := range pack.Stickers {
			metadata.Stickers = append(metadata.Stickers, StickerPackItemJSON{
				ID:          item.ID,
				Emoji:       item.Data.Emoji,
				ContentType: item.Data.Image.ContentType,
				Size:        len(item.Data.Image.Data),
			})
		}

		out.Packs = append(out.Packs, metadata)
	}

	return out
}

// StickerPacks prints installed metadata, including selectable sticker IDs.
func (p *Printer) StickerPacks(packs []signal.StickerPack) error {
	out := NewStickerPacksJSON(packs)
	if p.format == JSON {
		return p.writeJSON(out)
	}

	table := tabwriter.NewWriter(p.w, 0, 0, columnGap, ' ', 0)

	_, err := fmt.Fprintln(table, "ID\tTITLE\tAUTHOR\tCOVER\tSTICKERS")
	if err != nil {
		return fmt.Errorf("write sticker packs: %w", err)
	}

	for _, pack := range out.Packs {
		cover := "-"
		if pack.CoverID != nil {
			cover = strconv.FormatUint(uint64(*pack.CoverID), 10)
		}

		ids := make([]string, 0, len(pack.Stickers))
		for _, item := range pack.Stickers {
			ids = append(ids, strconv.FormatUint(uint64(item.ID), 10))
		}

		_, err := fmt.Fprintf(table, "%s\t%s\t%s\t%s\t%s\n",
			pack.ID, oneLine(pack.Title), oneLine(pack.Author), cover, strings.Join(ids, ", "))
		if err != nil {
			return fmt.Errorf("write sticker pack: %w", err)
		}
	}

	err = table.Flush()
	if err != nil {
		return fmt.Errorf("flush sticker packs: %w", err)
	}

	return nil
}
