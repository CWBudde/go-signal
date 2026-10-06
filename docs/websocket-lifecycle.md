# Websocket lifecycle investigation

Investigated offline on 2026-10-06 against the pinned
[`v0.2609.0-purego.22`](https://github.com/cwbudde/mautrix-signal/tree/v0.2609.0-purego.22)
fork, commit `fda5a06d822321a8e4fdc93f5f74af657fab8569`, using Go 1.27.1.
The earlier captured incoming-channel fix remains valid. This investigation found
additional lifecycle failures; `v0.2609.0-purego.23` repairs the contracts below.
No Signal account, service or phone was used.

## Reproduce

The probes copy the pinned module to a temporary directory and add test files plus
test-only scheduling barriers. They leave the module cache and fork checkout unchanged, ignore any
active workspace, and use `net.Pipe` websocket peers or an already-canceled context.
The four original probes and full-queue shutdown/reconnect tests now live in the fork.
The script additionally injects two scheduling barriers into the disposable production
source, immediately before response registration and worker joining, to test the actual
coordinator's late-registration ordering. Exact-once source anchors fail if the source
shape changes. This script requires Python 3 in addition to Go.
**Exit status 0 is expected on the current pin.** `just test-fork` runs the pure-Go
contracts; `just test-diff` and cgo CI run both backends with `-race`.

```sh
# Pure-Go backend; cgo enables race instrumentation, without a Rust library.
CGO_ENABLED=1 scripts/test-websocket-lifecycle.sh -tags libsignal_go -race

# cgo backend; requires the normal libsignal archive.
CGO_ENABLED=1 CGO_LDFLAGS="-L $PWD/third_party/lib" \
  scripts/test-websocket-lifecycle.sh -race
```

Use `-run '^TestLifecycleImmediateCancellation$'` to isolate the data race, or
`-run '^TestLifecycle(CloseWaitsForHandler|FullQueueCancellation|RequestCancellation)$'`
to run the other contracts. Context deadlines bound fixture setup and teardown.
The handler probes check that `Close` remains blocked for 150 ms before releasing
their gates. The queue and caller cancellation probes allow 150 ms for completion,
then release withheld queue space or a response and join the affected goroutine. Their source
paths explain the block; the watchdog is not a measurement of production latency.

## Repaired shutdown contract

- `Connect` captures a stable status channel and installs cancellation before launching
  the connection loop. Status closure and `Close` completion follow handler cleanup.
- Shutdown signals blocked senders separately from completed cleanup. It joins the active
  request handler without holding the outgoing-channel lock, then closes channels and
  signals completion. A handler must return on cancellation and must not call `Close`
  synchronously, which would wait for itself.
- Final shutdown discards queued requests without acknowledging them, allowing service
  redelivery. A cancellation check after queue selection prevents starting queued work
  after cancellation has been observed. An already-active handler can finish processing.
- Incoming enqueue selects on connection cancellation even when its queue is full.
  Forced reconnect joins the old reader and writer while retaining the handler and queue;
  the new connection can serve outgoing requests while the handler is still stalled.
- Connection cleanup joins all workers before draining pending responses. The reader owns
  entries it atomically removes; cleanup owns remaining entries after the join. A canceled
  caller can return its context error without awaiting a response and never closes the
  response channel. A concurrently ready response may win the select. Late responses
  can still be delivered safely, and subsequent requests remain usable.

The deterministic late-registration regression holds an accepted writer request before
registration, cancels the connection, observes the coordinator reaching its worker join,
then releases the writer. On `.22`, cleanup has already drained the map: the response
channel remains open, one entry remains, and the caller blocks. On `.23`, joining precedes
that drain: the channel closes, the map is empty and the caller completes. Teardown drains
orphans only after worker completion to join the caller on the old, failing pin.

The facade regression gates a real websocket handler after its facade callback returns.
It checks that `meowClient.Close` waits, the handler can still read the account database,
and the database closes after handler completion. No facade production behavior change
is needed because `StopReceiveLoops` already calls websocket `Close` before releasing it.

Affected fork suites and lifecycle probes passed with the race detector on both backends;
parent cgo and pure-Go checks are the required integration gates. These tests verify the
exercised shutdown and resource-lifetime paths, not live service delivery. Existing
acknowledgment-flush ordering and the key-check loop's separate self-join remain outside
this repair; the latter is recorded as an open follow-up in `PLAN.md`.

## Findings on the old pin

### Connect races with immediate cleanup

`Connect` starts `connectLoop` and then reads `s.statusChannel` without synchronization
([source, lines 129–131](https://github.com/cwbudde/mautrix-signal/blob/fda5a06d822321a8e4fdc93f5f74af657fab8569/pkg/signalmeow/web/signalwebsocket.go#L129)).
Cleanup writes `s.statusChannel = nil` under `s.closeLock`, which does not protect the
returning read
([lines 191–202](https://github.com/cwbudde/mautrix-signal/blob/fda5a06d822321a8e4fdc93f5f74af657fab8569/pkg/signalmeow/web/signalwebsocket.go#L191)).

`TestLifecycleImmediateCancellation` repeatedly connects with an already-canceled
context and drains the returned channel. The race detector reports the read at
line 131 against the write at line 201. The final pure-Go race run also returned a
nil status channel, triggering the probe's explicit nil-channel assertion.
Capture the status channel before launching the goroutine, or establish a stable,
synchronized channel lifetime. Preserve status delivery and closure semantics.

### Close does not wait for the request handler

The handler goroutine runs across connections and calls the handler synchronously
([lines 215–252](https://github.com/cwbudde/mautrix-signal/blob/fda5a06d822321a8e4fdc93f5f74af657fab8569/pkg/signalmeow/web/signalwebsocket.go#L215)).
It is outside the per-connection worker wait group. The deferred cleanup signals
`closeEvt` before channel cleanup; `Close` waits only for that event.

`TestLifecycleCloseWaitsForHandler` uses the complete `Connect` lifecycle, delivers
a real protobuf request over a pipe, gates the handler after entry, then cancels
the connection and calls `Close` asynchronously. `Close` returns while the handler
remains gated. Releasing the gate lets it finish; the probe then joins handler,
status closure and `Close`. This demonstrates an outstanding
handler, without claiming storage corruption or message loss.

The facade's `done`/`handling` guard joins its own event callback
(`internal/signal/meow.go`), but signalmeow decrypts before that callback and performs
deferred processing afterward (`pkg/signalmeow/receiving.go`). It therefore does not
join the entire upstream request handler before account resources are released.
Repair needs an explicit handler-completion boundary and a shutdown policy for
queued requests. Signal completion after cleanup; avoid joining a handler while
holding a lock it needs. A non-cooperative handler requires an explicit contract.

### A full incoming queue ignores cancellation

The incoming queue has capacity 256. After decoding a request, the reader performs
an unconditional send
([line 470](https://github.com/cwbudde/mautrix-signal/blob/fda5a06d822321a8e4fdc93f5f74af657fab8569/pkg/signalmeow/web/signalwebsocket.go#L470)).
Context cancellation is checked only at the next loop iteration. When the queue
is full and the handler cannot receive, the reader cannot reach that check.

`TestLifecycleFullQueueCancellation` fills a queue, pauses the real reader at its
decoded-request log hook, cancels its context, and releases the hook. The reader
remains blocked until the fixture frees one queue slot, then returns cancellation.
This isolates the actual reader; it does not execute the connection coordinator.
The coordinator waits for the reader before outer cleanup closes the queue
([lines 418–423](https://github.com/cwbudde/mautrix-signal/blob/fda5a06d822321a8e4fdc93f5f74af657fab8569/pkg/signalmeow/web/signalwebsocket.go#L418)),
so this condition can strand reconnect or shutdown. Production frequency was not
measured. Make enqueue cancellation-aware and define queued-request handling.

### Response cleanup has an uncovered registration window

On disconnect, the coordinator swaps out and closes pending response channels
**before** joining the workers and before its final `loopCancel` call (lines 419–423).
The coordinator has already observed connection cancellation at line 402; an
accepted writer request may still be in flight. The writer receives
a request at line 551, registers its response channel at line 562, then writes to
the socket. `exsync.Map.SwapData(nil)` installs a new empty map; it does not prevent
later registration.

The source admits this ordering: the writer accepts a request, cleanup drains the
old map, the writer registers into the new map, and writing fails on the closed
socket. There is no second drain after the worker join. `SendRequest` can remain
waiting for that channel at line 662. This was a **source-level finding** during the
original investigation. The new deterministic regression above subsequently reproduced
it on `.22`. The old fixtures lacked a barrier between receive and registration; the
current script injects one into a disposable source copy. The existing `RunWebsocketLoopsForTest` helper bypasses coordinator cleanup
and cannot validate this interleaving.

An additional cancellation failure is reproduced by
`TestLifecycleRequestCancellation`: the real writer sends a public `SendRequest`,
the peer reads it, and the caller context is canceled while the peer withholds the
response. The call remains blocked until the peer supplies that response. After
enqueue, the caller's wait does not select on its context. This probe uses a
test-only reader/writer helper and does not claim to reproduce the registration
window. Its fixture teardown joins workers and drains the remaining response map
to release the caller even on setup failures; that teardown is not production
coordinator coverage.

Repair should stop and join producers before draining pending responses, alongside
the incoming-queue fix, and make the caller wait cancellation-aware. Preserve
response-channel ownership: the reader's atomic `Pop` and cleanup's atomic map swap
currently give each existing entry a single owner. Caller cancellation must not
close a channel that the reader or cleanup may still use. A direct double-close or
send-on-closed-channel failure was not demonstrated by this investigation.

## Original evidence

Both cgo and pure-Go race runs on `.22` reproduced the `Connect` race and failed the
three original contracts:

```text
Close returned before the request handler completed
reader stayed blocked on a full queue after cancellation
request stayed waiting for a response after cancellation
```

The investigation and original failing probe sources remain in parent commit
[`61c5ded`](https://github.com/cwbudde/go-signal/commit/61c5dedffdf9d9860b05f8e686fea3d3a1a947cc).
The current script runs repaired contracts and the deterministic ownership regression.
