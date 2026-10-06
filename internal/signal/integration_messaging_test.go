//go:build integration && (cgo || libsignal_go)

package signal_test

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/cwbudde/go-signal/internal/signal"
)

// mentionPlaceholder is what a mention covers in a body; clients show the user's name instead.
const mentionPlaceholder = "￼"

// receiptGrace is how long to look for delivery receipts that the peer may not send at all.
const receiptGrace = 15 * time.Second

var errNotInContacts = errors.New("not in contacts")

// TestIntegrationMessaging sends fresh rich content to the peer and the test group. Delivery
// receipts prove transport only; the logged timestamps tell what to check on the peer's phone.
func TestIntegrationMessaging(t *testing.T) { //nolint:paralleltest // one live account
	if os.Getenv("GOSIGNAL_IT_MESSAGING") != "1" {
		t.Skip("GOSIGNAL_IT_MESSAGING not set to 1")
	}

	env := connectLive(t)
	env.stepReceive(t)
	env.resolvePeer(t)

	t.Run("Direct", func(t *testing.T) { //nolint:paralleltest // one live account
		env.messagingRound(t, signal.SendRequest{Recipients: []signal.Recipient{env.peer}})
	})
	t.Run("Group", func(t *testing.T) { //nolint:paralleltest // one live account
		group := env.testGroup(t)
		env.messagingRound(t, signal.SendRequest{GroupID: group.ID})
	})
	t.Run("Block", func(t *testing.T) { //nolint:paralleltest // one live account
		env.checkBlock(t)
	})
}

// messagingRound sends an image with a caption, a reply that quotes it and mentions the peer, a
// reaction on the image, and a message that it deletes again, all to the chat of req.
func (env *liveEnv) messagingRound(t *testing.T, chat signal.SendRequest) {
	t.Helper()

	own := signal.Recipient{ACI: env.acc.ACI}
	caption := body("image with caption")

	req := chat
	req.Body = caption
	req.Attachments = env.uploadPNG(t)
	imageSent := env.sendWithReceipt(t, "image", req)

	req = chat
	req.Body = body("reply quoting the image, mentioning " + mentionPlaceholder)
	req.Quote = &signal.Quote{Author: own, Timestamp: imageSent, Text: caption}
	before, _, _ := strings.Cut(req.Body, mentionPlaceholder)
	req.Mentions = []signal.Mention{{Start: utf16Len(before), Length: 1, Recipient: env.peer}}
	env.sendWithReceipt(t, "reply with mention", req)

	req = chat
	req.Reaction = &signal.OutgoingReaction{Emoji: "👍", TargetAuthor: own, TargetTimestamp: imageSent}
	reaction := sendAndCheck(t, env.client, req).Timestamp
	t.Logf("reaction 👍 on the image: timestamp %d", reaction)

	req = chat
	req.Body = body("message to be deleted")
	deleted := env.sendWithReceipt(t, "message to delete", req)

	req = chat
	req.DeleteTarget = deleted
	deletion := sendAndCheck(t, env.client, req).Timestamp
	t.Logf("remote delete of %d: timestamp %d", deleted, deletion)

	// Whether the phones acknowledge reactions and deletes is up to them; only report it.
	for _, sent := range []struct {
		what      string
		timestamp uint64
	}{{"👍 reaction", reaction}, {"remote delete", deletion}} {
		err := awaitEvent(env.events, receiptGrace, isDeliveryReceipt(env.peer.ACI, sent.timestamp))
		t.Logf("delivery receipt from the peer for the %s %d: %t", sent.what, sent.timestamp, err == nil)
	}
}

func (env *liveEnv) sendWithReceipt(t *testing.T, what string, req signal.SendRequest) uint64 {
	t.Helper()

	timestamp := sendAndCheck(t, env.client, req).Timestamp
	t.Logf("%s: timestamp %d", what, timestamp)

	waitForReceipt(t, env.events, env.receiptTimeout, env.peer.ACI, timestamp)

	return timestamp
}

func (env *liveEnv) uploadPNG(t *testing.T) []signal.UploadedAttachment {
	t.Helper()

	data, width, height := testPNG(t)

	uploaded, err := env.client.Upload(t.Context(), []signal.OutgoingAttachment{{
		Data: data, ContentType: pngType, Filename: "go-signal-integration.png", Width: width, Height: height,
	}})
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}

	return uploaded
}

// testPNG generates a small gradient image, different on every call.
func testPNG(t *testing.T) ([]byte, uint32, uint32) {
	t.Helper()

	const size = 64

	seed := uint8(time.Now().UnixNano()) //nolint:gosec // any byte
	img := image.NewRGBA(image.Rect(0, 0, size, size))

	for x := range size {
		for y := range size {
			img.Set(x, y, color.RGBA{R: uint8(x * 4), G: uint8(y * 4), B: seed, A: 255}) //nolint:gosec // < 256
		}
	}

	var buf bytes.Buffer

	err := png.Encode(&buf, img)
	if err != nil {
		t.Fatalf("encode PNG: %v", err)
	}

	return buf.Bytes(), size, size
}

func utf16Len(s string) uint32 {
	return uint32(len(utf16.Encode([]rune(s)))) //nolint:gosec // short
}

// testGroup fetches the test group and checks that we and the peer are full members, in the
// group list as well as in fresh server state.
func (env *liveEnv) testGroup(t *testing.T) signal.Group {
	t.Helper()

	ref := os.Getenv("GOSIGNAL_IT_GROUP")
	if ref == "" {
		t.Skip("GOSIGNAL_IT_GROUP not set")
	}

	group, err := env.client.Group(t.Context(), ref)
	if err != nil {
		t.Fatalf("Group: %v", err)
	}

	fresh, err := signal.FreshIntegrationGroup(t.Context(), env.client, group.ID)
	if err != nil {
		t.Fatalf("fresh Group: %v", err)
	}

	groups, err := env.client.Groups(t.Context())
	if err != nil {
		t.Fatalf("Groups: %v", err)
	}

	for _, listed := range groups {
		if listed.ID == group.ID {
			checkMembers(t, "Groups", listed, env.acc.ACI, env.peer.ACI)
		}
	}

	checkMembers(t, "Group", fresh, env.acc.ACI, env.peer.ACI)

	if t.Failed() {
		t.FailNow()
	}

	return fresh
}

func checkMembers(t *testing.T, source string, group signal.Group, acis ...string) {
	t.Helper()

	if group.Err != nil {
		t.Errorf("%s: group %s: %v", source, group.ID, group.Err)
	}

	for _, aci := range acis {
		membership, _ := group.MembershipOf(aci)
		if membership != signal.MembershipMember {
			t.Errorf("%s: %s is %v of group %s, want a full member", source, aci, membership, group.ID)
		}
	}
}

// checkBlock blocks the peer and unblocks it again in the cleanup.
func (env *liveEnv) checkBlock(t *testing.T) {
	t.Helper()

	peers := []signal.Recipient{env.peer}

	// Register before the write: a failure can follow a block that went out.
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		err := env.client.SetBlocked(ctx, peers, false)
		if err != nil {
			t.Errorf("unblock %s: %v; unblock the peer on the phone", env.peer, err)

			return
		}

		blocked, err := contactBlocked(ctx, env.client, env.peer.ACI)
		if err != nil || blocked {
			t.Errorf("after unblocking: blocked %t, %v", blocked, err)
		}
	})

	err := env.client.SetBlocked(t.Context(), peers, true)
	if err != nil {
		t.Fatalf("SetBlocked: %v", err)
	}

	blocked, err := contactBlocked(t.Context(), env.client, env.peer.ACI)
	if err != nil || !blocked {
		t.Errorf("after blocking: blocked %t, %v", blocked, err)
	}
}

func contactBlocked(ctx context.Context, client signal.Client, aci string) (bool, error) {
	contacts, err := client.Contacts(ctx)
	if err != nil {
		return false, err //nolint:wrapcheck // test helper
	}

	for _, contact := range contacts {
		if contact.ACI == aci {
			return contact.Blocked, nil
		}
	}

	return false, errNotInContacts
}
