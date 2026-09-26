package app_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/go-signal/internal/signal/signaltest"
)

// restricted returns an App on an unconnected client of fake that may only send to entries.
func restricted(t *testing.T, fake *signaltest.Fake, entries ...string) *app.App {
	t.Helper()

	list, err := app.ParseAllowlist(entries)
	if err != nil {
		t.Fatalf("allowlist: %v", err)
	}

	client, err := fake.Factory(t.Context(), signal.Options{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	t.Cleanup(func() { _ = client.Close() })

	return app.New(client, app.WithAllowlist(list))
}

func TestAllowlistAllows(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		entries    []string
		recipients []string
	}{
		// A number entry allows the user by ACI, and the other way round.
		{"number", []string{aliceNumber}, []string{aliceACI}},
		{"aci", []string{aliceACI}, []string{aliceNumber}},
		{"username", []string{bobUsername}, []string{bobACI}},
		{"group", []string{app.GroupPrefix + groupID}, []string{app.GroupPrefix + groupID}},
		// self also allows our own number.
		{app.SelfRecipient, []string{app.SelfRecipient}, []string{testAccount().Number}},
		{"all", []string{app.AllowAll}, []string{carolACI, app.GroupPrefix + groupID}},
		// An entry that isn't on Signal is left out.
		{"not on signal", []string{"+4915100000000", bobACI}, []string{bobACI}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			res, err := restricted(t, groupFake(), tc.entries...).Send(t.Context(), app.SendRequest{
				Recipients: tc.recipients, Body: body,
			})
			if err != nil || res.Failed() != 0 {
				t.Fatalf("send: %+v, %v", res, err)
			}
		})
	}
}

func TestAllowlistRejects(t *testing.T) {
	t.Parallel()

	for _, entries := range [][]string{nil, {aliceNumber, app.SelfRecipient}} {
		fake := groupFake()
		a := restricted(t, fake, entries...)

		_, err := a.Send(t.Context(), app.SendRequest{
			Recipients: []string{aliceACI, bobUsername, app.GroupPrefix + groupID}, Body: body,
			Attachments: []string{writeFile(t, "notes", []byte("text"))},
		})
		if !errors.Is(err, app.ErrRecipientNotAllowed) || !strings.Contains(err.Error(), bobACI) ||
			!strings.Contains(err.Error(), groupID) {
			t.Errorf("%v: send: %v, want bob and the group rejected", entries, err)
		}

		_, err = a.React(t.Context(), app.ReactRequest{
			Recipients: []string{bobACI}, Target: bobACI + ":1000", Emoji: "👍",
		})
		if !errors.Is(err, app.ErrRecipientNotAllowed) {
			t.Errorf("%v: react: %v, want ErrRecipientNotAllowed", entries, err)
		}

		_, err = a.Delete(t.Context(), app.DeleteRequest{Recipients: []string{app.GroupPrefix + groupID}, Target: 1000})
		if !errors.Is(err, app.ErrRecipientNotAllowed) {
			t.Errorf("%v: delete: %v, want ErrRecipientNotAllowed", entries, err)
		}

		err = a.CheckRecipients(t.Context(), []string{bobACI})
		if !errors.Is(err, app.ErrRecipientNotAllowed) {
			t.Errorf("%v: check: %v, want ErrRecipientNotAllowed", entries, err)
		}

		if len(fake.Sent()) != 0 || len(fake.Uploaded()) != 0 {
			t.Errorf("%v: sent %+v, uploaded %+v; want nothing", entries, fake.Sent(), fake.Uploaded())
		}
	}
}

func TestParseAllowlist(t *testing.T) {
	t.Parallel()

	_, err := app.ParseAllowlist([]string{aliceNumber, "nobody", "group:x"})
	if !errors.Is(err, app.ErrInvalidRecipient) || !strings.Contains(err.Error(), `"nobody"`) ||
		!strings.Contains(err.Error(), `"group:x"`) {
		t.Errorf("got %v, want both invalid entries", err)
	}

	for _, test := range []struct {
		entries    []string
		all, empty bool
	}{{nil, false, true}, {[]string{" * "}, true, false}, {[]string{aliceNumber}, false, false}} {
		list, err := app.ParseAllowlist(test.entries)
		if err != nil || list.All() != test.all || list.Empty() != test.empty {
			t.Errorf("%q: all %v, empty %v, %v; want %v, %v",
				test.entries, list.All(), list.Empty(), err, test.all, test.empty)
		}
	}
}

func TestSendAttachDir(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	outside := writeFile(t, "secret", []byte("secret"))

	err := os.WriteFile(filepath.Join(dir, "notes"), []byte("text"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	err = os.Symlink(outside, filepath.Join(dir, "link"))
	if err != nil {
		t.Fatal(err)
	}

	fake := directory()
	a := sender(t, fake)

	send := func(path string) error {
		_, err := a.Send(t.Context(), app.SendRequest{
			Recipients: []string{aliceNumber}, Attachments: []string{path}, AttachDir: dir,
		})

		return err //nolint:wrapcheck // the test checks it
	}

	for _, path := range []string{"notes", filepath.Join(dir, "notes")} {
		err = send(path)
		if err != nil {
			t.Errorf("%s: %v", path, err)
		}
	}

	for _, path := range []string{"../secret", outside, "link", filepath.Join(dir, "..", "x")} {
		err = send(path)
		if !errors.Is(err, app.ErrOutsideAttachDir) {
			t.Errorf("%s: got %v, want ErrOutsideAttachDir", path, err)
		}
	}

	err = send("missing")
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("missing: got %v, want ErrNotExist", err)
	}

	if len(fake.Uploaded()) != 2 {
		t.Errorf("uploaded %d files, want 2", len(fake.Uploaded()))
	}
}
