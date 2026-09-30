package app_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/go-signal/internal/signal/signaltest"
	"github.com/google/uuid"
)

const (
	pinTestOperation       = "pin"
	unpinTestOperation     = "unpin"
	pinUnknownAuthorTarget = "+19999999999:100"
	pinBrokenValue         = "broken-pin-value"
)

// pinClient overrides only external operations whose failures the fake cannot model.
type pinClient struct {
	signal.Client

	group   func(context.Context, string) (signal.Group, error)
	resolve func(context.Context, []signal.Recipient) ([]signal.Recipient, error)
	inbox   func(context.Context, signal.InboxQuery) ([]signal.InboxEntry, error)
}

func (c *pinClient) Group(ctx context.Context, ref string) (signal.Group, error) {
	if c.group != nil {
		return c.group(ctx, ref)
	}

	return c.Client.Group(ctx, ref) //nolint:wrapcheck // test boundary preserves errors
}

func (c *pinClient) Resolve(ctx context.Context, users []signal.Recipient) ([]signal.Recipient, error) {
	if c.resolve != nil {
		return c.resolve(ctx, users)
	}

	return c.Client.Resolve(ctx, users) //nolint:wrapcheck // test boundary preserves errors
}

func (c *pinClient) InboxList(ctx context.Context, query signal.InboxQuery) ([]signal.InboxEntry, error) {
	if c.inbox != nil {
		return c.inbox(ctx, query)
	}

	return c.Client.InboxList(ctx, query) //nolint:wrapcheck // test boundary preserves errors
}

func pinSender(t *testing.T, fake *signaltest.Fake) (*app.App, *pinClient) {
	t.Helper()

	client, err := fake.Factory(t.Context(), signal.Options{})
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = client.Close() })

	wrapper := &pinClient{Client: client}

	return app.New(wrapper, app.WithClock(func() time.Time { return time.UnixMilli(sentAt) })), wrapper
}

func pinGroup(role signal.GroupRole, editable bool) signal.Group {
	return signal.Group{
		Members: []signal.GroupMember{
			{Recipient: signal.Recipient{ACI: testAccount().ACI}, Role: role},
			{Recipient: bobUser(), Role: signal.GroupRoleMember},
		},
		MembersCanEditAttributes: editable,
	}
}

func TestPinUnpinPreflight(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		req  app.PinRequest
	}{
		{"recipients", app.PinRequest{Target: aliceACI + ":100", Forever: true}},
		{"recipient syntax", app.PinRequest{
			Recipients: []string{pinBrokenValue}, Target: aliceACI + ":100", Forever: true,
		}},
		{"target syntax", app.PinRequest{Recipients: []string{aliceACI}, Target: pinBrokenValue, Forever: true}},
		{"zero target", app.PinRequest{Recipients: []string{aliceACI}, Target: aliceACI + ":0", Forever: true}},
		{"nil author", app.PinRequest{Recipients: []string{aliceACI}, Target: uuid.Nil.String() + ":100", Forever: true}},
		{"no duration", app.PinRequest{Recipients: []string{aliceACI}, Target: aliceACI + ":100"}},
		{"both durations", app.PinRequest{
			Recipients: []string{aliceACI}, Target: aliceACI + ":100", DurationSeconds: 1, Forever: true,
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			fake := directory()

			if test.req.Check() == nil {
				t.Fatal("invalid request accepted")
			}

			_, err := sender(t, fake).Pin(t.Context(), test.req)
			if err == nil || len(fake.Connects()) != 0 || len(fake.Sent()) != 0 {
				t.Fatalf("invalid pin connected or sent: %v", err)
			}
		})
	}

	for _, req := range []app.UnpinRequest{
		{Target: aliceACI + ":100"},
		{Recipients: []string{aliceACI}, Target: uuid.Nil.String() + ":100"},
		{Recipients: []string{aliceACI}, Target: pinBrokenValue},
	} {
		fake := directory()

		_, err := sender(t, fake).Unpin(t.Context(), req)
		if req.Check() == nil || err == nil || len(fake.Connects()) != 0 {
			t.Fatalf("invalid unpin %+v: %v", req, err)
		}
	}
}

//nolint:cyclop // Independent assertions check delivery metadata and absence of retries.
func TestPinUnpinResolutionAndPartialOutcomes(t *testing.T) {
	t.Parallel()

	fake := directory()
	fake.GroupInfo = map[string]signal.Group{groupID: pinGroup(signal.GroupRoleAdmin, false)}
	fake.SendFailures = map[string]error{bobACI: signal.ErrNotOnSignal}
	a := sender(t, fake)

	got, err := a.Pin(t.Context(), app.PinRequest{
		Recipients: []string{aliceNumber, app.SelfRecipient, app.GroupPrefix + groupID},
		Target:     bobUsername + ":100", DurationSeconds: 60,
	})
	if !errors.Is(err, app.ErrSendFailed) || got.TargetAuthor.ACI != bobACI || got.Failed() != 1 || len(got.Results) != 3 {
		t.Fatalf("pin %+v: %v", got, err)
	}

	removed, err := a.Unpin(t.Context(), app.UnpinRequest{Recipients: []string{aliceACI}, Target: "self:100"})
	if err != nil || removed.TargetAuthor.ACI != testAccount().ACI || removed.Timestamp <= got.Timestamp {
		t.Fatalf("unpin %+v: %v", removed, err)
	}

	sent := fake.Sent()
	if len(sent) != 3 || sent[0].Pin == nil || sent[1].Pin == nil || sent[2].Unpin == nil {
		t.Fatalf("requests or retries %+v", sent)
	}

	if sent[0].Pin.TargetTimestamp != 100 || sent[0].Pin.DurationSeconds != 60 || sent[0].Pin.Forever ||
		sent[2].Unpin.TargetAuthor.ACI != testAccount().ACI || got.Operation != pinTestOperation ||
		removed.Operation != unpinTestOperation {
		t.Fatalf("payloads %+v, results %+v %+v", sent, got, removed)
	}

	if len(fake.Connects()) != 1 {
		t.Fatalf("connects %+v", fake.Connects())
	}
}

func TestPinUnpinFreshGroupPermissionsBeforeAnySend(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name  string
		group signal.Group
		want  error
	}{
		{"member allowed", pinGroup(signal.GroupRoleMember, true), nil},
		{"member denied", pinGroup(signal.GroupRoleMember, false), signal.ErrGroupPermission},
		{"admin", pinGroup(signal.GroupRoleAdmin, false), nil},
		{"absent", signal.Group{MembersCanEditAttributes: true}, signal.ErrNotAMember},
		{"pending admin", signal.Group{
			Pending: []signal.PendingMember{{
				Recipient: signal.Recipient{ACI: testAccount().ACI}, Role: signal.GroupRoleAdmin,
			}},
			MembersCanEditAttributes: true,
		}, signal.ErrNotAMember},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			fake := directory()
			fake.GroupInfo = map[string]signal.Group{groupID: test.group}
			a := sender(t, fake)
			args := []string{aliceACI, app.GroupPrefix + groupID}

			_, err := a.Pin(t.Context(), app.PinRequest{Recipients: args, Target: aliceACI + ":100", Forever: true})
			if !errors.Is(err, test.want) {
				t.Fatalf("permission %v, want %v", err, test.want)
			}

			_, err = a.Unpin(t.Context(), app.UnpinRequest{Recipients: args, Target: aliceACI + ":100"})
			if !errors.Is(err, test.want) {
				t.Fatalf("unpin permission %v, want %v", err, test.want)
			}

			if test.want != nil && len(fake.Sent()) != 0 {
				t.Fatalf("sent before checking later group: %+v", fake.Sent())
			}
		})
	}
}

func TestPinAllAllowlistsPrecedeGroupFetchAndAuthor(t *testing.T) {
	t.Parallel()

	fake := directory()
	fake.GroupErrs = map[string]error{groupID: errBoom}

	_, err := restricted(t, fake, aliceACI).Pin(t.Context(), app.PinRequest{
		Recipients: []string{aliceACI, app.GroupPrefix + groupID}, Target: pinUnknownAuthorTarget, Forever: true,
	})
	if !errors.Is(err, app.ErrRecipientNotAllowed) || len(fake.Sent()) != 0 {
		t.Fatalf("allowlist ordering %v, sends %+v", err, fake.Sent())
	}

	fake = directory()
	fake.GroupErrs = map[string]error{groupID: errBoom}

	_, err = sender(t, fake).Pin(t.Context(), app.PinRequest{
		Recipients: []string{aliceACI, app.GroupPrefix + groupID}, Target: pinUnknownAuthorTarget, Forever: true,
	})
	if !errors.Is(err, errBoom) || len(fake.Sent()) != 0 {
		t.Fatalf("group fetch ordering %v", err)
	}
}

func TestPinDistinctGroupsAndFreshRecheck(t *testing.T) {
	t.Parallel()

	fake := directory()
	fake.GroupInfo = map[string]signal.Group{groupID: pinGroup(signal.GroupRoleMember, true)}
	a, client := pinSender(t, fake)
	fetched := 0
	client.group = func(ctx context.Context, ref string) (signal.Group, error) {
		fetched++

		return client.Client.Group(ctx, ref)
	}
	req := app.PinRequest{
		Recipients: []string{aliceACI, app.GroupPrefix + groupID, app.GroupPrefix + groupID},
		Target:     aliceACI + ":100", Forever: true,
	}

	_, err := a.Pin(t.Context(), req)
	if err != nil || fetched != 1 {
		t.Fatalf("deduplicated fetches %d, %v", fetched, err)
	}

	fake.GroupInfo[groupID] = pinGroup(signal.GroupRoleMember, false)

	_, err = a.Pin(t.Context(), req)
	if !errors.Is(err, signal.ErrGroupPermission) || fetched != 2 || len(fake.Sent()) != 2 {
		t.Fatalf("stale permissions: %d fetches, %d sends, %v", fetched, len(fake.Sent()), err)
	}
}

func TestPinAuthorLookupFailuresBeforeAnySend(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name  string
		users []signal.Recipient
		err   error
		want  error
	}{
		{"lookup", nil, errBoom, errBoom},
		{"nil ACI", []signal.Recipient{{ACI: uuid.Nil.String()}}, nil, signal.ErrInvalidPin},
		{"empty ACI", []signal.Recipient{{}}, nil, signal.ErrInvalidPin},
		{"missing result", nil, nil, signal.ErrInvalidPin},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			fake := directory()
			a, client := pinSender(t, fake)
			client.resolve = func(context.Context, []signal.Recipient) ([]signal.Recipient, error) {
				return test.users, test.err
			}

			_, err := a.Pin(t.Context(), app.PinRequest{
				Recipients: []string{aliceACI}, Target: bobUsername + ":100", Forever: true,
			})
			if !errors.Is(err, test.want) || len(fake.Sent()) != 0 {
				t.Fatalf("lookup %v, sends %+v", err, fake.Sent())
			}
		})
	}
}
