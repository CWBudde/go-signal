# Durable Prekey Allocators Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Complete Phase 16 Stage 2a so imported and existing accounts allocate prekeys without reusing IDs after consumption, restart or interrupted generation.

**Architecture:** The mautrix-signal fork owns three durable allocator rows per account and typed local service ID. Ordinary refill commits an atomic range reservation before generation; import seeding and provisioning initialization instead participate in the transaction that stores their keys. A schema compatibility floor prevents older binaries from reopening the database and reverting to retained `MAX+1` allocation.

**Tech Stack:** Go, dbutil, SQLite through mattn and modernc, PostgreSQL-compatible fork queries, both libsignal backends.

**Spec:** `docs/superpowers/specs/2026-10-10-account-import-design.md`, “Durable key allocation”; source mapping in `docs/account-import.md`.

## Global Constraints

- Scope is Stage 2a only. Storage-key lifecycle, the converter, privacy, registry recovery and live import acceptance remain open.
- Source IDs are `0..0xfffffe`; ordinary and last-resort Kyber keys share one counter. Allocation refuses wrap at exclusive limit `0xffffff`.
- Preserve independent EC/signed/Kyber positions and active signed/last-resort references for each account/local typed ACI or PNI. ID zero is valid; absent active references use nil.
- Seed import metadata with records in the conversion transaction. Reject next IDs at/below retained maxima and invalid active references. An absent active reference is valid even when historical records remain, as in Java.
- Failed reservations return no usable range and consume no IDs. Generation/storage failure after a successful reservation leaves gaps, never reusable IDs.
- Legacy migration is a one-time retained-max fallback, not reconstruction of lost consumption or active-key history. Generic deletion and reopening must preserve allocator state.
- New provisioning initializes fixed signed/LR ID `1`, next signed/Kyber `2`, next EC `1`, and active signed/LR `1`, atomically with its protocol keys.
- Count ordinary Kyber separately from last-resort; retain existing ordinary deletion/LR retention behavior. Do not invent last-resort replay history.
- Retain native libsignal `v0.102.2` and libsignal-go `v0.7.1-cw.6`. Fork release and parent pins are immutable; no committed `replace` or `go.work`.
- Parent changes remain directly on authorized main. Use an isolated writable fork checkout; preserve the unrelated `reference/signal-cli` checkout and never edit it.
- Execute coupled implementation tasks inline, with read-only agents for reconnaissance and one independent final review. The controller owns plans, shared files and history.

## Review Focus

1. Downgrading to a fork without reservations must refuse schema 28 before touching key state; Task 1 tests the old upgrade-table reader.
2. An outer transaction must not turn a successful reservation into reusable IDs after rollback; Task 1 tests transaction-context rejection.
3. Interrupted generation and competing database handles must preserve disjoint committed ranges, including fully depleted stores; Tasks 1–3 exercise failures, concurrency and process/backend switching.
4. Relinking an existing account, including a changed PNI, must initialize the new credentials and keys together or leave the old protocol state intact; Task 2 injects failure during replacement.
5. Legacy IDs beyond the source allocation range and ambiguous active-key candidates must not cause truncation, fabricated references or downward allocator movement; Task 1 exercises these migration inputs.

## Written-plan review (2026-10-14)

One independent read-only review checked this plan against the spec, `docs/account-import.md` and the fork at `a3a2de4`. Verdict: ready after fixes. Owner rulings and amendments below take precedence over conflicting text in the tasks.

**Owner rulings (2026-10-14).**

- R1 Compatibility floor: schema/compat `28/28` stays; downgrading after the upgrade is refused until relink. Document it in `docs/dev.md`, `docs/maintenance.md` and the release notes, not only in the evidence doc. `docs/maintenance.md` gains a rule: fork-only schema versions are recorded there and future upstream migrations are renumbered after them on rebase.
- R2 Missing allocator rows (device created by plain `PutDevice`, with no provisioning, import or migration): refill fails closed with `ErrPreKeyAllocatorUninitialized`, logs it, and never falls back to `MAX+1`. `GetNext*` and `PreKeyCounts` behave the same: counts still work, positions return the error. Tests that reach refill seed explicitly.
- Exhaustion is already decided by the spec ("Refuse exhaustion before wrapping"). Import headroom is converter scope (later stage), not 2a.

**Amendments.**

- A1 (blocker) go-signal `internal/store/linkstore.go` implements `store.DeviceStore` and is the production provisioning path (`internal/signal/meow.go`). Task 3 adds `LinkStore.StoreProvisionedDevice`: lock and open lazily as `PutDevice` does, then delegate to `Store.Devices`; on failure release the lock and close the database. Test success, relinking with a changed PNI and injected rollback through LinkStore. Task 2 ends with a local-workspace `go vet ./...` of the parent.
- A2 Transaction detection uses `_, inTxn := db.Execable(ctx).(dbutil.Transaction)`, one helper shared by Reserve (must not be in a transaction), Seed (must be) and `StoreProvisionedDevice` (must not be). It sees only transactions of the same root database; a foreign handle's transaction surfaces as bounded `SQLITE_BUSY`, which a test asserts (no hang).
- A3 Fork SQL tests use `t.TempDir()` file databases with the parent's DSN options per driver (foreign keys, WAL, `busy_timeout=5000`, immediate txlock). The modernc dependency replaces the fake-fixture limitation recorded in `docs/maintenance.md`; update that paragraph.
- A4 Exhaustion or another refill error in one kind or scope must not stop the others: EC, Kyber, ACI and PNI are each attempted and their errors joined. Test it.
- A5 Kyber zero start is impossible with a retained LR (shared counter, seed rejects next ≤ max): zero start is tested for EC, Kyber starts at or above LR ID + 1.
- A6 Migration skips scopes whose service ID is not the device's current ACI or `PNI:<uuid>` (orphaned rows stay, without allocator rows), and creates no PNI scope for a nil PNI. Test both.
- A7 Schema-floor test: v27 reader is `upgrades.Table[:27]`; the fixture includes the `signalmeow_version` row `(27, 13)`. Latest header `v0 -> v28 (compatible with v28+)`, upgrade registered via `dbutil.WrapUpgrade`/`.With` in `upgrades.go`.
- A8 Schema CHECKs: `kind IN ('ec','signed','kyber')`, `next_id >= 0`, `active_id` NULL for `ec`. `Container.DeleteDevice` cascades on purpose; "deletion never deletes allocator rows" means key/session deletion, including `ClearDeviceKeys` on unlink/422. Active references may point at keys deleted later.
- A9 `GetNext*` returns (ordinary count, persisted next). Next `0xffffff` is returned as is (exhaustion is a reservation error); only positions beyond `uint32` error.
- A10 Provisioning atomicity is local only: the server registers the device before local writes. A failed local transaction keeps the old local rows, but the error text says a relink is required. No claim that the old account stays usable.
- A11 `internal/store/prekey_allocator_test.go` carries `//go:build cgo || libsignal_go`; the backend-step test skips unless its environment variables are set.
- A12 Task 3 also updates `docs/account-import.md` (allocator metadata now exists; current fork commit) and the deliberate fork additions in `AGENTS.md`.
- A13 Ledger: the executing session's scratchpad (outside both repositories, never committed); its durable summary goes to `docs/account-import-allocators.md`. This plan file is committed with the batch.

---

## Boundaries and verification

The fork starts from released commit `a3a2de4f8371aaf9b02b979d98dd205c346d36ae` (`v0.2609.0-purego.30`). Paths labeled “fork” below are relative to its writable checkout; other paths are relative to go-signal. Recheck branch/tags before release and choose the next unused immutable `v0.2609.0-purego.*` tag; never overwrite one.

All new tests use external `_test` packages. Fork database tests use real SQLite: mattn for `cgo && !libsignal_go`, modernc for `libsignal_go`. Add test dependency `modernc.org/sqlite v1.59.0`, matching the parent, rather than a fake SQL driver. Fork changes must retain PostgreSQL-compatible SQL syntax; SQLite tests do not establish PostgreSQL runtime coverage.

Before implementation, run `GOWORK=off just check` once and save its exit status/output in this plan's ignored workspace. Use repository-disk `TMPDIR`; native commands need `CGO_LDFLAGS="-L /home/christian/Code/go-signal/third_party/lib"`. An ignored temporary workspace may connect local fork and parent during development. Repeat release acceptance with `GOWORK=off` after pinning published versions.

## Task 1: Durable storage, migration and reservation contracts

**Files (fork):** Create `pkg/signalmeow/store/prekey_allocator.go`, `prekey_allocator_test.go`, `prekey_allocator_sqlite_cgo_test.go`, `prekey_allocator_sqlite_purego_test.go`, `testdata/schema-v27.sql`, `upgrades/28-prekey-allocators.go`; modify `prekey_store.go`, `upgrades/00-latest.sql`, `upgrades/upgrades.go`, `go.mod`, `go.sum`.

**Interfaces:** Extend `PreKeyStore` with the methods below. Add the seed operation to `Container`, without expanding its ordinary `DeviceStore` interface for import. Errors are `errors.Is`-matchable `ErrPreKeyAllocatorUninitialized`, `ErrPreKeyAllocatorExists`, `ErrPreKeyAllocatorInvalid`, `ErrPreKeyAllocatorExhausted` and `ErrPreKeyReservationInTransaction`.

```go
type PreKeyKind string // Constants PreKeyEC="ec", PreKeySigned="signed", PreKeyKyber="kyber".
const PreKeyIDLimit uint64 = 0xffffff // Exclusive generated-ID bound.
type PreKeyAllocatorState struct {
    NextEC, NextSigned, NextKyber uint64
    ActiveSigned, ActiveLastResort *uint32
}
// Container: requires the caller's transaction; rejects an existing allocator.
SeedPreKeyAllocator(ctx context.Context, accountACI uuid.UUID,
    serviceID libsignalgo.ServiceID, state PreKeyAllocatorState) error
// PreKeyStore: snapshot and counts do not allocate.
PreKeyAllocatorState(ctx context.Context) (PreKeyAllocatorState, error)
PreKeyCounts(ctx context.Context) (ordinaryEC, ordinaryKyber uint32, err error)
ReservePreKeyIDs(ctx context.Context, kind PreKeyKind, count uint32) (start uint32, err error)
```

- [x] **Step 1: Write storage contract tests.** Copy the unchanged v27 schema from released commit `a3a2de4f8371aaf9b02b979d98dd205c346d36ae` into the test fixture and record provenance in its SQL comment. `TestPreKeyAllocatorMigration` opens that genuine v27 database, migrates sparse and empty ACI/PNI stores, then proves positions survive deletion and reopening. Seed retained EC `7`, signed `11`, ordinary Kyber `17`, LR `23`: expect next `8/12/24`; counts exclude LR. A unique retained signed/LR candidate becomes the legacy active reference; multiple candidates yield nil, documenting that history is unknown. Preserve actual wide `MAX+1` for legacy keys outside the source range and refuse future allocation; never clamp or cast it backwards. `TestPreKeyAllocatorSchemaFloor` asserts version/compat `28/28` for fresh and migrated databases and `dbutil.ErrUnsupportedDatabaseVersion` using a v27 upgrade-table reader.
- [x] **Step 2: Write allocator API tests.** `TestPreKeyAllocatorSeedTransaction` stores records and seeds in one transaction; commit persists both, injected rollback persists neither. Reject wrong/nonlocal typed scope, duplicate initialization, next-at/below-max, invalid active signed/LR references, active LR pointing to an ordinary key, and seed positions above `PreKeyIDLimit`. Allow next `0` for an empty kind and nil active references. Snapshot results have independent active-ID pointers. `TestPreKeyAllocatorDirectWrites` writes above the watermark and expects exactly `id+1` for EC, signed and shared ordinary/LR Kyber; lower writes never rewind it, and active references remain unchanged. Injected SQL failure and enclosing transaction rollback each restore both key row and position. Initial writes without metadata do not initialize it; wide legacy writes never truncate their position. `TestPreKeyAllocatorReservations` covers all three kinds, multiple accounts, ACI/PNI with the same UUID, sparse/depleted stores, two concurrent database handles and restart. Concurrent successful ranges cannot overlap. Missing state, invalid kind, zero count, canceled context and SQL failure return no range without advancement. `TestPreKeyAllocatorExhaustion` reserves the final ID `0xfffffe`, persists next `0xffffff`, then refuses another reservation; a crossing batch fails atomically. `TestPreKeyAllocatorOuterTransaction` proves reservations reject caller transactions and rollback cannot expose a usable range.
- [x] **Step 3: Run `CGO_ENABLED=0 go test -tags libsignal_go -count=1 -run '^TestPreKeyAllocator' ./pkg/signalmeow/store`.** Expected: failure for the missing API/schema before implementation, after resolving only test prerequisites.
- [x] **Step 4: Implement the schema and API.** Table `signalmeow_prekey_allocators` has account/service/kind composite primary key, nonnegative BIGINT next position, nullable active ID and cascading device FK. Typed IDs use existing `ServiceID` SQL encoding. Migration v28 and fresh latest schema require compatibility v28; initialize three rows for both current device scopes, including empty stores. Use wide arithmetic for retained maxima. Seed requires a shared dbutil transaction context, validates records/scope/range, then inserts all three rows; an existing state cannot be reset through this API. Reservation rejects an enclosing dbutil transaction and commits a guarded SQL `UPDATE ... RETURNING` in its own transaction before returning the start. Validate `count` before subtraction and distinguish exhausted/missing rows without read/update races. On any error return zero start. Keep `GetNextPreKeyID`/`GetNextKyberPreKeyID` as nonallocating compatibility snapshots of durable positions and ordinary counts; return explicit exhaustion rather than truncate an unrepresentable position. Generic key writes advance existing allocator positions monotonically in the same transaction as the record write, without changing active references or creating missing metadata; initial/import writes are followed by explicit seeding. Deletion must never delete or reseed allocator rows.
- [x] **Step 5: Run scoped tests on both backends, then full fork suites.** Expected: all pass. Commands: pure-Go scoped command above; `CGO_ENABLED=1 go test -race -count=1 -run '^TestPreKeyAllocator' ./pkg/signalmeow/store`; `CGO_ENABLED=0 go test -tags libsignal_go -count=1 ./pkg/libsignalgo/... ./pkg/signalmeow/...`; `CGO_ENABLED=1 go test -race -count=1 ./pkg/libsignalgo/... ./pkg/signalmeow/...`.
- [x] **Step 6: Commit only Task 1 fork paths:** `feat(store): reserve durable prekey ID ranges`. Record commit and verification in the execution ledger.

## Task 2: Refill and atomic provisioning integration

**Files (fork):** Modify `pkg/signalmeow/keys.go`, `provisioning.go`, `keycheck_lifecycle_test.go`, `store/container.go`; create `pkg/signalmeow/prekey_refill_test.go`, `prekey_refill_sqlite_cgo_test.go`, `prekey_refill_sqlite_purego_test.go`, `store/provisioning_prekeys.go`, `store/provisioning_prekeys_test.go`. The refill package needs its own test-driver registration; the store package's `_test` imports do not register drivers in another test binary.

**Interfaces:** Consume Task 1's counts, reservation and seed methods. Add `StoreProvisionedDevice(ctx context.Context, data *DeviceData, keys ProvisioningPreKeys) (*Device, error)` to `DeviceStore` and implement it on `Container`. `ProvisioningPreKeys` contains `ACISigned`, `PNISigned` (`*libsignalgo.SignedPreKeyRecord`) and `ACILastResort`, `PNILastResort` (`*libsignalgo.KyberPreKeyRecord`). The method is the explicit credential-replacement boundary, not an ordinary deletion/reset API. Keep public generator and refill signatures unchanged.

- [x] **Step 1: Write `TestPreKeyRefillDurableRanges`, `TestPreKeyRefillFailureAfterReservation`, `TestPreKeyRefillOrdinaryKyberCounts` and `TestPreKeyRefillActiveReferences`.** Exercise actual refill against SQLite for ACI and PNI: count `0` ordinary plus retained LR yields `PREKEY_BATCH_SIZE` ordinary keys, starts at persisted next (including zero), and never collides with LR. Consuming the highest/all ordinary keys and reopening cannot rewind IDs. Inject a record-save failure after a committed reservation; partial rows may remain, but the next refill starts beyond the whole reserved batch. SQL reservation failure must generate/store no keys. Existing server-count threshold/reupload behavior remains correct. Ordinary refill leaves signed/LR active references unchanged; ordinary Kyber usage deletes a key while LR use retains it. Fakes model new count/reservation methods explicitly, without relying on nil embedded interfaces.
- [x] **Step 2: Write `TestProvisionedPreKeyAllocators` and `TestProvisionedPreKeyRollback`.** Persist real fixed-ID keys, reopen and assert both typed scopes have next `1/2/2`, active signed/LR `1`, and no ordinary Kyber. Replace an existing device with a changed PNI; on success old keys/sessions/allocator scopes are cleared and new state is initialized. Inject clear, key-write and seed failures after replacement starts; each must preserve old credentials, records and metadata and return no provisioned device. Invalid/missing/non-ID-1 bootstrap keys fail before publishing success.
- [x] **Step 3: Run `CGO_ENABLED=0 go test -tags libsignal_go -count=1 -run '^(TestPreKeyRefill|TestProvisionedPreKey)' ./pkg/signalmeow/...`.** Expected: assertions fail on retained-max allocation/LR-inclusive counts or missing provisioning method before implementation.
- [x] **Step 4: Integrate refill.** Query `PreKeyCounts`, reserve exactly the missing ordinary batch before invoking generators, then save/upload through existing paths. Preserve generated ID bounds and existing reupload behavior. Never allocate from `GetNext*` snapshots. Propagate reservation/store errors. A panic/cancellation/interruption after reservation cannot roll its transaction back. Do not add production RNG hooks to test interruption.
- [x] **Step 5: Integrate provisioning.** `StoreProvisionedDevice` rejects an outer transaction and runs credential insertion, loading the new device, clearing old protocol keys/sessions, explicitly clearing old allocator scopes, storing local identities/four fixed keys and seeding both scopes in one fork-owned transaction. Return the device only after commit. Replace the existing unchecked clear/fixed-key writes in `provisioning.go` with this method and propagate errors through `StateProvisioningError`. Profile storage and network registration retain their existing ordering; no claim is made that the complete remote provisioning protocol is transactional.
- [x] **Step 6: Run scoped tests and both full fork suites from Task 1.** Expected: all pass, including existing key-check worker lifecycle and unlink behavior.
- [x] **Step 7: Commit only Task 2 fork paths:** `fix(keys): use reserved prekeys and initialize provisioning atomically`.

## Task 3: Parent acceptance, fork release and roadmap completion

**Files (go-signal):** Create `internal/store/prekey_allocator_test.go`, `scripts/test-prekey-allocators.sh`, `docs/account-import-allocators.md`; modify `justfile`, `docs/dev.md`, `docs/maintenance.md`, `go.mod`, `go.sum`, `PLAN.md`, and this plan's completion boxes.

**Interfaces:** Consume released fork APIs against the parent's real account DB, including `Device.DoDecryptionTxn` for seed/record transaction tests. `TestPreKeyAllocatorBackendStep` uses `GOSIGNAL_TEST_ALLOCATOR_DIR` and `GOSIGNAL_TEST_ALLOCATOR_ACTION=init|reserve|abort|check`. Script builds separate native/race and pure-Go store-test binaries and requires the expected action ledger for both native-first and pure-Go-first runs. `abort` commits a reservation then exits its test process without generating keys; subsequent process must allocate beyond it. Do not couple this to Java corpus regeneration or change checksummed fixtures.

- [x] **Step 1: Write `TestPreKeyAllocatorAccountStore`, `TestPreKeyAllocatorBackendStep` and the runner's expected sequence assertions.** Each sequence seeds sparse ACI and PNI records with different counters/active references, allocates and stores ordinary EC/Kyber, consumes them, reserves and abandons a batch, switches backend, and proves monotonic positions, no reuse, LR retention and unchanged active references. Missing/skipped/duplicate actions fail the runner. Exercise seed/record rollback on the account DB. Run the tests/script before implementation and confirm the expected missing API/runner failure.
- [x] **Step 2: Implement the runner and CI commands.** Use isolated temporary databases and no production Signal network. Add pure-Go allocator contract/SQL tests to `just test-fork`, both native/race and pure-Go/race fork tests plus the backend runner to `just test-diff`. Add documented commands and compatibility-floor/legacy-history limitations. Preserve default Java-free checks and existing Java acceptance runner.
- [x] **Step 3: Run local-workspace acceptance.** Expected: zero exits for `just fmt`, `just lint`, `just check`, `just check-purego`, `just test-fork`, `just test-diff`, `CGO_ENABLED=0 go test -count=1 ./...`, and `scripts/test-prekey-allocators.sh`. Record action counts and exact local fork commit; new SQL tests must run, not skip, on both backends. PostgreSQL runtime acceptance is outside the initial Linux/SQLite importer target; preserve the existing fork dialect support without claiming it was exercised.
- [x] **Step 4: Obtain one independent whole-change review before publication.** Give the reviewer both repo/fork diffs, this plan/spec, verification results and all ledger rulings. Resolve material findings with reproducing tests and rerun affected/full checks. Recheck remote fork/parent tips; preserve unrelated changes and never force-push.
- [x] **Step 5: Publish the verified fork normally and pin its next unused immutable version in go-signal.** Verify remote branch/tag SHA against the reviewed commit, then resolve the release with `GOWORK=off`. No native/submodule or libsignal-go release is needed. Run `go mod tidy`; commit no temporary workspace.
- [x] **Step 6: Repeat release-graph acceptance with `GOWORK=off`.** Expected: all Step 3 commands pass. Also run `scripts/test-account-import-compatibility.sh --java` with the retained verified private Java 25/Maven toolchain and cache, requiring all 264 actions/48 Java legs; failure is unfinished integration, never a skipped leg.
- [x] **Step 7: Write durable acceptance evidence and update planning once.** `docs/account-import-allocators.md` records released version/SHA, tested contracts, commands/results, action ledger and limitations. Tick Stage 2a in `PLAN.md` only after integrated release checks pass; leave Stage 2b/import/live acceptance open and identify Stage 2b as next. Mark this plan's executed steps complete. Run formatting/lint before committing only this batch: `feat(import): establish durable prekey allocation prerequisites`. Ship normally on authorized main and report the commit, acceptance and remaining Stage 2b.

## Plan self-review

Prepared 2026-10-11 against the approved design and two independent read-only contract investigations. The five Review Focus inputs have explicit owning tests. Task 2 consumes Task 1's identical signatures; Task 3 consumes the released versions of both. Import conversion transaction exposure belongs to the later converter, while Stage 2a proves the fork's seed operation participates in shared dbutil transactions. No importer, live account use, source mutation, storage-key feature or Java fixture rewrite is included.

Executed 2026-10-14 after the written-plan review above. Fork commits `5656c11` (Task 1), `23de57b` (Task 2) and `74fc1aa` (fix from the pre-release review: refill failures are joined with the upload result) were released as `v0.2609.0-purego.31`. Deviations from the task text: Task 2's tests were written after the implementation, and their expected failure was confirmed by restoring the released refill (interrupted-reservation reuse and missing exhaustion were reported). The `aci-refill`/`pni-kyber-refill` key-check cases use fakes; "real SQLite" covers the store and refill tests. Evidence: [account-import-allocators.md](../../account-import-allocators.md).
