// Test-only barriers injected into a disposable copy by the lifecycle script.
package web

import (
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow/protobuf/signalpb"
	"go.mau.fi/util/exsync"
)

var (
	lifecycleBeforeResponseRegistrationForTest = func(*exsync.Map[uint64, websocketPendingResponse], chan *signalpb.WebSocketResponseMessage) {}
	lifecycleBeforeWorkerJoinForTest           = func() {}
)

// SetLifecycleRegistrationHooksForTest gates the actual writer before it
// registers an accepted request and observes the actual coordinator's join.
// Install before Connect and reset only after Close joins all workers.
func SetLifecycleRegistrationHooksForTest(accepted func(<-chan *signalpb.WebSocketResponseMessage, func() int, func()), joining func()) func() {
	lifecycleBeforeResponseRegistrationForTest = func(responses *exsync.Map[uint64, websocketPendingResponse], pending chan *signalpb.WebSocketResponseMessage) {
		accepted(pending, responses.Len, func() {
			for _, orphan := range responses.SwapData(nil) {
				close(orphan.channel)
			}
		})
	}
	lifecycleBeforeWorkerJoinForTest = joining
	return func() {
		lifecycleBeforeResponseRegistrationForTest = func(*exsync.Map[uint64, websocketPendingResponse], chan *signalpb.WebSocketResponseMessage) {}
		lifecycleBeforeWorkerJoinForTest = func() {}
	}
}
