//go:build integration && (cgo || libsignal_go)

package signal_test

import (
	"os"
	"testing"

	"github.com/cwbudde/go-signal/internal/signal"
)

// TestIntegrationEdit sends and edits fresh messages only. Delivery receipts prove transport,
// not that the peer rendered the edit; inspect the peer's phone to verify that separately.
func TestIntegrationEdit(t *testing.T) { //nolint:paralleltest // one live account
	if os.Getenv("GOSIGNAL_IT_EDIT") != "1" {
		t.Skip("GOSIGNAL_IT_EDIT not set to 1")
	}

	env := connectLive(t)
	env.stepReceive(t)

	peers, err := env.client.Resolve(t.Context(), []signal.Recipient{{Number: env.peerNumber}})
	if err != nil {
		t.Fatal(err)
	}

	env.peer = peers[0]

	t.Run("Self", func(t *testing.T) { //nolint:paralleltest // one live account
		editAndCheck(t, env, signal.SendRequest{Recipients: []signal.Recipient{{ACI: env.acc.ACI}}}, false)
	})
	t.Run("Direct", func(t *testing.T) { //nolint:paralleltest // one live account
		editAndCheck(t, env, signal.SendRequest{Recipients: []signal.Recipient{env.peer}}, true)
	})
	t.Run("Group", func(t *testing.T) { //nolint:paralleltest // one live account
		ref := os.Getenv("GOSIGNAL_IT_GROUP")
		if ref == "" {
			t.Skip("GOSIGNAL_IT_GROUP not set")
		}

		group, err := env.client.Group(t.Context(), ref)
		if err != nil {
			t.Fatal(err)
		}

		membership, _ := group.MembershipOf(env.peer.ACI)
		if membership != signal.MembershipMember {
			t.Fatal("peer must be a full group member")
		}

		editAndCheck(t, env, signal.SendRequest{GroupID: group.ID}, true)
	})
}

func editAndCheck(t *testing.T, env *liveEnv, req signal.SendRequest, receipt bool) {
	t.Helper()

	req.Body = body("edit test: original")

	original := sendAndCheck(t, env.client, req)
	if receipt {
		waitForReceipt(t, env.events, env.receiptTimeout, env.peer.ACI, original.Timestamp)
	}

	req.Body = body("edit test: corrected")
	req.EditTarget = original.Timestamp

	edited := sendAndCheck(t, env.client, req)
	if edited.Timestamp <= original.Timestamp {
		t.Fatal("edit timestamp did not advance")
	}

	if receipt {
		waitForReceipt(t, env.events, env.receiptTimeout, env.peer.ACI, edited.Timestamp)
	}

	t.Logf("original timestamp %d, edit timestamp %d", original.Timestamp, edited.Timestamp)
}
