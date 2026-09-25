package app

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/cwbudde/go-signal/internal/signal"
)

var (
	// ErrNotADir means that the download directory is not a directory.
	ErrNotADir = errors.New("not a directory")
	// ErrFileExists means that no free file name was found for an attachment.
	ErrFileExists = errors.New("file exists")
)

const (
	// dirPerm and filePerm match the data dir: downloads are private.
	dirPerm  = 0o700
	filePerm = 0o600
	// maxNameBytes limits the sanitized part of a file name (after "<ts>-<n>-"), well below
	// the usual limit of 255 bytes per name.
	maxNameBytes = 120
	// maxExtBytes is the longest extension that is kept when a name is shortened.
	maxExtBytes = 16
	// maxRenames is how many numbered variants of a taken name are tried.
	maxRenames = 100
	// defaultName replaces a file name that is missing or has nothing usable.
	defaultName = "attachment"
)

// extension returns the file extension for common content types, for attachments without a
// file name (photos and voice notes usually have none), or "". "text/x-signal-plain" is the rest
// of a long message that Signal sends as an attachment. Unlike mime.ExtensionsByType, it doesn't
// depend on the system's MIME tables.
//
//nolint:cyclop // one case per content type
func extension(contentType string) string {
	switch strings.ToLower(contentType) {
	case "application/pdf":
		return ".pdf"
	case "audio/aac":
		return ".aac"
	case "audio/mp4":
		return ".m4a"
	case "audio/mpeg":
		return ".mp3"
	case "audio/ogg":
		return ".ogg"
	case "image/gif":
		return ".gif"
	case "image/heic":
		return ".heic"
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	case "text/plain", "text/x-signal-plain":
		return ".txt"
	case "video/mp4":
		return ".mp4"
	case "video/quicktime":
		return ".mov"
	default:
		return ""
	}
}

// SaveAttachmentsRequest is the input of SaveAttachments.
type SaveAttachmentsRequest struct {
	// Dir is the directory the files go to; it is created (mode 0700) if missing.
	Dir string
	// Message is the received message whose attachments are saved.
	Message *signal.Message
}

// SavedAttachment is the outcome of saving one attachment.
type SavedAttachment struct {
	// Path is the file written, Dir joined with its name; empty if Err is set.
	Path string
	// Err says why the attachment wasn't saved, e.g. signal.ErrAttachmentNotFound.
	Err error
}

// PrepareDownloadDir creates dir (mode 0700) if it is missing and checks that it is a
// directory, so that a bad --download-attachments fails before anything is received.
func PrepareDownloadDir(dir string) error {
	err := os.MkdirAll(dir, dirPerm)
	if err != nil {
		return fmt.Errorf("download dir: %w", err)
	}

	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("download dir: %w", err)
	}

	if !info.IsDir() {
		return fmt.Errorf("download dir %s: %w", dir, ErrNotADir)
	}

	return nil
}

// SaveAttachments downloads the attachments of req.Message (including view-once ones) and
// writes each to a new file in req.Dir, named by AttachmentFilename. An existing file is never
// overwritten: the name gets a number instead ("…-2.jpg"). The result has one entry per
// attachment, in order; a failure only affects its own entry, so that receiving can go on.
func (a *App) SaveAttachments(ctx context.Context, req SaveAttachmentsRequest) []SavedAttachment {
	atts := req.Message.Attachments
	out := make([]SavedAttachment, len(atts))

	err := PrepareDownloadDir(req.Dir)
	if err != nil {
		for i := range out {
			out[i].Err = err
		}

		return out
	}

	for i, att := range atts {
		name := AttachmentFilename(req.Message.Timestamp, i+1, att)

		out[i].Path, out[i].Err = a.saveAttachment(ctx, req.Dir, name, att)
	}

	return out
}

func (a *App) saveAttachment(ctx context.Context, dir, name string, att signal.Attachment) (string, error) {
	data, err := a.client.Download(ctx, att)
	if err != nil {
		return "", err //nolint:wrapcheck // shown as "download failed: <err>"
	}

	path, err := writeNewFile(dir, name, data)
	if err != nil {
		return "", fmt.Errorf("save: %w", err)
	}

	return path, nil
}

// writeNewFile writes data to a file in dir that didn't exist before: name, or name with a
// number before the extension. The file is opened through an os.Root, so that it can't end up
// outside dir, not even through a symlink.
func writeNewFile(dir, name string, data []byte) (string, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return "", fmt.Errorf("open download dir: %w", err)
	}
	defer root.Close()

	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)

	for i := 1; i <= maxRenames; i++ {
		candidate := name
		if i > 1 {
			candidate = stem + "-" + strconv.Itoa(i) + ext
		}

		file, err := root.OpenFile(candidate, os.O_WRONLY|os.O_CREATE|os.O_EXCL, filePerm)
		if errors.Is(err, fs.ErrExist) {
			continue
		}

		if err != nil {
			return "", fmt.Errorf("create file: %w", err)
		}

		_, err = file.Write(data)

		err = errors.Join(err, file.Close())
		if err != nil {
			return "", errors.Join(fmt.Errorf("write %s: %w", candidate, err), root.Remove(candidate))
		}

		return filepath.Join(dir, candidate), nil
	}

	return "", fmt.Errorf("%w: %s and %d numbered variants", ErrFileExists, name, maxRenames-1)
}

// AttachmentFilename returns the file name for the n-th (from 1) attachment of the message with
// the timestamp sent: "<sent>-<n>-<name>", where name is the sanitized file name the sender gave, or
// "attachment" plus an extension that fits the content type. Only letters, digits, ".", "-"
// and "_" remain of the sender's name; directories are dropped, other characters become "_",
// leading dots are removed (no hidden files, no ".."), and it is shortened to 120 bytes.
func AttachmentFilename(sent uint64, n int, att signal.Attachment) string {
	name := sanitizeFilename(att.Filename)
	if name == "" {
		name = defaultName + extension(att.ContentType)
	}

	return strconv.FormatUint(sent, 10) + "-" + strconv.Itoa(n) + "-" + name
}

// sanitizeFilename makes the sender's file name safe to use in a directory (see
// AttachmentFilename); "" means that nothing usable is left.
func sanitizeFilename(name string) string {
	// Senders' paths use either separator.
	name = name[strings.LastIndexAny(name, `/\`)+1:]

	var out strings.Builder

	for _, char := range name {
		switch {
		case unicode.IsLetter(char), unicode.IsDigit(char), unicode.IsMark(char), strings.ContainsRune(".-_", char):
			out.WriteRune(char)
		case strings.HasSuffix(out.String(), "_"):
			// Collapse runs of replaced characters.
		default:
			out.WriteRune('_')
		}
	}

	clean := strings.Trim(out.String(), "._")
	if strings.Trim(clean, "-") == "" {
		return ""
	}

	return shorten(clean, maxNameBytes)
}

// shorten cuts name to at most limit bytes on a character boundary, keeping a short extension.
func shorten(name string, limit int) string {
	if len(name) <= limit {
		return name
	}

	ext := filepath.Ext(name)
	if len(ext) > maxExtBytes {
		ext = ""
	}

	stem := strings.TrimSuffix(name, ext)
	keep := limit - len(ext)

	for keep > 0 && !utf8.RuneStart(stem[keep]) {
		keep--
	}

	return stem[:keep] + ext
}
