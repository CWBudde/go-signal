//go:build cgo || libsignal_go

//nolint:goconst // Literal expectations are independent story fixtures.
package signal_test

import (
	"errors"
	"testing"

	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow/protobuf/signalpb"
)

//nolint:cyclop // Assertions cover independent story payload and preflight boundaries.
func TestOutgoingStoryMessage(t *testing.T) {
	t.Parallel()

	msg, err := signal.OutgoingStoryMessage(&signal.OutgoingStory{Text: "A good day", AllowsReplies: true}, nil)
	if err != nil {
		t.Fatal(err)
	}

	text := msg.GetTextAttachment()
	if !msg.GetAllowsReplies() || text.GetText() != "A good day" ||
		text.GetTextStyle() != signalpb.TextAttachment_DEFAULT ||
		text.GetColor() != 0xff000000 || text.GetTextForegroundColor() != 0xffffffff || msg.GetGroup() != nil {
		t.Fatalf("text card: %v", msg)
	}

	pointer := &signalpb.AttachmentPointer{ContentType: new(pngType), Key: []byte{1, 2}, FileName: new("story-photo.png")}

	media := &signal.OutgoingStory{File: &signal.UploadedAttachment{ID: "uploaded-card-media", ContentType: pngType}}

	msg, err = signal.OutgoingStoryMessage(media, pointer)
	if err != nil || msg.GetFileAttachment().GetFileName() != "story-photo.png" || msg.GetTextAttachment() != nil ||
		msg.GetAllowsReplies() {
		t.Fatalf("media: %v %v", msg, err)
	}

	pointer.Key[0] = 9

	if msg.GetFileAttachment().GetKey()[0] != 1 {
		t.Fatal("caller pointer aliases outgoing media")
	}

	_, err = signal.OutgoingStoryMessage(media, nil)
	if !errors.Is(err, signal.ErrUnknownAttachment) {
		t.Fatalf("missing pointer: %v", err)
	}
}
