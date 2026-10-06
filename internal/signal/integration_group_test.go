//go:build integration && (cgo || libsignal_go)

package signal_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/internal/signal"
)

// TestIntegrationRenameGroup changes only the dedicated fixture and restores its title.
func TestIntegrationRenameGroup(t *testing.T) { //nolint:paralleltest // mutates a live group on one account
	if os.Getenv("GOSIGNAL_IT_RENAME_GROUP") != "1" {
		t.Skip("GOSIGNAL_IT_RENAME_GROUP not set to 1")
	}

	ref := os.Getenv("GOSIGNAL_IT_GROUP")
	if ref == "" {
		t.Fatal("GOSIGNAL_IT_GROUP must name the dedicated test group")
	}

	env := connectLive(t)
	env.stepReceive(t)

	group, err := env.client.Group(t.Context(), ref)
	if err != nil {
		t.Fatalf("Group: %v", err)
	}

	peers, err := env.client.Resolve(t.Context(), []signal.Recipient{{Number: env.peerNumber}})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	checkCreatedGroup(t, group, env.acc.ACI, peers[0].ACI)

	if t.Failed() {
		t.FailNow()
	}

	cleanupGroupTitle(t, env.client, group)

	title := group.Title + " · rename test 🐶"

	renamed, err := env.client.RenameGroup(t.Context(), group.ID, title)
	if err != nil {
		t.Fatalf("RenameGroup: %v", err)
	}

	checkRenamedGroup(t, env.client, group, renamed, title)
}

func cleanupGroupTitle(t *testing.T, client signal.Client, group signal.Group) {
	t.Helper()

	// Register before the write: even a reported failure can follow a server-side success.
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		_, err := client.RenameGroup(ctx, group.ID, group.Title)
		if err != nil {
			t.Errorf("restore original title %q for group %s: %v", group.Title, group.ID, err)

			return
		}

		restored, err := signal.FreshIntegrationGroup(ctx, client, group.ID)
		if err != nil || restored.Title != group.Title {
			t.Errorf("verify restored title: %q, %v; want %q", restored.Title, err, group.Title)
		}
	})
}

func checkRenamedGroup(t *testing.T, client signal.Client, group, renamed signal.Group, title string) {
	t.Helper()

	fresh, err := signal.FreshIntegrationGroup(t.Context(), client, group.ID)
	if err != nil || fresh.Title != title || fresh.Revision != renamed.Revision || fresh.Revision <= group.Revision {
		t.Fatalf("server group = %q revision %d, %v; want %q revision %d",
			fresh.Title, fresh.Revision, err, title, renamed.Revision)
	}

	unchanged, err := client.RenameGroup(t.Context(), group.ID, title)
	if err != nil || unchanged.Revision != fresh.Revision {
		t.Fatalf("unchanged title revision %d, %v; want %d", unchanged.Revision, err, fresh.Revision)
	}

	titles, err := client.GroupTitles(t.Context())
	if err != nil || titles[group.ID].Title != title {
		t.Errorf("cached title = %q, %v; want %q", titles[group.ID].Title, err, title)
	}
}

// TestIntegrationCreateGroup is a one-time fixture setup, separate from the repeatable suite.
func TestIntegrationCreateGroup(t *testing.T) { //nolint:paralleltest // creates a live group on one account
	if os.Getenv("GOSIGNAL_IT_CREATE_GROUP") != "1" {
		t.Skip("GOSIGNAL_IT_CREATE_GROUP not set to 1")
	}

	if os.Getenv("GOSIGNAL_IT_GROUP") != "" {
		t.Fatal("GOSIGNAL_IT_GROUP already set; reuse it instead of creating another group")
	}

	env := connectLive(t)
	env.stepReceive(t)

	peers, err := env.client.Resolve(t.Context(), []signal.Recipient{{Number: env.peerNumber}})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	created, err := env.client.CreateGroup(t.Context(), signal.CreateGroupOptions{
		Title: "go-signal integration test", Members: peers,
	})

	groupID := created.ID
	if groupID != "" {
		t.Logf("GOSIGNAL_IT_GROUP=%s (retain for both backends; inspect before retrying creation)", groupID)
	}

	if err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}

	group, err := signal.FreshIntegrationGroup(t.Context(), env.client, groupID)
	if err != nil {
		t.Fatalf("Group: %v", err)
	}

	checkCreatedGroup(t, group, env.acc.ACI, peers[0].ACI)
}

func checkCreatedGroup(t *testing.T, group signal.Group, ownACI, peerACI string) {
	t.Helper()

	if len(group.Members) != 2 {
		t.Fatalf("group has %d full members, want 2; setup incomplete", len(group.Members))
	}

	for aci, want := range map[string]signal.GroupRole{ownACI: signal.GroupRoleAdmin, peerACI: signal.GroupRoleMember} {
		membership, role := group.MembershipOf(aci)
		if membership != signal.MembershipMember || role != want {
			t.Errorf("member %s: membership %v, role %v; want full member with role %v", aci, membership, role, want)
		}
	}
}

// TestIntegrationLeaveGroup leaves a disposable group for good; it can't be undone from here.
func TestIntegrationLeaveGroup(t *testing.T) { //nolint:paralleltest // leaves a live group on one account
	ref := os.Getenv("GOSIGNAL_IT_LEAVE_GROUP")
	if ref == "" {
		t.Skip("GOSIGNAL_IT_LEAVE_GROUP not set")
	}

	if ref == os.Getenv("GOSIGNAL_IT_GROUP") {
		t.Fatal("GOSIGNAL_IT_LEAVE_GROUP must name a disposable group, not the test group")
	}

	env := connectLive(t)
	env.stepReceive(t)

	group, err := env.client.Group(t.Context(), ref)
	if err != nil {
		t.Fatalf("Group: %v", err)
	}

	membership, role := group.MembershipOf(env.acc.ACI)
	if membership != signal.MembershipMember {
		t.Fatalf("we are %v of group %q, want a full member", membership, group.Title)
	}

	t.Logf("leaving group %q (%s) as %v at revision %d", group.Title, group.ID, role, group.Revision)

	left, err := env.client.LeaveGroup(t.Context(), group.ID, signal.LeaveOptions{})
	if err != nil {
		t.Fatalf("LeaveGroup: %v", err)
	}

	if left.Group.Membership != signal.MembershipMember || left.Group.LeftAt.IsZero() || left.Revision <= group.Revision {
		t.Errorf("LeaveGroup = membership %v, left at %s, revision %d; want member, left now, after %d",
			left.Group.Membership, left.Group.LeftAt, left.Revision, group.Revision)
	}

	checkLeftGroup(t, env.client, env.acc.ACI, group.ID)
}

// checkLeftGroup checks that the cache remembers leaving and that the server no longer shows
// us the group (or shows us as no member).
func checkLeftGroup(t *testing.T, client signal.Client, ownACI, groupID string) {
	t.Helper()

	// Before any fetch: a successful one would clear LeftAt.
	titles, err := client.GroupTitles(t.Context())
	if err != nil || titles[groupID].LeftAt.IsZero() {
		t.Errorf("cached LeftAt = %s, %v; want set", titles[groupID].LeftAt, err)
	}

	fresh, err := signal.FreshIntegrationGroup(t.Context(), client, groupID)
	membership, _ := fresh.MembershipOf(ownACI)

	switch {
	case errors.Is(err, signal.ErrNotAMember):
		t.Logf("fresh Group: %v", err)
	case err != nil || membership != signal.MembershipNone:
		t.Errorf("fresh Group: membership %v, %v; want ErrNotAMember", membership, err)
	}

	groups, err := client.Groups(t.Context())
	if err != nil {
		t.Fatalf("Groups: %v", err)
	}

	for _, group := range groups {
		if group.ID == groupID && group.Membership != signal.MembershipNone {
			t.Errorf("Groups lists %s with membership %v (%v), want none", groupID, group.Membership, group.Err)
		}
	}
}
