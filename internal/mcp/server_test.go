package mcp_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/mcp"
	"github.com/cwbudde/go-signal/internal/output"
	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/go-signal/internal/signal/signaltest"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	testVersion     = "1.2.3"
	accountShowTool = "account_show"
)

func testAccount() signal.Account {
	return signal.Account{
		Number:     "+4915112345678",
		ACI:        "11111111-2222-3333-4444-555555555555",
		PNI:        "66666666-7777-8888-9999-000000000000",
		DeviceID:   2,
		DeviceName: "laptop",
		LinkedAt:   time.Date(2026, 9, 20, 12, 30, 0, 0, time.UTC),
	}
}

// connect starts a server on a client of fake and returns an MCP client session connected to it
// through the SDK's in-memory transport. Both end with the test. Like `mcp serve`, it connects
// the client first, if fake has an account, and receives its events into the inbox.
func connect(t *testing.T, fake *signaltest.Fake) *sdk.ClientSession {
	t.Helper()

	return connectWith(t, fake, mcp.Options{}, nil)
}

// connectWith is connect with server options (Version, Location and a DownloadDir are filled
// in) and client options.
func connectWith(
	t *testing.T, fake *signaltest.Fake, opts mcp.Options, clientOpts *sdk.ClientOptions,
) *sdk.ClientSession {
	t.Helper()

	client, err := fake.Factory(t.Context(), signal.Options{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	t.Cleanup(func() {
		err := client.Close()
		if err != nil {
			t.Errorf("close: %v", err)
		}
	})

	opts.Version, opts.Location = testVersion, time.UTC
	if opts.DownloadDir == "" {
		opts.DownloadDir = t.TempDir()
	}

	server := mcp.NewServer(app.New(client), opts)

	if len(fake.Linked) > 0 {
		receive(t, server, client)
	}

	serverTransport, clientTransport := sdk.NewInMemoryTransports()

	serverSession, err := server.Connect(t.Context(), serverTransport, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}

	t.Cleanup(func() { _ = serverSession.Close() })

	mcpClient := sdk.NewClient(&sdk.Implementation{Name: "test", Version: "0"}, clientOpts)

	session, err := mcpClient.Connect(t.Context(), clientTransport, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}

	t.Cleanup(func() { _ = session.Close() })

	return session
}

// receive connects client and receives its events into the server's inbox until the test ends.
func receive(t *testing.T, server *mcp.Server, client signal.Client) {
	t.Helper()

	err := client.Connect(t.Context())
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	received := make(chan error, 1)

	go func() { received <- server.Receive(ctx, client.Events()) }()

	t.Cleanup(func() {
		cancel()

		err := <-received
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Errorf("receive: %v", err)
		}
	})
}

func TestInitialize(t *testing.T) {
	t.Parallel()

	session := connect(t, &signaltest.Fake{Linked: []signal.Account{testAccount()}})

	init := session.InitializeResult()
	if init.ServerInfo.Name != mcp.Name || init.ServerInfo.Version != testVersion {
		t.Errorf("server info %+v, want %s %s", init.ServerInfo, mcp.Name, testVersion)
	}

	if init.Instructions == "" {
		t.Error("no instructions")
	}

	if init.Capabilities.Tools == nil {
		t.Error("tools capability not advertised")
	}
}

func TestListTools(t *testing.T) {
	t.Parallel()

	session := connect(t, &signaltest.Fake{Linked: []signal.Account{testAccount()}})

	res, err := session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}

	// The tools that change something: attachment_get writes a file, mark_read sends receipts.
	writes := map[string]bool{"attachment_get": true, "mark_read": true}

	names := make([]string, 0, len(res.Tools))
	for _, tool := range res.Tools {
		names = append(names, tool.Name)

		if tool.Annotations == nil || tool.Annotations.ReadOnlyHint == writes[tool.Name] {
			t.Errorf("%s: annotations %+v, want readOnlyHint %v", tool.Name, tool.Annotations, !writes[tool.Name])
		}

		if writes[tool.Name] && (tool.Annotations.DestructiveHint == nil || *tool.Annotations.DestructiveHint) {
			t.Errorf("%s: not annotated as non-destructive", tool.Name)
		}

		if tool.OutputSchema == nil {
			t.Errorf("%s: no output schema", tool.Name)
		}
	}

	want := []string{
		accountShowTool, "attachment_get", "contacts_list", "contacts_show", "groups_list", "groups_show",
		"identities_list", "mark_read", "messages_list", "messages_wait",
	}
	if !slices.Equal(names, want) {
		t.Errorf("tools %v, want %v", names, want)
	}
}

func TestAccountShow(t *testing.T) {
	t.Parallel()

	session := connect(t, &signaltest.Fake{Linked: []signal.Account{testAccount()}})

	res, err := session.CallTool(t.Context(), &sdk.CallToolParams{Name: accountShowTool})
	if err != nil {
		t.Fatalf("call: %v", err)
	}

	if res.IsError {
		t.Fatalf("tool error: %+v", res.Content)
	}

	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}

	var got output.AccountJSON

	err = json.Unmarshal(raw, &got)
	if err != nil {
		t.Fatalf("structured content %s: %v", raw, err)
	}

	if want := output.NewAccountJSON(testAccount()); got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}

	if len(res.Content) == 0 {
		t.Error("no text content for clients that ignore structured output")
	}
}

func TestAccountShowNotLinked(t *testing.T) {
	t.Parallel()

	session := connect(t, &signaltest.Fake{})

	res, err := session.CallTool(t.Context(), &sdk.CallToolParams{Name: accountShowTool})
	if err != nil {
		t.Fatalf("call: %v", err)
	}

	if !res.IsError {
		t.Fatalf("got %+v, want a tool error", res)
	}
}

func TestServeEOF(t *testing.T) {
	t.Parallel()

	client, err := (&signaltest.Fake{}).Factory(t.Context(), signal.Options{})
	if err != nil {
		t.Fatal(err)
	}

	defer client.Close()

	// A client that closes stdin right away ends the server without an error.
	err = mcp.Serve(t.Context(), app.New(client), nil, mcp.Options{}, strings.NewReader(""), io.Discard)
	if err != nil {
		t.Fatalf("serve: %v", err)
	}
}
