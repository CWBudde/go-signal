//go:build cgo

package signal_test

import (
	"bytes"
	"errors"
	"io"
	"testing"

	"github.com/cwbudde/go-signal/internal/signal"
	"go.mau.fi/mautrix-signal/pkg/signalmeow"
)

func downloadable() signal.Attachment {
	return signal.Attachment{
		ContentType: "image/png",
		Size:        7,
		Remote: signal.RemoteAttachment{
			CDNNumber: 3,
			CDNKey:    "abc",
			Key:       bytes.Repeat([]byte{1}, 64),
			Digest:    bytes.Repeat([]byte{2}, 32),
		},
	}
}

func TestCheckDownload(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		change func(*signal.Attachment)
		want   error
	}{
		{"valid", func(*signal.Attachment) {}, nil},
		{"legacy CDN ID", func(a *signal.Attachment) { a.Remote.CDNKey, a.Remote.CDNID = "", 42 }, nil},
		{"no location", func(a *signal.Attachment) { a.Remote.CDNKey = "" }, signal.ErrAttachmentNotFound},
		{"unknown CDN", func(a *signal.Attachment) { a.Remote.CDNNumber = 4 }, signal.ErrAttachmentInvalid},
		{"short key", func(a *signal.Attachment) { a.Remote.Key = a.Remote.Key[:32] }, signal.ErrAttachmentInvalid},
		{"no digest", func(a *signal.Attachment) { a.Remote.Digest = nil }, signal.ErrAttachmentInvalid},
		{"no size", func(a *signal.Attachment) { a.Size = 0 }, signal.ErrAttachmentInvalid},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			att := downloadable()
			test.change(&att)

			err := signal.CheckDownload(att)
			if test.want == nil && err != nil || !errors.Is(err, test.want) {
				t.Errorf("CheckDownload = %v, want %v", err, test.want)
			}
		})
	}
}

func TestDownloadError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in, want error
	}{
		{signalmeow.ErrAttachmentNotFound, signal.ErrAttachmentNotFound},
		{signalmeow.ErrInvalidDigestForAttachment, signal.ErrAttachmentInvalid},
		{signalmeow.ErrInvalidMACForAttachment, signal.ErrAttachmentInvalid},
		{io.ErrUnexpectedEOF, io.ErrUnexpectedEOF},
	}

	for _, test := range tests {
		err := signal.DownloadError(test.in)
		if !errors.Is(err, test.want) {
			t.Errorf("DownloadError(%v) = %v, want %v", test.in, err, test.want)
		}
	}
}
