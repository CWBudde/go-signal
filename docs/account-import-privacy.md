# Account-import Stage 3 verification

Date: 2026-10-24. Status: Stage 3 complete.

This records Phase 16 Stage 3: durable privacy guards, typed blocking and effective projection
in the mautrix-signal fork, with go-signal's consumers. It is an offline prerequisite. It adds
no converter, imports no account and establishes no phone or server acceptance. Stage 4
(shared registry/open protocol and recoverable publication) is next. Design:
[importer spec](superpowers/specs/2026-10-10-account-import-design.md), "Storage keys and
privacy reconciliation". Decisions: [plan](superpowers/plans/2026-10-24-account-import-privacy.md),
including where the implementation differs from the reviewed draft.

## What changed

- **Schema.** Fork schema v30 (compatibility v30) adds `signalmeow_privacy_guards` and
  `signalmeow_device.storage_manifest_version`. Each guard row holds:
  - the entity kind (`account`, `aci`, `pni`), the typed ID and the field;
  - the local presence and value, and whether it is protected;
  - the manifest version it was armed at;
  - the last raw remote presence and value, and the version they were observed in.
- **Protected fields.** Blocked and profile sharing of typed users; read receipts, typing and
  sealed-sender indicators, link previews, the unlisted number and phone number sharing; our
  own profile key.
- **Writes.** `StoreRecipient` applies protected values before every row write, and
  `StoreProfileKey` keeps a protected own key. That covers storage contacts, message-request
  acceptance, the PNI message path, the outgoing profile key update, sent transcripts, stories,
  group updates, profile refresh, backup import and merges.
- **Storage.**
  - `ApplyStorage` observes each snapshot's raw values, unless the snapshot is older than the
    highwater, then writes.
  - A guard is released only by a strictly newer snapshot than the one it was armed at, whose
    present record carries the matching value. A proto3 boolean omitted from a present record
    counts as false. A missing record, an unknown enum or an absent key never releases a guard,
    and there is no timeout.
  - The device and `Store.AccountRecord` get a projected copy. The update handler receives the
    untouched raw snapshot.
  - Everything commits in one transaction, and a failed commit publishes nothing.
  - Profile sharing now follows storage both ways.
- **Typed blocking.**
  - `IsBlocked` takes a `libsignalgo.ServiceID`. The block cache is keyed by service ID, and
    PNI-only rows persist blocked and profile sharing.
  - Receive passes the sender's typed ID.
  - Blocking by number and group blocking are not enforced on receive.
- **Local commands.**
  - `Device.SetRecipientBlocked` re-arms an existing guard in the same transaction as the
    write. go-signal's `contacts block`/`unblock` use it.
  - go-signal's block overrides stay separate and apply on top.
  - The outgoing blocked list and the story audiences use protected values, including imported
    users that raw storage doesn't have. A blocked PNI-only user is listed by number. Without a
    number, the list is refused (`ErrBlockedListIncomplete`), since the phone would unblock them.
- **Seeding.** `accountimport.AccountOverlay` and `AccountGuards` build the effective
  AccountRecord and its guards from signal-cli's configuration. `CONTACTS` sharing is refused.

## Java overlay vectors

`scripts/generate-account-import-fixtures.sh --account-overlay <dir>` uses
signal-service-java's Wire `AccountRecord` from
`com.github.turasa:signal-service-java:2.15.3_unofficial_154` (SHA256 `2a35df8f…a09b`),
`wire-runtime-jvm:6.0.0-alpha02` (`750aa2aa…1520`) and `okio-jvm:3.17.0` (`3b13dcc3…6a73`).
The script checks all three; each also matches Maven Central's SHA-1.

- **Reimplementation.** signal-cli's account loader can't run without an account. The
  generator therefore restates the builder calls of `StorageSyncModels.localToRemoteRecord`;
  decoding, construction and encoding are Java's own. The provenance says so.
- **Cases.** All settings absent, all false, all true, `CONTACTS` sharing, only receipts set,
  and no self profile key. All of them start from an invented cached record.
- **Location.** Output is in `internal/signal/accountimport/testdata/java-account-overlay/`.
  `vectors.json` SHA256 is `e93542b2f195c6f6bcfc910faa31a847f3883d505bcb8fcc1263d14708aad986`.
- **Determinism.** A second generation produced a byte-identical `vectors.json`. In
  `provenance.json`, only the key order of one JNI entry differed.
- **Consumers.** `TestJavaAccountOverlay` compares the Go overlay with each Java record.
  For `CONTACTS`, it checks that Go refuses what Java collapses into `NOBODY`.

## Published version

| Module         | Version               | Commit                                     | Module checksum                                   |
| -------------- | --------------------- | ------------------------------------------ | ------------------------------------------------- |
| mautrix-signal | `v0.2609.0-purego.33` | `10c11384ee81c6666913864000b9fa5ff463c6a8` | `h1:ZdGCoSvdO9DbeyAR+pCOnUkZZFwQdXy8Al5rFYPcJxM=` |

Built on `v0.2609.0-purego.32` (`60cc9ee`) with one commit.

- **Before tagging.** go-signal's cgo `-race`, pure-Go (`just check-purego`) and untagged no-cgo
  suites passed against the local fork through an uncommitted scratch `go.work`.
- **Release integrity.** The fetched module's `pkg` tree matches the checkout, apart from the
  nested libsignal submodule. `go mod verify` passes.
- **Unchanged pins.** libsignal-go stays `v0.7.1-cw.6` and native libsignal stays `v0.102.2`.

## Verification

Fork checks before publication:

- **Vet and suites.** Fork vet passed. Both full suites passed: `./pkg/...` pure-Go, and
  native `-race` `./pkg/signalmeow/... ./pkg/libsignalgo/...`.
- **Race runs.** Three `-race` runs per backend of the new guard, typed-blocking and storage
  tests passed.
- **Mutation checks.** These faults were each caught:
  - releasing on the armed version itself (`TestPrivacyGuardRelease` fails);
  - skipping the write-time guards (first fetch, restart, missing/false, release and bypass
    tests fail);
  - publishing the account record before commit (`TestPrivacyGuardRollback` and
    `TestStorageUpdateHandlerCommitFailure` fail).
    In go-signal, these were caught:
  - writing a local block without re-arming (`TestSetBlockedRearmsGuard` fails);
  - leaving protected blocks out of the outgoing list (`TestSetBlockedRearmsGuard` and
    `TestBlockedListGuardedPNI` fail).

| Spec evidence           | Tests                                                                                                                                   |
| ----------------------- | --------------------------------------------------------------------------------------------------------------------------------------- |
| First conflicting fetch | `TestPrivacyGuardFirstFetch` (SQL, block cache, live and stored account record; raw snapshot to the handler)                            |
| Restart                 | `TestPrivacyGuardRestart`, the reopened steps of `TestPrivacyGuardBypass` and `TestPrivacyGuardRelease`                                 |
| Missing/false values    | `TestPrivacyGuardMissingAndFalse`, `TestPrivacyGuardRelease` (store), `TestPrivacyGuardProjectAccountRecord`                            |
| Alignment, later change | `TestPrivacyGuardRelease` (both), `TestSetBlockedRearmsGuard`                                                                           |
| Typed sender/cache      | `TestTypedBlocking`                                                                                                                     |
| Bypass handlers         | `TestPrivacyGuardBypass` (requests, PNI message, sent transcript/group/story keys, profile refresh, backup import, merge)               |
| Raw consumers           | `TestPrivacyGuardFirstFetch` (handler), `TestSetBlockedRearmsGuard`, `TestBlockedListGuardedPNI`, `TestStoryAudiencesUseGuards`         |
| Rollback/cache          | `TestPrivacyGuardRollback`, `TestStorageUpdateHandlerCommitFailure`                                                                     |
| Schema floor, seeding   | `TestPrivacyGuardSchemaFloor` (a v29 reader refuses v30), `TestPrivacyGuardSeedRefusals`, `TestJavaAccountOverlay`, `TestAccountGuards` |

Final commands use `GOWORK=off` against the published pin. Every one exited 0:

| Command (`GOWORK=off`)                                                                      | Evidence                                                                   |
| ------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------- |
| `just check`                                                                                | Formatting, lint, native race suite, libsignal pin and tidy checks         |
| `just check-purego`                                                                         | Pure-Go vet, lint and suite                                                |
| `just test-fork`                                                                            | Includes `TestPrivacyGuard*`, `TestTypedBlocking`, `TestWhitelistTwoWay`   |
| `just test-diff`                                                                            | Differential tests, race runs of the fork filters and the allocator runner |
| `CGO_ENABLED=0 go test -count=1 ./...`                                                      | Untagged no-cgo suite                                                      |
| `scripts/generate-account-import-fixtures.sh --account-overlay`                             | Regenerated `vectors.json` byte-identical                                  |
| `scripts/test-prekey-allocators.sh`                                                         | 12 actions, exact ledger                                                   |
| `CGO_LDFLAGS="-L $PWD/third_party/lib" scripts/test-account-import-compatibility.sh --java` | All 264 actions, 48 Java legs                                              |

The Java steps used the retained private JDK 25.0.2 / Maven 3.9.11 toolchain and cache. The
compatibility ledger is byte-identical to the [Stage 1 ledger](account-import-compatibility-ledger.tsv)
(SHA256 `8d058a94556bbd97a48b5205c08c2af1f207fdf0590dc55523056065bd4c34bb`).

## Limitations

- **One-way upgrade.** Upgrading uses compatibility v30, so going back means starting the
  account over ([dev.md](dev.md#prekey-allocators-and-downgrades)).
- **Behaviour change for all accounts.** Profile sharing now follows storage both ways. A
  storage record with `whitelisted=false` clears local acceptance, and receipts and typing to
  that user are then suppressed.
- **No guards for numbers or groups.** Number-only users and groups have no guards, and their
  blocks aren't enforced on receive. The converter must refuse blocked number-only contacts
  and blocked groups.
- **Freshness is the converter's job.** Deciding which fields to protect from the source's
  local values and storage IDs is left to the Stage 5 converter (`AccountGuards`'s `fresh`).
- **No sync handlers.** There is still no handler for an incoming blocked-list or
  configuration sync; any future one must use the guards.
- **Offline only.** No live account, phone or server was used.
