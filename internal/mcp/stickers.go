package mcp

import (
	"context"
	"errors"
	"fmt"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/output"
	"github.com/cwbudde/go-signal/internal/signal"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type stickerPacksOutput struct {
	Packs []output.StickerPackJSON `json:"stickerPacks"`
}

type stickerPackInput struct {
	Pack string `json:"pack" jsonschema:"HTTPS signal.art sticker pack link containing pack_id and pack_key"`
}

type stickerSendInput struct {
	Recipients []string `json:"recipients" jsonschema:"users (number, ACI, @username, self) or groups"`
	Pack       string   `json:"pack"       jsonschema:"HTTPS signal.art sticker pack link containing pack_id and pack_key"`
	StickerID  uint32   `json:"stickerId"  jsonschema:"sticker ID from sticker_packs_list"`
}

type stickerGetInput struct {
	Message string `json:"message" jsonschema:"inbox ID of a received sticker message"`
}

func addStickerTools(server *sdk.Server, handlers *tools) {
	sdk.AddTool(server, &sdk.Tool{
		Name:  "sticker_packs_list",
		Title: "List local sticker packs",
		Description: "List the selected account's locally installed packs and sticker IDs. Does not expose " +
			"pack keys or image bytes.",
		Annotations: readOnly(),
	}, handlers.stickerPacksList)
	sdk.AddTool(server, &sdk.Tool{
		Name:  "sticker_get",
		Title: "Download received sticker",
		Description: "Save an inbox message's sticker in the download directory, falling back to its " +
			"authenticated pack if the embedded image has expired. Does not mark it read or send receipts.",
		Annotations: &sdk.ToolAnnotations{
			DestructiveHint: new(false),
			OpenWorldHint:   new(false),
		},
	}, handlers.stickerGet)

	if handlers.readOnly {
		return
	}

	sdk.AddTool(server, &sdk.Tool{
		Name:  "sticker_pack_install",
		Title: "Install local sticker pack",
		Description: "Download and cache a complete pack for this account. Installation is local; " +
			"phone installation state is unchanged.",
		Annotations: &sdk.ToolAnnotations{DestructiveHint: new(false), OpenWorldHint: new(true)},
	}, handlers.stickerPackInstall)
	sdk.AddTool(server, &sdk.Tool{
		Name:  "sticker_send",
		Title: "Send sticker",
		Description: "Send a sticker from an existing pack to users or groups. Uses an installed local " +
			"cache when available, otherwise downloads the selected image." + allowlistNote,
		Annotations: &sdk.ToolAnnotations{DestructiveHint: new(true), OpenWorldHint: new(true)},
	}, handlers.stickerSend)
}

//nolint:wrapcheck // app errors already identify the operation; URL errors omit secret input.
func (t *tools) stickerPacksList(
	ctx context.Context, _ *sdk.CallToolRequest, _ noInput,
) (*sdk.CallToolResult, stickerPacksOutput, error) {
	packs, err := t.app.StickerPacks(ctx)
	if err != nil {
		return nil, stickerPacksOutput{}, err
	}

	res, err := t.plain(app.Names{}, func(p *output.Printer) error { return p.StickerPacks(packs) })

	return res, stickerPacksOutput{Packs: output.NewStickerPacksJSON(packs).Packs}, err
}

//nolint:wrapcheck // app errors already identify the operation; URL errors omit secret input.
func (t *tools) stickerPackInstall(
	ctx context.Context, req *sdk.CallToolRequest, in stickerPackInput,
) (*sdk.CallToolResult, stickerPacksOutput, error) {
	ref, err := app.ParseStickerPackURL(in.Pack)
	if err != nil {
		return nil, stickerPacksOutput{}, err
	}

	if t.confirmer != nil {
		ask, err := t.confirmer.check(req, "Install sticker pack "+ref.PackID+" locally?")
		if ask != nil || err != nil {
			return ask, stickerPacksOutput{}, err
		}
	}

	pack, err := t.app.StickerPackInstall(ctx, in.Pack)
	if err != nil {
		return nil, stickerPacksOutput{}, err
	}

	packs := []signal.StickerPack{pack}
	res, err := t.plain(app.Names{}, func(p *output.Printer) error { return p.StickerPacks(packs) })

	return res, stickerPacksOutput{Packs: output.NewStickerPacksJSON(packs).Packs}, err
}

//nolint:wrapcheck // app errors already identify the operation; URL errors omit secret input.
func (t *tools) stickerSend(
	ctx context.Context, req *sdk.CallToolRequest, in stickerSendInput,
) (*sdk.CallToolResult, output.SendJSON, error) {
	ref, err := app.ParseStickerPackURL(in.Pack)
	if err != nil {
		return nil, output.SendJSON{}, err
	}

	ref.StickerID = in.StickerID

	chats, err := t.chatArgs(ctx, in.Recipients)
	if err != nil {
		return nil, output.SendJSON{}, err
	}

	names := t.names(ctx)

	ask, err := t.confirm(ctx, req, chats, names, fmt.Sprintf("Send sticker %d from pack %s", ref.StickerID, ref.PackID))
	if ask != nil || err != nil {
		return ask, output.SendJSON{}, err
	}

	sent, err := t.app.Send(ctx, app.SendRequest{Recipients: chats, Sticker: &ref})
	if err != nil && !errors.Is(err, app.ErrSendFailed) {
		return nil, output.SendJSON{}, err
	}

	res, plainErr := t.plain(names, func(p *output.Printer) error { return p.Send(sent) })

	return sendResult(res, err, plainErr), output.NewSendJSON(sent, names), plainErr
}

//nolint:wrapcheck // app errors already identify the operation; URL errors omit secret input.
func (t *tools) stickerGet(
	ctx context.Context, _ *sdk.CallToolRequest, in stickerGetInput,
) (*sdk.CallToolResult, attachmentGetOutput, error) {
	if t.dir == "" {
		return nil, attachmentGetOutput{}, errNoDownloadDir
	}

	saved, err := t.inbox.Sticker(ctx, in.Message, t.dir)
	if err != nil {
		return nil, attachmentGetOutput{}, err
	}

	out := attachmentGetOutput{
		Path:        saved.Path,
		ContentType: saved.Attachment.ContentType,
		Filename:    saved.Attachment.Filename,
		Size:        len(saved.Data),
	}

	res := &sdk.CallToolResult{
		Content: []sdk.Content{
			&sdk.TextContent{
				Text: fmt.Sprintf("Saved sticker (%d bytes) to %s", len(saved.Data), saved.Path),
			},
		},
	}
	if inlineImage(out.ContentType) && len(saved.Data) <= maxInlineImage {
		res.Content = append(res.Content, &sdk.ImageContent{Data: saved.Data, MIMEType: out.ContentType})
	}

	return res, out, nil
}
