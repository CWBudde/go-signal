package mcp_test

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/mcp"
	"github.com/cwbudde/go-signal/internal/output"
	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/go-signal/internal/signal/signaltest"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	stickerPackArg     = "pack"
	stickerItemArg     = "stickerId"
	stickerDecline     = "decline"
	stickerInstallTool = "sticker_pack_install"
	stickerListTool    = "sticker_packs_list"
	stickerSendTool    = "sticker_send"
	stickerGetTool     = "sticker_get"
	mcpStickerID       = "abababababababababababababababab"
	mcpStickerLink     = "https://signal.art/addstickers/#pack_id=" + mcpStickerID +
		"&pack_key=0000000000000000000000000000000000000000000000000000000000000000"
)

func stickerFake() *signaltest.Fake {
	fake := writeFake()
	fake.StickerPackFixtures = map[string]signal.StickerPack{
		mcpStickerID: {
			Reference: signal.StickerReference{PackID: mcpStickerID, PackKey: make([]byte, 32)},
			Title:     "Animals",
			Stickers: []signal.StickerPackItem{
				{
					ID: 7,
					Data: signal.StickerData{
						Emoji: "cat",
						Image: signal.OutgoingAttachment{Data: []byte("gif"), ContentType: "image/gif"},
					},
				},
			},
		},
	}
	fake.Stickers = map[string]signal.StickerData{
		mcpStickerID + ":7": fake.StickerPackFixtures[mcpStickerID].Stickers[0].Data,
	}

	return fake
}

func TestMCPStickerTools(t *testing.T) {
	t.Parallel()

	fake := stickerFake()
	session := connectWith(t, fake, mcp.Options{}, testClient{}, allow(t, aliceNumber))

	var packs output.StickerPacksJSON
	call(t, session, stickerInstallTool, map[string]any{stickerPackArg: mcpStickerLink}, &packs)

	if len(packs.Packs) != 1 || packs.Packs[0].ID != mcpStickerID {
		t.Fatalf("pack %+v", packs)
	}

	res := callRaw(t, session, stickerListTool, nil)

	raw, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(string(raw), "packKey") || strings.Contains(string(raw), mcpStickerLink) {
		t.Fatal("key exposed")
	}

	var sentResult sent
	call(t, session, stickerSendTool, map[string]any{
		recipientsArg:  []string{aliceNumber},
		stickerPackArg: mcpStickerLink,
		stickerItemArg: 7,
	}, &sentResult)

	if len(fake.Sent()) != 1 || fake.Sent()[0].Sticker == nil || len(fake.FetchedStickers()) != 0 {
		t.Fatal("cached send failed")
	}
}

func TestMCPStickerSendDenied(t *testing.T) {
	t.Parallel()

	fake := stickerFake()
	session := connectWith(t, fake, mcp.Options{}, testClient{}, allow(t))

	res := callRaw(t, session, stickerSendTool, map[string]any{
		recipientsArg:  []string{aliceNumber},
		stickerPackArg: mcpStickerLink,
		stickerItemArg: 7,
	})
	if !res.IsError || !strings.Contains(text(res), app.ErrRecipientNotAllowed.Error()) ||
		len(fake.FetchedStickers()) != 0 || len(fake.Uploaded()) != 0 {
		t.Fatalf("denied %+v", res)
	}
}

func TestMCPStickerInstallConfirmation(t *testing.T) {
	t.Parallel()

	for _, action := range []string{accept, stickerDecline} {
		t.Run(action, func(t *testing.T) {
			t.Parallel()

			fake := stickerFake()
			session := connectWith(t, fake, mcp.Options{Confirm: true},
				testClient{options: &sdk.ClientOptions{ElicitationHandler: func(
					_ context.Context, req *sdk.ElicitRequest,
				) (*sdk.ElicitResult, error) {
					if strings.Contains(req.Params.Message, "pack_key") || strings.Contains(req.Params.Message, mcpStickerLink) {
						t.Error("confirmation exposed key")
					}

					return &sdk.ElicitResult{Action: action}, nil
				}}})

			res := callRaw(t, session, stickerInstallTool, map[string]any{stickerPackArg: mcpStickerLink})
			if res.IsError == (action == accept) {
				t.Fatalf("confirmation %s %s", action, text(res))
			}

			var packs output.StickerPacksJSON
			call(t, session, stickerListTool, nil, &packs)

			if (len(packs.Packs) == 1) != (action == accept) {
				t.Fatalf("installed %+v", packs)
			}
		})
	}
}

//nolint:cyclop // checks saved bytes, key-free inbox output, receipts and read-only tools together.
func TestMCPStickerGet(t *testing.T) {
	t.Parallel()

	fake := stickerFake()
	session := connectWith(t, fake, mcp.Options{ReadOnly: true}, testClient{})
	msg := photoMessage(1001)
	msg.Attachments = nil
	msg.Sticker = &signal.Sticker{
		PackID:    mcpStickerID,
		PackKey:   make([]byte, 32),
		StickerID: 7,
	}
	messages := waitForPush(t, session, fake, msg)
	entryID := messages.Messages[len(messages.Messages)-1].ID

	var got struct {
		Path string `json:"path"`
	}

	res := callRaw(t, session, stickerGetTool, map[string]any{messageArg: entryID})
	decode(t, res, &got)

	data, err := os.ReadFile(got.Path)
	if err != nil || string(data) != "gif" || len(res.Content) != 2 {
		t.Fatalf("image %q %v %+v", data, err, res)
	}

	if len(fake.Receipts()) != 0 {
		t.Fatal("sticker_get sent receipt")
	}

	list := callRaw(t, session, messagesList, nil)

	raw, err := json.Marshal(list)
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(string(raw), "PackKey") || strings.Contains(string(raw), "packKey") {
		t.Fatal("inbox output exposed key")
	}

	for _, tool := range []string{stickerInstallTool, stickerSendTool} {
		result, err := session.CallTool(t.Context(), &sdk.CallToolParams{
			Name: tool,
			Arguments: map[string]any{
				stickerPackArg: mcpStickerLink,
				recipientsArg:  []string{aliceNumber},
				stickerItemArg: 7,
			},
		})
		if err == nil && !result.IsError {
			t.Fatalf("read-only permits %s", tool)
		}
	}
}
