package mcp

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/output"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// noInput is the input of tools without arguments.
type noInput struct{}

type contactsListInput struct {
	Query   string `json:"query,omitempty"   jsonschema:"list only users whose name, number or ACI contains this"`
	Blocked bool   `json:"blocked,omitempty" jsonschema:"list only blocked users"`
}

type contactsListOutput struct {
	Contacts []output.ContactJSON `json:"contacts"`
}

type contactsShowInput struct {
	Recipient string `json:"recipient" jsonschema:"the user: E.164 number (+4915112345678), ACI, @username or self"`
}

type groupsListOutput struct {
	Groups []output.GroupJSON `json:"groups"`
}

type groupsShowInput struct {
	Group string `json:"group" jsonschema:"the group: its ID from groups_list, or its title if only one group has it"`
}

type identitiesListInput struct {
	Recipient string `json:"recipient,omitempty" jsonschema:"list only this user's key: E.164 number, ACI or @username"`
}

type identitiesListOutput struct {
	Identities []output.IdentityJSON `json:"identities"`
}

// tools holds what the tool handlers need.
type tools struct {
	app   *app.App
	inbox *app.Inbox
	loc   *time.Location
	// dir is where attachment_get saves attachments.
	dir    string
	logger *slog.Logger
}

// readOnly are the annotations of tools that only read our own account's data.
func readOnly() *sdk.ToolAnnotations {
	return &sdk.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: new(false)}
}

// addReadTools registers the tools that only read. Each returns a docs/json.md object as
// structured content and the CLI's plain output as text, for clients that ignore structured
// content.
func addReadTools(server *sdk.Server, handlers *tools) {
	sdk.AddTool(server, &sdk.Tool{
		Name:        "account_show",
		Title:       "Show account",
		Description: "Show the linked Signal account this server acts for: number, ACI, PNI, device.",
		Annotations: readOnly(),
	}, handlers.accountShow)

	sdk.AddTool(server, &sdk.Tool{
		Name:  "contacts_list",
		Title: "List contacts",
		Description: "List the users known from the phone's contacts, the storage service and received messages, " +
			"sorted by name, with number and ACI. The name is the nickname, else the phone's contact name, else " +
			"the profile name. Use query to find a user by name or number.",
		Annotations: readOnly(),
	}, handlers.contactsList)

	sdk.AddTool(server, &sdk.Tool{
		Name:  "contacts_show",
		Title: "Show contact",
		Description: "Show what is known about one user: names, number, ACI, PNI, whether they are blocked and " +
			"whether their message request was accepted.",
		Annotations: readOnly(),
	}, handlers.contactsShow)

	sdk.AddTool(server, &sdk.Tool{
		Name:  "groups_list",
		Title: "List groups",
		Description: "List the known groups with their current state from the server: ID, title, our membership " +
			"and role, members with their names and roles.",
		Annotations: readOnly(),
	}, handlers.groupsList)

	sdk.AddTool(server, &sdk.Tool{
		Name:  "groups_show",
		Title: "Show group",
		Description: "Show one group with its current state from the server: title, description, settings, " +
			"members, invited and requesting members, with their names.",
		Annotations: readOnly(),
	}, handlers.groupsShow)

	sdk.AddTool(server, &sdk.Tool{
		Name:  "identities_list",
		Title: "List identity keys",
		Description: "List the stored identity keys of other users with their trust level (trusted on first use, " +
			"verified or untrusted after a key change). Sending to a user with an untrusted key fails.",
		Annotations: readOnly(),
	}, handlers.identitiesList)
}

func (t *tools) accountShow(
	ctx context.Context, _ *sdk.CallToolRequest, _ noInput,
) (*sdk.CallToolResult, output.AccountJSON, error) {
	acc, err := t.app.AccountShow(ctx)
	if err != nil {
		return nil, output.AccountJSON{}, err //nolint:wrapcheck // app wraps it
	}

	res, err := t.plain(app.Names{}, func(p *output.Printer) error { return p.Account(acc) })

	return res, output.NewAccountJSON(acc), err
}

func (t *tools) contactsList(
	ctx context.Context, _ *sdk.CallToolRequest, in contactsListInput,
) (*sdk.CallToolResult, contactsListOutput, error) {
	contacts, err := t.app.ContactsList(ctx, app.ContactsListRequest{Query: in.Query, Blocked: in.Blocked})
	if err != nil {
		return nil, contactsListOutput{}, err //nolint:wrapcheck // app wraps it
	}

	out := contactsListOutput{Contacts: make([]output.ContactJSON, 0, len(contacts))}
	for _, contact := range contacts {
		out.Contacts = append(out.Contacts, output.NewContactJSON(contact))
	}

	res, err := t.plain(app.Names{}, func(p *output.Printer) error { return p.Contacts(contacts) })

	return res, out, err
}

func (t *tools) contactsShow(
	ctx context.Context, _ *sdk.CallToolRequest, in contactsShowInput,
) (*sdk.CallToolResult, output.ContactJSON, error) {
	contact, err := t.app.ContactsShow(ctx, in.Recipient)
	if err != nil {
		return nil, output.ContactJSON{}, err //nolint:wrapcheck // app wraps it
	}

	res, err := t.plain(app.Names{}, func(p *output.Printer) error { return p.Contact(contact) })

	return res, output.NewContactJSON(contact), err
}

func (t *tools) groupsList(
	ctx context.Context, _ *sdk.CallToolRequest, _ noInput,
) (*sdk.CallToolResult, groupsListOutput, error) {
	groups, err := t.app.GroupsList(ctx)
	if err != nil {
		return nil, groupsListOutput{}, err //nolint:wrapcheck // app wraps it
	}

	names := t.names(ctx)

	out := groupsListOutput{Groups: make([]output.GroupJSON, 0, len(groups))}
	for _, group := range groups {
		out.Groups = append(out.Groups, output.NewGroupJSON(group, names))
	}

	res, err := t.plain(names, func(p *output.Printer) error { return p.Groups(groups) })

	return res, out, err
}

func (t *tools) groupsShow(
	ctx context.Context, _ *sdk.CallToolRequest, in groupsShowInput,
) (*sdk.CallToolResult, output.GroupJSON, error) {
	group, err := t.app.GroupsShow(ctx, in.Group)
	if err != nil {
		return nil, output.GroupJSON{}, err //nolint:wrapcheck // app wraps it
	}

	names := t.names(ctx)

	res, err := t.plain(names, func(p *output.Printer) error { return p.Group(group) })

	return res, output.NewGroupJSON(group, names), err
}

func (t *tools) identitiesList(
	ctx context.Context, _ *sdk.CallToolRequest, in identitiesListInput,
) (*sdk.CallToolResult, identitiesListOutput, error) {
	ids, err := t.app.IdentitiesList(ctx, app.IdentitiesListRequest{Recipient: in.Recipient})
	if err != nil {
		return nil, identitiesListOutput{}, err //nolint:wrapcheck // app wraps it
	}

	out := identitiesListOutput{Identities: make([]output.IdentityJSON, 0, len(ids))}
	for _, id := range ids {
		out.Identities = append(out.Identities, output.NewIdentityJSON(id))
	}

	res, err := t.plain(t.names(ctx), func(p *output.Printer) error { return p.Identities(ids) })

	return res, out, err
}

// names returns the names of the contacts and groups in the store. They only add to the output,
// so when they can't be loaded, the tools go on without them.
func (t *tools) names(ctx context.Context) app.Names {
	names, err := t.app.Names(ctx)
	if err != nil {
		t.logger.DebugContext(ctx, "names not loaded", "error", err)
	}

	return names
}

// plain returns a tool result whose text content is what render prints in plain format, as the
// CLI does, with the users named from names.
func (t *tools) plain(names app.Names, render func(*output.Printer) error) (*sdk.CallToolResult, error) {
	var buf bytes.Buffer

	printer := output.New(&buf, output.Plain, t.loc)
	printer.SetNames(names)

	err := render(printer)
	if err != nil {
		return nil, err
	}

	text := strings.TrimRight(buf.String(), "\n")

	return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: text}}}, nil
}
