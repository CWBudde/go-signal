# IT-01: libsignalgo session clocks pass seconds instead of milliseconds

Status: prepared on 2026-10-09 and published as
[mautrix/signal#674](https://github.com/mautrix/signal/issues/674) on 2026-10-10 with owner approval.
Before publication, upstream main had advanced to `e73dca83e722522231fe5bec3511f6e95765e180`;
the intervening commit changed only provisioning, leaving the three affected calls unchanged.
Our pinned fork already contains the repair. This report does not change our dependency pins.

## Upstream report

At upstream main [`fc893ce0a3026c982a48702f2d913e63bbc15c58`](https://github.com/mautrix/signal/tree/fc893ce0a3026c982a48702f2d913e63bbc15c58),
three libsignalgo calls pass `time.Now().Unix()` to a libsignal protocol timestamp:

| Go call                         | FFI call                                        | Source                                                                                                                                        |
| ------------------------------- | ----------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------- |
| `Encrypt`                       | `signal_encrypt_message`                        | [message.go:32](https://github.com/mautrix/signal/blob/fc893ce0a3026c982a48702f2d913e63bbc15c58/pkg/libsignalgo/message.go#L32)               |
| `ProcessPreKeyBundle`           | `signal_process_prekey_bundle`                  | [prekeybundle.go:33](https://github.com/mautrix/signal/blob/fc893ce0a3026c982a48702f2d913e63bbc15c58/pkg/libsignalgo/prekeybundle.go#L33)     |
| `SessionRecord.HasCurrentState` | `signal_session_record_has_usable_sender_chain` | [sessionrecord.go:108](https://github.com/mautrix/signal/blob/fc893ce0a3026c982a48702f2d913e63bbc15c58/pkg/libsignalgo/sessionrecord.go#L108) |

Upstream pins [libsignal v0.105.0](https://github.com/mautrix/signal/blob/fc893ce0a3026c982a48702f2d913e63bbc15c58/pkg/libsignalgo/signalversion/version.go).
That version's bridge takes `Timestamp` for
[session usability](https://github.com/signalapp/libsignal/blob/v0.105.0/rust/bridge/shared/src/protocol.rs#L964),
[bundle processing](https://github.com/signalapp/libsignal/blob/v0.105.0/rust/bridge/shared/src/protocol.rs#L1003) and
[encryption](https://github.com/signalapp/libsignal/blob/v0.105.0/rust/bridge/shared/src/protocol.rs#L1025).
[Timestamp](https://github.com/signalapp/libsignal/blob/v0.105.0/rust/protocol/src/timestamp.rs)
represents epoch milliseconds. Passing epoch seconds therefore moves the effective clock to 1970.

This affects unacknowledged session creation and expiry. The serialized pending pre-key timestamp
is deliberately in **seconds**: libsignal
[converts its input clock to seconds when writing it](https://github.com/signalapp/libsignal/blob/v0.105.0/rust/protocol/src/state/session.rs#L509),
then [checks expiry against the current clock](https://github.com/signalapp/libsignal/blob/v0.105.0/rust/protocol/src/state/session.rs#L262).
That stored field should retain its existing units. The three Go-to-FFI clock arguments need
`UnixMilli()` instead. These are protocol timestamps; this report does not propose changing
zkgroup's separate timestamps in seconds.

## Offline reproduction and repair

The existing downstream [TestUnacknowledgedSessionClock](https://github.com/CWBudde/mautrix-signal/blob/7f2481fad0c11ad2b3917a4979bd70f70d3ed04b/pkg/libsignalgo/sessiontime_test.go)
creates a real PQXDH session, inspects its serialized creation time, encrypts a fresh message,
then backdates the pending pre-key beyond libsignal's 30-day unacknowledged-session limit.
It checks both `HasCurrentState` and `Encrypt` without contacting Signal.

On 2026-10-09, the test was run in a disposable copy of fork `v0.2609.0-purego.29`, restoring
upstream's `Unix()` expressions at these three call sites. The native library was **v0.102.2**.
Current upstream v0.105.0 was checked by source inspection, not built or tested locally.

| Clock arguments                               | Result with native v0.102.2                                                      |
| --------------------------------------------- | -------------------------------------------------------------------------------- |
| All three use seconds                         | FAIL: stored creation time `1791543` is below current epoch seconds `1791543125` |
| Only session creation uses milliseconds       | FAIL: `HasCurrentState` accepts the expired session                              |
| Creation and usability check use milliseconds | FAIL: `Encrypt` accepts the expired session                                      |
| All three use milliseconds                    | PASS, including ten runs with `-race`                                            |

The [downstream repair](https://github.com/CWBudde/mautrix-signal/commit/acd96fee8560826d7cd0155351f694d2fd707cfa)
changes only those three clock expressions and adds the regression test. Its protobuf test import
uses the downstream module path and would need adapting for an upstream test.

From go-signal, with its pinned native library built, the regression runs against the downloaded
fork with:

```sh
GOWORK=off CGO_ENABLED=1 CGO_LDFLAGS="-L $PWD/third_party/lib" \
  go test -race -count=10 -run '^TestUnacknowledgedSessionClock$' \
  github.com/cwbudde/mautrix-signal/pkg/libsignalgo
GOWORK=off CGO_ENABLED=0 \
  go test -tags libsignal_go -count=10 -run '^TestUnacknowledgedSessionClock$' \
  github.com/cwbudde/mautrix-signal/pkg/libsignalgo
```

These commands validate the repaired downstream implementation. To reproduce the failures,
copy the dependency to a disposable module directory and restore the `Unix()` expressions
in the combinations above; do not edit the module cache or a real account.

No production bridge failure or current-upstream runtime result is claimed. Existing sessions
with incorrect creation timestamps are not migrated by this three-line fix.

### Upstream checklist

- [x] This is a code-level bug, with an offline reproduction rather than a setup report.
- [x] The affected calls, timestamp contract, proposed fix and validation limits are included.
- [x] The bug remains present on main at the revision above. `!signal version` is unavailable:
      this is a libsignalgo source report, not a running bridge instance.
