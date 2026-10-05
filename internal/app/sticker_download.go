package app

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/cwbudde/go-signal/internal/signal"
)

func (a *App) stickerData(ctx context.Context, sticker *signal.Sticker) (signal.StickerData, bool, error) {
	ref := signal.StickerReference{
		PackID:    sticker.PackID,
		PackKey:   sticker.PackKey,
		StickerID: sticker.StickerID,
	}
	if sticker.Image != nil {
		data, err := a.client.Download(ctx, *sticker.Image)
		if err == nil {
			return signal.StickerData{
				Emoji: sticker.Emoji,
				Image: signal.OutgoingAttachment{Data: data, ContentType: sticker.Image.ContentType},
			}, false, nil
		}

		if !errors.Is(err, signal.ErrAttachmentNotFound) || ref.Check() != nil {
			return signal.StickerData{}, false, err //nolint:wrapcheck // preserve the existing per-image error.
		}
	}

	err := ref.Check()
	if err != nil {
		return signal.StickerData{}, true, fmt.Errorf("sticker fallback unavailable: %w", err)
	}

	data, err := a.client.FetchSticker(ctx, ref)
	if err != nil {
		return signal.StickerData{}, true, fmt.Errorf("sticker fallback: %w", err)
	}

	return data, true, nil
}

func (a *App) saveSticker(ctx context.Context, req SaveAttachmentsRequest) (AttachmentResult, error) {
	if req.Message.Sticker == nil {
		return AttachmentResult{}, ErrNoAttachment
	}

	err := PrepareDownloadDir(req.Dir)
	if err != nil {
		return AttachmentResult{}, err
	}

	data, fallback, err := a.stickerData(ctx, req.Message.Sticker)
	if err != nil {
		return AttachmentResult{}, err
	}

	name := strconv.FormatUint(req.Message.Timestamp, 10) + "-sticker-" +
		strconv.FormatUint(uint64(req.Message.Sticker.StickerID), 10) + extension(data.Image.ContentType)

	path, err := writeNewFile(req.Dir, name, data.Image.Data)
	if err != nil {
		return AttachmentResult{}, fmt.Errorf("save sticker: %w", err)
	}

	metadata := signal.Attachment{
		ContentType: data.Image.ContentType,
		Size:        uint32(len(data.Image.Data)),
	}
	if !fallback && req.Message.Sticker.Image != nil {
		metadata.Filename = req.Message.Sticker.Image.Filename
		metadata.Caption = req.Message.Sticker.Image.Caption
	}

	return AttachmentResult{Attachment: metadata, Path: path, Data: data.Image.Data}, nil
}

// Sticker saves a received sticker without marking its message read or sending receipts.
func (i *Inbox) Sticker(ctx context.Context, id, dir string) (AttachmentResult, error) {
	msg, err := i.Message(ctx, id)
	if err != nil {
		return AttachmentResult{}, err
	}

	return i.app.saveSticker(ctx, SaveAttachmentsRequest{Dir: dir, Message: msg})
}
