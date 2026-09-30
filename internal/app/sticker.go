package app

import (
	"context"
	"encoding/hex"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/cwbudde/go-signal/internal/signal"
)

// ParseStickerPackURL parses a HTTPS signal.art sticker pack link without exposing keys in errors.
//
//nolint:cyclop // each guard rejects a distinct unsafe link component
func ParseStickerPackURL(raw string) (signal.StickerReference, error) {
	link, err := url.Parse(raw)
	if err != nil || link.Scheme != "https" || !strings.EqualFold(link.Hostname(), "signal.art") ||
		link.User != nil || (link.Port() != "" && link.Port() != "443") || link.Path != "/addstickers/" {
		return signal.StickerReference{}, signal.ErrInvalidSticker
	}

	fragment, err := url.ParseQuery(link.Fragment)
	if err != nil || len(fragment["pack_id"]) != 1 || len(fragment["pack_key"]) != 1 {
		return signal.StickerReference{}, signal.ErrInvalidSticker
	}

	packID, err := hex.DecodeString(fragment.Get("pack_id"))
	if err != nil || len(packID) != 16 {
		return signal.StickerReference{}, signal.ErrInvalidSticker
	}

	key, err := hex.DecodeString(fragment.Get("pack_key"))
	if err != nil || len(key) != 32 {
		return signal.StickerReference{}, signal.ErrInvalidSticker
	}

	return signal.StickerReference{PackID: hex.EncodeToString(packID), PackKey: key}, nil
}

func checkStickerRequest(req SendRequest) error {
	if req.Sticker == nil {
		return nil
	}

	if req.Body != "" || len(req.Attachments) != 0 || req.Quote != "" || req.QuoteText != "" || req.EditTarget != 0 {
		return signal.ErrInvalidSticker
	}

	return req.Sticker.Check() //nolint:wrapcheck // Send adds context without exposing the reference
}

func (a *App) buildSticker(ctx context.Context, ref signal.StickerReference) (content, error) {
	data, err := a.client.FetchSticker(ctx, ref)
	if err != nil {
		return content{}, fmt.Errorf("fetch sticker: %w", err)
	}

	uploaded, err := a.client.Upload(ctx, []signal.OutgoingAttachment{data.Image})
	if err != nil {
		return content{}, fmt.Errorf("upload sticker: %w", err)
	}

	if len(uploaded) != 1 {
		return content{}, fmt.Errorf("upload sticker: %w", errNoResult)
	}

	return content{sticker: &signal.OutgoingSticker{Reference: ref, Emoji: data.Emoji, Image: uploaded[0]}}, nil
}

// SaveMessageMedia saves ordinary attachments and the embedded sticker image separately.
func (a *App) SaveMessageMedia(ctx context.Context, req SaveAttachmentsRequest) MessageMediaResult {
	out := MessageMediaResult{Attachments: a.SaveAttachments(ctx, req)}

	sticker := req.Message.Sticker
	if sticker == nil || sticker.Image == nil {
		return out
	}

	out.Sticker = &SavedAttachment{}

	err := PrepareDownloadDir(req.Dir)
	if err != nil {
		out.Sticker.Err = err
		return out
	}

	name := strconv.FormatUint(req.Message.Timestamp, 10) + "-sticker-" +
		strconv.FormatUint(uint64(sticker.StickerID), 10) + extension(sticker.Image.ContentType)
	out.Sticker.Path, out.Sticker.Err = a.saveAttachment(ctx, req.Dir, name, *sticker.Image)

	return out
}
