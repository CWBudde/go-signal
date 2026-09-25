package mcp

import (
	"context"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/output"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// noInput is the input of tools without arguments.
type noInput struct{}

// addReadTools registers the tools that only read.
func addReadTools(server *sdk.Server, a *app.App) {
	sdk.AddTool(server, &sdk.Tool{
		Name:        "account_show",
		Title:       "Show account",
		Description: "Show the linked Signal account this server acts for: number, ACI, PNI, device.",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: new(false)},
	}, func(ctx context.Context, _ *sdk.CallToolRequest, _ noInput) (*sdk.CallToolResult, output.AccountJSON, error) {
		acc, err := a.AccountShow(ctx)
		if err != nil {
			return nil, output.AccountJSON{}, err //nolint:wrapcheck // app wraps it
		}

		return nil, output.NewAccountJSON(acc), nil
	})
}
