//go:build cgo

package signal

import (
	"context"
	"errors"
	"fmt"

	"go.mau.fi/mautrix-signal/pkg/signalmeow"
	"go.mau.fi/mautrix-signal/pkg/signalmeow/protobuf/signalpb"
	"go.mau.fi/mautrix-signal/pkg/signalmeow/web"
)

// Lengths of an attachment's key (AES-256 + HMAC-SHA256) and digest (SHA-256).
const (
	attachmentKeyLength    = 64
	attachmentDigestLength = 32
)

// convertAttachment maps an attachment pointer of a received message to ours.
func convertAttachment(att *signalpb.AttachmentPointer) Attachment {
	return Attachment{
		ContentType: att.GetContentType(),
		Filename:    att.GetFileName(),
		Size:        att.GetSize(),
		Caption:     att.GetCaption(),
		Remote: RemoteAttachment{
			CDNNumber: att.GetCdnNumber(),
			CDNID:     att.GetCdnId(),
			CDNKey:    att.GetCdnKey(),
			Key:       att.GetKey(),
			Digest:    att.GetDigest(),
		},
	}
}

func (c *meowClient) Download(ctx context.Context, att Attachment) ([]byte, error) {
	err := checkDownload(att)
	if err != nil {
		return nil, err
	}

	remote := att.Remote
	ctx = c.zlog.WithContext(ctx)

	type result struct {
		data []byte
		err  error
	}

	// signalmeow's request ignores ctx (and has no timeout), so a download only ends when the
	// transfer does. Waiting in a goroutine lets ctx cancel the wait at least; the abandoned
	// transfer finishes in the background and its result is dropped.
	done := make(chan result, 1)

	go func() {
		data, err := signalmeow.DownloadAttachment(ctx, remote.CDNID, remote.CDNKey, remote.CDNNumber,
			remote.Key, remote.Digest, false, att.Size, nil)
		done <- result{data: data, err: err}
	}()

	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("download: %w", ctx.Err())
	case res := <-done:
		if res.err != nil {
			return nil, downloadError(res.err)
		}

		return res.data, nil
	}
}

// checkDownload rejects an attachment that Download can't fetch or verify before any request.
// It also keeps signalmeow from indexing past its CDN host list, which panics.
func checkDownload(att Attachment) error {
	remote := att.Remote

	switch {
	case !remote.Located():
		return ErrAttachmentNotFound
	case int(remote.CDNNumber) >= len(web.CDNHosts):
		return fmt.Errorf("%w: unknown CDN %d", ErrAttachmentInvalid, remote.CDNNumber)
	case len(remote.Key) != attachmentKeyLength:
		return fmt.Errorf("%w: key has %d bytes, want %d", ErrAttachmentInvalid, len(remote.Key), attachmentKeyLength)
	case len(remote.Digest) != attachmentDigestLength:
		return fmt.Errorf("%w: digest has %d bytes, want %d", ErrAttachmentInvalid, len(remote.Digest),
			attachmentDigestLength)
	case att.Size == 0:
		// signalmeow cuts the plaintext to the size, which would leave nothing.
		return fmt.Errorf("%w: no size", ErrAttachmentInvalid)
	default:
		return nil
	}
}

// downloadError maps signalmeow's download errors to ours.
func downloadError(err error) error {
	switch {
	case errors.Is(err, signalmeow.ErrAttachmentNotFound):
		return ErrAttachmentNotFound
	case errors.Is(err, signalmeow.ErrInvalidDigestForAttachment),
		errors.Is(err, signalmeow.ErrInvalidMACForAttachment):
		return fmt.Errorf("%w: %w", ErrAttachmentInvalid, err)
	default:
		return fmt.Errorf("fetch attachment: %w", err)
	}
}
