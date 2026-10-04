package cmd_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cwbudde/go-signal/cmd"
	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/go-signal/internal/signal/signaltest"
)

type refreshingLinkClient struct {
	signal.Client
}

func (c refreshingLinkClient) Link(ctx context.Context, name string, onURI func(string)) (signal.Account, error) {
	onURI("sgnl://linkdevice?uuid=expired&pub_key=old")

	account, err := c.Client.Link(ctx, name, onURI)
	if err != nil {
		return signal.Account{}, fmt.Errorf("refreshed link: %w", err)
	}

	return account, nil
}

func TestLinkRefreshOutput(t *testing.T) {
	t.Parallel()

	fake := &signaltest.Fake{LinkAs: *testAccount()}
	factory := func(ctx context.Context, opts signal.Options) (signal.Client, error) {
		client, err := fake.Factory(ctx, opts)
		if err != nil {
			return nil, fmt.Errorf("open fake: %w", err)
		}

		return refreshingLinkClient{Client: client}, nil
	}

	config := filepath.Join(t.TempDir(), "config.yaml")

	err := os.WriteFile(config, nil, 0o600)
	if err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer

	root := cmd.NewRootCmd(cmd.WithClientFactory(factory))
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{avatarConfigFlag, config, dataDirFlag, t.TempDir(), "link", "--sync-timeout", "0"})

	err = root.ExecuteContext(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(out.String(), "QR code refreshed; scan the newest code below.\n"+signaltest.LinkURI) {
		t.Fatalf("missing refresh notice before newest URI:\n%s", out.String())
	}

	golden(t, "link_refresh", out.String())

	if !fake.AllClosed() {
		t.Error("client was not closed")
	}

	if len(fake.Linked) != 1 {
		t.Fatalf("linked %d accounts, want one", len(fake.Linked))
	}
}
