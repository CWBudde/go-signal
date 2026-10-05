package output

import "github.com/cwbudde/go-signal/internal/app"

func applySavedSticker(sticker *stickerJSON, saved *app.SavedAttachment) {
	if sticker == nil || saved == nil {
		return
	}

	if image := saved.Image; image != nil && (saved.Err == nil || sticker.Image == nil) {
		sticker.Image = &attachmentJSON{
			ContentType: image.ContentType, Filename: image.Filename, Size: image.Size, Caption: image.Caption,
		}
	}

	if sticker.Image != nil {
		sticker.Image.Path = saved.Path
		sticker.Image.DownloadError = errorText(saved.Err)
	}
}
