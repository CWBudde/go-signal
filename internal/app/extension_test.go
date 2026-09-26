package app_test

import (
	"testing"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/signal"
)

func TestAttachmentFilenameExtensions(t *testing.T) {
	t.Parallel()

	for contentType, ext := range map[string]string{
		"application/pdf":     ".pdf",
		"audio/aac":           ".aac",
		"audio/mp4":           ".m4a",
		"audio/mpeg":          ".mp3",
		"audio/ogg":           ".ogg",
		"image/gif":           ".gif",
		"image/heic":          ".heic",
		"image/webp":          ".webp",
		"text/plain":          ".txt",
		"text/x-signal-plain": ".txt",
		"video/mp4":           ".mp4",
		"video/quicktime":     ".mov",
		"Video/QuickTime":     ".mov",
		"":                    "",
	} {
		got := app.AttachmentFilename(msgTS, 0, signal.Attachment{ContentType: contentType})
		if want := "1789907401000-0-" + noFileName + ext; got != want {
			t.Errorf("content type %q: got %q, want %q", contentType, got, want)
		}
	}
}
