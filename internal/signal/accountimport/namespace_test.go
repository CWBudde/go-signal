//go:build cgo || libsignal_go

package accountimport_test

import (
	"testing"

	"github.com/cwbudde/mautrix-signal/pkg/libsignalgo"
	"github.com/google/uuid"
)

// This is a target-store keying contract, separate from Java exchange evidence.
// A copied record is archived with the Go API to make the two opaque rows differ.
func TestJavaAccountImportTypedNamespaceCollision(t *testing.T) {
	t.Parallel()
	corpus := loadJavaCorpus(t)
	scenario := corpus.scenario(t, "aci-current-skipped")
	dir := t.TempDir()
	restoreJavaAccount(t, dir, scenario.SourceAccountID)
	account := openJavaAccount(t, dir, scenario.SourceAccountID)
	sid := uuid.MustParse(scenario.RemoteServiceID.UUID)
	aci := must(libsignalgo.NewACIServiceID(sid).Address(2))(t)
	pni := must(libsignalgo.NewPNIServiceID(sid).Address(2))(t)
	current := must(account.device.ACISessionStore.LoadSession(t.Context(), aci))(t)
	original := must(current.Serialize())(t)
	archived := must(current.Clone())(t)
	check(t, archived.ArchiveCurrentState())
	changed := must(archived.Serialize())(t)
	check(t, account.device.ACISessionStore.StoreSession(t.Context(), pni, archived))
	// Local account namespace is independent of the typed remote address.
	check(t, account.device.PNISessionStore.StoreSession(t.Context(), aci, archived))
	account.close(t)

	reopened := openJavaAccount(t, dir, scenario.SourceAccountID)
	defer reopened.close(t)

	equal(t, must(must(reopened.device.ACISessionStore.LoadSession(t.Context(), aci))(t).Serialize())(t), original)
	equal(t, must(must(reopened.device.ACISessionStore.LoadSession(t.Context(), pni))(t).Serialize())(t), changed)
	equal(t, must(must(reopened.device.PNISessionStore.LoadSession(t.Context(), aci))(t).Serialize())(t), changed)

	if must(reopened.device.PNISessionStore.LoadSession(t.Context(), pni))(t) != nil {
		t.Fatal("local or remote service namespaces collided")
	}
}
