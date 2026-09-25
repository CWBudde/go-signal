package signal

import "errors"

// ErrAttachmentNotFound means that the CDN doesn't have an attachment (any more; it keeps them
// for about 30 days), or that the attachment names no CDN object.
var ErrAttachmentNotFound = errors.New("attachment not found on the CDN")

// ErrAttachmentInvalid means that a downloaded attachment failed verification (digest or MAC
// mismatch, too short), or that it lacks what is needed to fetch and verify it (key, digest, a
// known CDN).
var ErrAttachmentInvalid = errors.New("invalid attachment")

// RemoteAttachment is what Download needs to fetch, decrypt and verify an attachment: where it
// is on the CDN and the keys that came with the message.
type RemoteAttachment struct {
	// CDNNumber selects the CDN; CDNKey (current uploads) or CDNID (legacy ones) names the
	// object on it.
	CDNNumber uint32
	CDNID     uint64
	CDNKey    string
	// Key is the 64-byte AES-256 + HMAC-SHA256 key the content is encrypted with.
	Key []byte
	// Digest is the SHA-256 of the encrypted content (padding and MAC included).
	Digest []byte
}

// Located reports whether r names a CDN object.
func (r RemoteAttachment) Located() bool {
	return r.CDNKey != "" || r.CDNID != 0
}
