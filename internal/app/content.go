package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	_ "image/gif"  // decoder for the dimensions of attached images
	_ "image/jpeg" // decoder for the dimensions of attached images
	_ "image/png"  // decoder for the dimensions of attached images
	"io"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/cwbudde/go-signal/internal/signal"
)

// MaxAttachmentSize is the largest file Send attaches: 100 MiB, the limit of the official
// clients.
const MaxAttachmentSize = 100 << 20

var (
	// ErrAttachmentTooLarge means that a file is larger than MaxAttachmentSize.
	ErrAttachmentTooLarge = errors.New("attachment is too large")
	// ErrNotAFile means that an attachment path is not a regular file.
	ErrNotAFile = errors.New("not a regular file")
	// ErrInvalidQuote means that a quote argument is not <author>:<timestamp>.
	ErrInvalidQuote = errors.New("invalid quote")
	// ErrOutsideAttachDir means that an attachment is not in SendRequest.AttachDir.
	ErrOutsideAttachDir = errors.New("outside the attachment directory")
)

// mentionPattern matches a @{<recipient>} placeholder in a message body.
var mentionPattern = regexp.MustCompile(`@\{([^{}\s]+)\}`)

const (
	// mentionPlaceholder is the character a mention covers in the body; clients show the name.
	mentionPlaceholder = "\uFFFC"
	// mentionLength is the length of mentionPlaceholder in UTF-16 code units.
	mentionLength = 1
)

// loadAttachments reads the files at paths (see loadAttachment). With a dir, the paths are
// relative to it, and files outside it (also through symlinks) fail with ErrOutsideAttachDir.
func loadAttachments(dir string, paths []string) ([]signal.OutgoingAttachment, error) {
	var files fileOpener = osFiles{}

	if dir != "" && len(paths) > 0 {
		root, err := openAttachDir(dir)
		if err != nil {
			return nil, err
		}
		defer root.Close()

		files = root
	}

	out := make([]signal.OutgoingAttachment, 0, len(paths))

	var errs []error

	for _, path := range paths {
		att, err := loadAttachment(files, path)
		if err != nil {
			errs = append(errs, err)

			continue
		}

		out = append(out, att)
	}

	err := errors.Join(errs...)
	if err != nil {
		return nil, err
	}

	return out, nil
}

// fileOpener opens attachment files: from the file system, or confined to a directory.
type fileOpener interface {
	Stat(name string) (fs.FileInfo, error)
	Open(name string) (*os.File, error)
}

// osFiles opens files by their path.
type osFiles struct{}

func (osFiles) Stat(name string) (fs.FileInfo, error) { return os.Stat(name) } //nolint:wrapcheck // as os

func (osFiles) Open(name string) (*os.File, error) {
	return os.Open(name) //nolint:gosec,wrapcheck // the user names the file to send; as os
}

// attachDir opens files by their path relative to a directory, and never outside it.
type attachDir struct {
	*os.Root

	dir string
}

func openAttachDir(dir string) (*attachDir, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("attachment directory: %w", err)
	}

	root, err := os.OpenRoot(abs)
	if err != nil {
		return nil, fmt.Errorf("attachment directory: %w", err)
	}

	return &attachDir{Root: root, dir: abs}, nil
}

func (d *attachDir) Stat(name string) (fs.FileInfo, error) {
	rel, err := d.local(name)
	if err != nil {
		return nil, err
	}

	info, err := d.Root.Stat(rel)
	if err != nil {
		// os.Root has no sentinel error for a symlink that leads outside.
		_, outErr := os.Stat(filepath.Join(d.dir, rel))
		if outErr == nil {
			return nil, fmt.Errorf("attachment %s: %w %s", name, ErrOutsideAttachDir, d.dir)
		}

		return nil, err //nolint:wrapcheck // as os
	}

	return info, nil
}

func (d *attachDir) Open(name string) (*os.File, error) {
	rel, err := d.local(name)
	if err != nil {
		return nil, err
	}

	return d.Root.Open(rel) //nolint:wrapcheck // as os
}

// local returns name relative to the directory, if it lies within it.
func (d *attachDir) local(name string) (string, error) {
	rel := name
	if filepath.IsAbs(name) {
		var err error

		rel, err = filepath.Rel(d.dir, name)
		if err != nil {
			return "", fmt.Errorf("attachment %s: %w %s", name, ErrOutsideAttachDir, d.dir)
		}
	}

	if !filepath.IsLocal(rel) {
		return "", fmt.Errorf("attachment %s: %w %s", name, ErrOutsideAttachDir, d.dir)
	}

	return rel, nil
}

// loadAttachment reads the file at path, checks its size and determines its content type and,
// for GIF, JPEG and PNG images, its dimensions.
func loadAttachment(files fileOpener, path string) (signal.OutgoingAttachment, error) {
	info, err := files.Stat(path)
	if err != nil {
		return signal.OutgoingAttachment{}, fmt.Errorf("attachment: %w", err)
	}

	if !info.Mode().IsRegular() {
		return signal.OutgoingAttachment{}, fmt.Errorf("attachment %s: %w", path, ErrNotAFile)
	}

	if info.Size() > MaxAttachmentSize {
		return signal.OutgoingAttachment{}, fmt.Errorf("attachment %s: %w (%d bytes, at most %d)",
			path, ErrAttachmentTooLarge, info.Size(), MaxAttachmentSize)
	}

	data, err := readFile(files, path)
	if err != nil {
		return signal.OutgoingAttachment{}, fmt.Errorf("attachment: %w", err)
	}

	att := signal.OutgoingAttachment{
		Data:        data,
		Filename:    filepath.Base(path),
		ContentType: contentType(path, data),
	}

	if strings.HasPrefix(att.ContentType, "image/") {
		cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
		if err == nil && cfg.Width > 0 && cfg.Height > 0 {
			att.Width, att.Height = uint32(cfg.Width), uint32(cfg.Height) //nolint:gosec // positive
		}
	}

	return att, nil
}

// readFile reads the file at path, at most MaxAttachmentSize bytes of it.
func readFile(files fileOpener, path string) ([]byte, error) {
	file, err := files.Open(path)
	if err != nil {
		return nil, err //nolint:wrapcheck // the caller wraps it
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, MaxAttachmentSize+1))
	if err != nil {
		return nil, err //nolint:wrapcheck // the caller wraps it
	}

	if len(data) > MaxAttachmentSize {
		return nil, fmt.Errorf("%s: %w (at most %d bytes)", path, ErrAttachmentTooLarge, MaxAttachmentSize)
	}

	return data, nil
}

// contentType returns the MIME type of a file: sniffed from data, unless that only gives a
// generic type (binary, plain text or ZIP, as for office documents), in which case the file
// extension decides if it is known.
func contentType(path string, data []byte) string {
	sniffed := baseType(http.DetectContentType(data))

	switch sniffed {
	case "application/octet-stream", "text/plain", "application/zip":
		if byExt := baseType(mime.TypeByExtension(filepath.Ext(path))); byExt != "" {
			return byExt
		}
	}

	return sniffed
}

// baseType returns the MIME type without parameters, or "" if it is invalid.
func baseType(contentType string) string {
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return ""
	}

	return mediaType
}

// ParseQuote parses a quote argument, <author>:<timestamp>: the author of the quoted message as
// a user recipient argument (see ParseRecipient; self for our own messages) and its sent
// timestamp in milliseconds.
func ParseQuote(arg string) (Target, uint64, error) {
	return parseMessageRef(arg, ErrInvalidQuote)
}

// parseMessageRef parses <author>:<timestamp> (see ParseQuote); errors wrap kind.
func parseMessageRef(arg string, kind error) (Target, uint64, error) {
	i := strings.LastIndex(arg, ":")
	if i < 0 {
		return Target{}, 0, fmt.Errorf("%w %q: want <author>:<timestamp>", kind, arg)
	}

	timestamp, err := parseTimestamp(arg[i+1:])
	if err != nil {
		return Target{}, 0, fmt.Errorf("%w %q: %w", kind, arg, err)
	}

	author, err := ParseRecipient(arg[:i])
	if err != nil {
		return Target{}, 0, fmt.Errorf("%w %q: %w", kind, arg, err)
	}

	if author.IsGroup() {
		return Target{}, 0, fmt.Errorf("%w %q: the author must be a user", kind, arg)
	}

	return author, timestamp, nil
}

// errBadTimestamp explains an invalid message timestamp.
var errBadTimestamp = errors.New("the timestamp must be the message's time in ms")

// parseTimestamp parses a message's sent timestamp in ms, which is never zero.
func parseTimestamp(arg string) (uint64, error) {
	timestamp, err := strconv.ParseUint(strings.TrimSpace(arg), 10, 64)
	if err != nil || timestamp == 0 {
		return 0, errBadTimestamp
	}

	return timestamp, nil
}

// mention is a @{<recipient>} placeholder found in a message body.
type mention struct {
	start  uint32 // in UTF-16 code units of the rewritten body
	target Target
}

// parseMentions replaces every @{<recipient>} placeholder in body whose recipient is a user
// (see ParseRecipient) by U+FFFC and returns the new body with the mentions. Other @{...}
// sequences (like git's HEAD@{1}) are left alone.
func parseMentions(body string) (string, []mention) {
	matches := mentionPattern.FindAllStringSubmatchIndex(body, -1)
	if matches == nil {
		return body, nil
	}

	var (
		out      strings.Builder
		mentions []mention
		units    int // UTF-16 length of out
		last     int
	)

	for _, match := range matches {
		target, err := ParseRecipient(body[match[2]:match[3]])
		if err != nil || target.IsGroup() {
			continue
		}

		before := body[last:match[0]]
		units += utf16Len(before)
		mentions = append(mentions, mention{start: uint32(units), target: target}) //nolint:gosec // bounded by the body
		units += mentionLength

		out.WriteString(before)
		out.WriteString(mentionPlaceholder)

		last = match[1]
	}

	out.WriteString(body[last:])

	return out.String(), mentions
}

// utf16Len returns the length of s in UTF-16 code units.
func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		n += utf16.RuneLen(r)
	}

	return n
}

// content is the resolved rich content of a message.
type content struct {
	body        string
	quote       *signal.Quote
	mentions    []signal.Mention
	attachments []signal.UploadedAttachment
	// reaction or deleteTarget replace the content above for React and Delete.
	reaction     *signal.OutgoingReaction
	deleteTarget uint64
}

// buildContent resolves the quote author and the mentioned users of req to ACIs and uploads files.
func (a *App) buildContent(ctx context.Context, req SendRequest, files []signal.OutgoingAttachment) (content, error) {
	out, err := a.resolveContent(ctx, req)
	if err != nil || len(files) == 0 {
		return out, err
	}

	out.attachments, err = a.client.Upload(ctx, files)
	if err != nil {
		return content{}, fmt.Errorf("upload attachments: %w", err)
	}

	return out, nil
}

// resolveContent resolves the quote author and the mentioned users of req to ACIs.
func (a *App) resolveContent(ctx context.Context, req SendRequest) (content, error) {
	body, found := parseMentions(req.Body)
	out := content{body: body}

	targets := make([]Target, 0, len(found)+1)
	for _, m := range found {
		targets = append(targets, m.target)
	}

	var quoted uint64

	if req.Quote != "" {
		author, timestamp, err := ParseQuote(req.Quote)
		if err != nil {
			return content{}, err
		}

		targets = append(targets, author)
		quoted = timestamp
	}

	if len(targets) == 0 {
		return out, nil
	}

	err := a.resolveUsers(ctx, targets)
	if err != nil {
		return content{}, fmt.Errorf("resolve mentions and quote: %w", err)
	}

	for i, m := range found {
		out.mentions = append(out.mentions, signal.Mention{
			Start: m.start, Length: mentionLength, Recipient: targets[i].Recipient,
		})
	}

	if req.Quote != "" {
		out.quote = &signal.Quote{Author: targets[len(targets)-1].Recipient, Timestamp: quoted, Text: req.QuoteText}
	}

	return out, nil
}
