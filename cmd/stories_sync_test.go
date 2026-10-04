package cmd_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/cwbudde/go-signal/internal/app"
)

var errStorySyncRejected = errors.New("transcript rejected")

func TestStoriesSendSyncFailure(t *testing.T) {
	t.Parallel()

	for _, format := range []string{formatPlain, formatJSON} {
		t.Run(format, func(t *testing.T) {
			t.Parallel()

			fake := storiesFake()
			fake.StorySyncErr = errUnreachable

			out, err := runSend(t, fake, "", "-o", format, "stories", "send", groupFlag, groupID, "-m", "Story")
			if !errors.Is(err, app.ErrSendFailed) || !strings.Contains(out, "recipient unreachable") || len(fake.Sent()) != 1 {
				t.Fatalf("sync failure lost: %s %v", out, err)
			}

			if format == formatPlain && !strings.Contains(out, "2 members") {
				t.Fatalf("sync failure hid accepted peers: %s", out)
			}

			golden(t, "stories_send_sync_failed_"+format, out)
		})
	}
}

func TestStoriesSendPeerAndSyncFailure(t *testing.T) {
	t.Parallel()

	fake := storiesFake()
	fake.StorySyncErr = errStorySyncRejected
	fake.SendFailures = map[string]error{carolACI: errUnreachable}

	out, err := runSend(t, fake, "", "stories", "send", groupFlag, groupID, "-m", "Story")
	if !errors.Is(err, app.ErrSendFailed) || !strings.Contains(out, "1 of 2 members failed") ||
		!strings.Contains(out, "recipient unreachable") || !strings.Contains(out, "transcript rejected") {
		t.Fatalf("peer/transcript results lost: %s %v", out, err)
	}

	golden(t, "stories_send_peer_sync_failed_plain", out)
}

func TestStoriesSendAllPeersAndSyncFailure(t *testing.T) {
	t.Parallel()

	fake := storiesFake()
	fake.StorySyncErr = errStorySyncRejected
	fake.SendFailures = map[string]error{aliceACI: errUnreachable, carolACI: errUnreachable}

	out, err := runSend(t, fake, "", "stories", "send", groupFlag, groupID, "-m", "Story")
	if !errors.Is(err, app.ErrSendFailed) || !strings.Contains(out, "2 of 2 members failed") ||
		!strings.Contains(out, aliceACI) || !strings.Contains(out, carolACI) ||
		!strings.Contains(out, "transcript rejected") {
		t.Fatalf("all peer/transcript failures lost: %s %v", out, err)
	}

	golden(t, "stories_send_all_peer_sync_failed_plain", out)
}
