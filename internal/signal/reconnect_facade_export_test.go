//go:build cgo || libsignal_go

package signal

import "context"

// PrepareReceiveSupervisor uses the facade's supervisor with controlled transport start/join.
// It prepares cancellation before returning the launcher, as Connect must before starting workers.
func PrepareReceiveSupervisor(ctx context.Context, client Client, policy ReconnectPolicy,
	start func() (<-chan LoopStatus, error), stop func() error,
) (func(<-chan LoopStatus), <-chan struct{}, <-chan struct{}) {
	meow := client.(*meowClient) //nolint:forcetypeassert // test helper
	supervisorCtx, sup := meow.prepareSupervisor(ctx)
	sup.policy, sup.start, sup.stop = policy, start, stop

	return func(statuses <-chan LoopStatus) {
		meow.supervise(supervisorCtx, statuses, sup)
	}, meow.supervised, supervisorCtx.Done()
}
