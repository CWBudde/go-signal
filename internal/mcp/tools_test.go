package mcp_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/internal/output"
	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/go-signal/internal/signal/signaltest"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	aliceACI    = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	aliceNumber = "+4915199999999"
	bobACI      = "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
	alice       = "Alice"
	recipient   = "recipient"
	familyID    = "ZmFtaWx5LWlkLWZhbWlseS1pZC1mYW1pbHktaWQtZmE="
)

// toolsFake returns a fake whose account knows Alice (in the phone's contacts) and Bob (blocked,
// by profile name), is admin of "Family" with Alice and stores Alice's identity key.
func toolsFake() *signaltest.Fake {
	own := testAccount().ACI
	groups := map[string]signal.Group{
		familyID: {ID: familyID, Title: "Family", Revision: 3, Members: []signal.GroupMember{
			{Recipient: signal.Recipient{ACI: own}, Role: signal.GroupRoleAdmin},
			{Recipient: signal.Recipient{ACI: aliceACI}, Role: signal.GroupRoleMember},
		}},
	}

	return &signaltest.Fake{
		Linked:    []signal.Account{testAccount()},
		Directory: []signal.Recipient{{ACI: aliceACI, Number: aliceNumber}},
		Contacts: []signal.Contact{
			{Recipient: signal.Recipient{ACI: aliceACI, Number: aliceNumber}, ContactName: alice},
			{Recipient: signal.Recipient{ACI: bobACI}, ProfileName: "Bob", Blocked: true},
		},
		GroupInfo:       groups,
		GroupTitleCache: signaltest.CachedTitles(groups),
		Identities: []signal.Identity{{
			Recipient:   signal.Recipient{ACI: aliceACI, Number: aliceNumber},
			Fingerprint: "05" + strings.Repeat("ab", 32),
			Trust:       signal.TrustUnverified,
			FirstSeen:   time.Date(2026, 9, 21, 8, 0, 0, 0, time.UTC),
		}},
	}
}

// call calls the tool with args and decodes its structured content into out. It fails the test
// on a tool error and when there is no text content.
func call(t *testing.T, session *sdk.ClientSession, name string, args map[string]any, out any) string {
	t.Helper()

	return decode(t, callRaw(t, session, name, args), out)
}

// decode decodes the structured content of res into out and returns its text, like call.
func decode(t *testing.T, res *sdk.CallToolResult, out any) string {
	t.Helper()

	if res.IsError {
		t.Fatalf("tool error: %s", text(res))
	}

	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}

	err = json.Unmarshal(raw, out)
	if err != nil {
		t.Fatalf("structured content %s: %v", raw, err)
	}

	txt := text(res)
	if txt == "" {
		t.Error("no text content for clients that ignore structured output")
	}

	return txt
}

func callRaw(t *testing.T, session *sdk.ClientSession, name string, args map[string]any) *sdk.CallToolResult {
	t.Helper()

	res, err := session.CallTool(t.Context(), &sdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}

	return res
}

func text(res *sdk.CallToolResult) string {
	var out strings.Builder

	for _, content := range res.Content {
		if txt, ok := content.(*sdk.TextContent); ok {
			out.WriteString(txt.Text)
		}
	}

	return out.String()
}

// TestContactsList answers "what is Alice's number?".
func TestContactsList(t *testing.T) {
	t.Parallel()

	session := connect(t, toolsFake())

	var got struct {
		Contacts []output.ContactJSON `json:"contacts"`
	}

	txt := call(t, session, "contacts_list", map[string]any{"query": "alice"}, &got)

	if len(got.Contacts) != 1 || got.Contacts[0].Number != aliceNumber || got.Contacts[0].Name != alice {
		t.Errorf("contacts %+v, want Alice with her number", got.Contacts)
	}

	if !strings.Contains(txt, aliceNumber) {
		t.Errorf("text %q lacks Alice's number", txt)
	}

	call(t, session, "contacts_list", map[string]any{"blocked": true}, &got)

	if len(got.Contacts) != 1 || got.Contacts[0].ACI != bobACI || !got.Contacts[0].Blocked {
		t.Errorf("blocked contacts %+v, want Bob", got.Contacts)
	}

	call(t, session, "contacts_list", map[string]any{"query": "nobody"}, &got)

	if got.Contacts == nil || len(got.Contacts) != 0 {
		t.Errorf("no match: contacts %+v, want an empty list", got.Contacts)
	}
}

func TestContactsShow(t *testing.T) {
	t.Parallel()

	session := connect(t, toolsFake())

	var got output.ContactJSON

	call(t, session, "contacts_show", map[string]any{recipient: aliceNumber}, &got)

	if got.ACI != aliceACI || got.ContactName != alice {
		t.Errorf("contact %+v, want Alice", got)
	}

	res := callRaw(t, session, "contacts_show", map[string]any{recipient: "+4915100000000"})
	if !res.IsError {
		t.Errorf("unknown user: got %+v, want a tool error", res)
	}

	res = callRaw(t, session, "contacts_show", nil)
	if !res.IsError {
		t.Errorf("no recipient: got %+v, want a tool error", res)
	}
}

// TestGroups answers "who is in group Family?" on a client that is connected already, like
// the one of `mcp serve`.
func TestGroups(t *testing.T) {
	t.Parallel()

	session := connect(t, toolsFake())

	var list struct {
		Groups []output.GroupJSON `json:"groups"`
	}

	call(t, session, "groups_list", nil, &list)

	if len(list.Groups) != 1 || list.Groups[0].ID != familyID || list.Groups[0].Role != "admin" {
		t.Fatalf("groups %+v, want Family with us as admin", list.Groups)
	}

	var group output.GroupJSON

	txt := call(t, session, "groups_show", map[string]any{"group": "family"}, &group)

	if group.ID != familyID || len(group.Members) != 2 {
		t.Fatalf("group %+v, want Family with two members", group)
	}

	raw, err := json.Marshal(group.Members[1])
	if err != nil {
		t.Fatal(err)
	}

	// The members' names come from the contacts.
	if !strings.Contains(string(raw), `"name":"Alice"`) || !strings.Contains(txt, alice) {
		t.Errorf("member %s, text %q: want Alice named", raw, txt)
	}

	res := callRaw(t, session, "groups_show", map[string]any{"group": "Club"})
	if !res.IsError {
		t.Errorf("unknown group: got %+v, want a tool error", res)
	}
}

func TestIdentitiesList(t *testing.T) {
	t.Parallel()

	session := connect(t, toolsFake())

	var got struct {
		Identities []output.IdentityJSON `json:"identities"`
	}

	call(t, session, "identities_list", map[string]any{recipient: aliceACI}, &got)

	if len(got.Identities) != 1 || got.Identities[0].ACI != aliceACI || got.Identities[0].Trust != "trusted-unverified" {
		t.Errorf("identities %+v, want Alice's unverified key", got.Identities)
	}

	call(t, session, "identities_list", map[string]any{recipient: bobACI}, &got)

	if got.Identities == nil || len(got.Identities) != 0 {
		t.Errorf("no key stored: identities %+v, want an empty list", got.Identities)
	}
}
