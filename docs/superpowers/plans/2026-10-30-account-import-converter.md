# Offline Account Converter Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use `executing-plans` to implement this plan task by task inline. Use `subagent-driven-development` only if the user chooses delegation. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Complete Phase 16 Stage 5: convert a stopped signal-cli linked-account snapshot offline, publish it without changing the source or replacing an account, and expose the result through `account import` and its guide.

**Architecture:** A standalone app use case calls an injected importer; it never opens a live client. `internal/signal/accountimport` validates and copies the source, converts it in one transaction, and uses Stage 4's private build and publication protocol. Existing allocator, storage-key and privacy helpers supply the prerequisites; two small store additions provide atomic identity seeding and the final source check.

**Tech Stack:** Go 1.26, Cobra/Viper, `database/sql` with the existing mattn/modernc backend split, pinned libsignalgo record APIs, directory-rooted filesystem access, Linux publication primitives, existing Java-generated protocol/vector fixtures.

**Spec:** [Approved importer design](../specs/2026-10-10-account-import-design.md), especially “Command and package boundary”, “Source snapshot and conversion” and Stage 5; [source-format contract](../../account-import.md). Read both before implementation.

**Status:** Draft for review, 2026-10-30; baseline `99a813a`, mautrix-signal `v0.2609.0-purego.33`, libsignal-go `v0.7.1-cw.6`. The user approved drafting this plan and a documentation PR, not converter implementation. Decisions below are proposals within the approved design; Stage 5 remains open.

## Global Constraints

- Scope is Stage 5 only. Stages 1–4 are prerequisites already delivered. No fork release, protocol-format expansion, primary registration, source cleanup, or live server/phone operation is part of this plan.
- Target signal-cli v0.14.9: registry 2, account JSON 11, SQLite schema 31. Require registered LIVE linked devices (`deviceId > 1`), valid nonzero ACI/PNI, both identity pairs and registration IDs, E.164 number, password and a valid own profile key.
- Initial publication is Linux-only. Unsupported platforms refuse before creating a destination; ordinary committed data remains usable with either backend.
- The source is a complete copy made while signal-cli, its daemon and scheduled invocations are stopped. Keep the source writer stopped afterward. File checks detect changes; they do not prove Java quiescence or snapshot consistency.
- Open the supplied tree read-only using directory-rooted filesystem operations. Reject absolute/traversing registry paths, symlinks in required paths, special files and source/destination overlap, including physical aliases.
- Bound registry and account JSON to 4 MiB each, the copied SQL database plus WAL/SHM to 4 GiB total, each protocol blob to 1 MiB, total protocol rows to 1,000,000, recipient rows to 100,000 and group rows to 10,000.
- Reject duplicate JSON keys, duplicate protocol keys and ambiguous address mappings. Check SQLite integrity and exact expected schema/field types. Stream copying and row conversion.
- Require an empty pending message cache, no pending failed-message retry, and empty send-log tables. Never delete or checkpoint source data to satisfy these requirements.
- Reject primary/staging/unregistered accounts, unsupported versions or protocol encodings, inconsistent active references, detected allocator rollover, legacy groups, blocked groups, groups with disabled profile sharing, blocked contacts without an addressable ACI, phone-sharing `CONTACTS`, and unsupported nondefault mute/archive/hidden/story settings.
- Preserve local ACI/PNI namespaces, whole sessions and archived/skipped state, retained prekeys and incoming sender chains. Generate no replacement identities, credentials or retained prekeys. Import no outbound sharing authorization; the first outbound group send needs a fresh key and distribution ID.
- Import both trust copies atomically; unknown history stays unknown. Seed protected ACI trust observations, keep PNI trust independent, and enqueue no verification sync or identity-change events.
- Preserve source storage-key forms without deriving an already-derived key again. Explicit false privacy values and absent SQL settings are distinct. Source metadata is provenance, never installed target sync progress.
- Build under the existing locked `.imports/` attempt, with directories 0700 and files 0600. After durable publication intent, errors retain recovery evidence rather than claiming a clean cancellation.
- Results contain account identifiers, record counts, fixed exclusions and source format markers only. Never print credentials, keys, raw failing records or message content. `LinkedAt`, `LastSync` and unavailable device names stay unknown.
- No runtime Java requirement, committed database, `replace`, `go.work`, or new Go dependency is planned. Keep existing fixture bytes, provenance, ledgers and Java generators unchanged. Build caches stay under `~/.cache`, `~/go` or `/tmp`.

## Review Focus

1. An omitted `retry-failed-messages` row means **true** in Java, not false; reject it rather than importing potentially pending work (Task 3).
2. A nonexistent destination beneath a source alias must be rejected before `store.OpenDir` creates anything; file hard links and path-component replacement must not bypass source protection (Task 2).
3. A source changed while waiting for the registry lock must fail before intent, including newly appearing WAL/cache files; cancellation after intent must retain recovery evidence (Tasks 2 and 6).
4. A valid archived session can contain an older remote identity or lack a usable current sender chain; preserve it while still rejecting a mismatched local identity (Task 4).
5. Output failure after successful publication must not undo the account or invite a destructive retry; retry is a collision, and the guide must explain inspection/recovery (Tasks 7 and 8).

---

## Current APIs and missing integration

| Area               | Existing implementation                                                                                                                                | Stage 5 use or gap                                                                                                                                                             |
| ------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| Publication        | `Dir.BeginImport`, `Import.Build(ctx, aci, build, log)`, `Import.Publish(entry)`, `Import.Close` in `internal/store/imports.go`                        | Build already checkpoints, closes, reopens and syncs the target. `Publish` lacks the required source-validation callback under the registry lock.                              |
| Store transactions | Fork `Device.DoContactTxn`/`DoDecryptionTxn`; parent writers use transaction-bearing contexts on the same dbutil root                                  | The device must itself be inserted inside conversion's transaction. Add a parent wrapper around that root rather than inserting a device first or opening a second connection. |
| Identity trust     | Fork `SaveIdentityKey` always writes `TRUSTED_UNVERIFIED`; parent `ApplyIdentityTrust` updates only the facade copy                                    | These calls alone cannot restore all three source trust levels. Add one import-only store operation that inserts both copies and protected ACI observations.                   |
| Allocators         | `Container.SeedPreKeyAllocator(ctx, accountACI, serviceID, state)`                                                                                     | Requires the enclosing transaction, retained records first, and both local service namespaces. No allocator redesign.                                                          |
| Keys and privacy   | `DeviceData.ResolveStorageServiceKey`, `AccountOverlay`, `AccountGuards`, `PrivacyGuardStore.SeedPrivacyGuards`                                        | Read actual SQL presence/types and initialize guards; do not rebuild Stage 2/3 runtime reconciliation.                                                                         |
| Existing corpus    | `fixtures_test.go` restores protocol records directly into target stores; `schema.sql`, `rows.sql`, `account.json`, `corpus.json` hold source evidence | Direct restoration does not test conversion. Materialize stopped source trees from existing bytes in tests, then exercise the real importer.                                   |
| Commands           | `rootOptions.newClient`, `newAccountCmd`, `clientOpener.open`; output consumes app/facade types                                                        | Add a separate importer option and standalone app function. Import must never call the client factory.                                                                         |

Source details were checked against `SignalAccount.java`, `ConfigurationStore.java`, `MessageCache.java`, the protocol stores and the schema extraction in the committed corpus. The source retry flag defaults to true; message files live in `data/<path>.d/msg-cache/`; `storage-manifest-version` defaults to -1. Historical plan instructions to commit to `main` belonged to earlier stages; this batch uses a dedicated branch and PR.

## Proposed implementation decisions

- **D1 Public boundary.** Put backend-neutral request/result values and import error sentinels in `internal/signal/account_import.go`. `app.AccountImporter` consumes these values; `accountimport.Importer{}` implements it without importing `app`. Keep the live `signal.Client` interface unchanged.
- **D2 Source selection.** A registry entry is initially eligible when it is LIVE and has valid registry number/ACI/path fields. Require exactly one such entry without a selector, then fully validate its JSON/SQL. Never skip an invalid selected account and silently choose another. Explicit E.164/ACI selection must be unique; duplicate selected identities/paths are errors. Require a nonempty `--source-dir`; expand destination `~/` and defaults consistently with existing store behavior before overlap checks.
- **D3 Snapshot workspace.** Keep an importer-owned private workspace under the system temp directory, outside the source and destination account path, with a cleanup defer. Pin source directory/file descriptors. Use no-follow directory traversal, reject nonregular files before reading, and fingerprint file identity, length, mtime and streamed SHA-256. Record optional-file absence and bounded message-cache directory listings too. SQLite opens only the copied files.
- **D4 Schema and parser.** Reject duplicate JSON members at every nesting level and trailing values. Require the reviewed v0.14.9 field/type contract; unknown fields require review rather than permissive interpretation. Compare SQLite schema semantically (tables, columns, declared types, constraints and required indexes), not by whitespace or object creation order. Reject unexpected executable schema objects. Check `typeof` for consumed values, strict booleans/enums, integrity and foreign-key consistency before conversion.
- **D5 Freshness.** Treat the snapshot as unverified remote freshness: compare cached records, local fields and storage IDs for consistency, but arm every supported imported privacy value, including equal values (`AccountGuards(..., fresh=false)`). Use the validated SQL `storage-manifest-version` as `ArmedVersion`; absent/-1 becomes 0, other negatives or malformed values fail. Do not deserialize/copy the manifest file, install source storage IDs, or advance target storage highwater. A strictly newer authenticated matching snapshot is still required to release a guard. This conservative proposal avoids inventing a remote-freshness proof from a stopped local cache.
- **D6 Allocation and missing keys.** Require next IDs strictly above retained maxima and at most `0xffffff`; impose no additional arbitrary headroom. A position at the exclusive limit is preserved as exhausted, and normal refill refuses to wrap. Map source active-reference sentinel `-1` (or absence) to nil; preserve active ID 0 and reject other negatives. Require the three next-ID fields rather than deriving them from retained rows. All absent storage-key forms remain unknown; accept only `ErrStorageServiceKeyUnknown` in that case, never malformed or conflicting supplied forms. Explain that later key acquisition is a connected operation.
- **D7 Final validation.** Add `PublishChecked(entry, validate)`; keep `Publish(entry)` as the existing unchecked wrapper for its callers. Under the registry lock, after recovery/collision checks and before marker/intent, call `validate`, which checks context and fully revalidates the pinned source and path bindings. Hash immediately before this section too. The final full hash can hold the registry lock for the duration of a 4 GiB read; another process may receive existing `ErrRegistryBusy`. This preserves the design's source check but needs explicit review of the lock-duration tradeoff, not a silent metadata-only shortcut.
- **D8 Error and output contract.** Add fixed import errors for invalid input, unsupported input, ambiguous selection and changed source; preserve cancellation and existing collision/recovery errors through `%w`/`errors.Join`. At the importer boundary, replace driver/parser diagnostics that contain source values with safe category/row context; no untrusted SQL values or raw paths from registry entries appear in errors. A published result means local publication only. Render failures leave that publication intact.
- **D9 Exclusions.** Return a sorted fixed inventory, independent of cache presence: `attachments`, `avatars`, `cdsi-tokens`, `endorsements`, `message-history`, `outbound-sharing`, `source-only-metadata`, `stickers`, `storage-cache`, `unsupported-preferences`. These name unmapped categories; nondefault unsupported preferences and pending work still cause refusal. No scanning or counting unrelated image/history trees. Inbox, polls and pins start empty.

These choices do not authorize broader source support. If a required invariant cannot be inspected using the pinned APIs, stop and report the precise missing prerequisite; do not accept the record or add an unreviewed fork change.

## Files and interfaces

All names in this section are proposed new interfaces unless marked existing. Tests stay in external `_test` packages; a narrowly scoped `export_test.go` may expose deterministic barriers and lowered test limits, following the repository pattern.

| Paths                                                                                                                                                       | Responsibility                                                                                |
| ----------------------------------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------- |
| `internal/signal/account_import.go`                                                                                                                         | Plain Go DTOs and import error sentinels, available in every build.                           |
| `internal/app/account_import.go`, `_test.go`                                                                                                                | Injected offline use case and forwarding/error tests.                                         |
| `internal/signal/accountimport/importer.go`, `importer_nocgo.go`, `limits.go`, `export_test.go`                                                             | Real/stub orchestration, fixed limits and per-instance test seams.                            |
| `internal/signal/accountimport/source.go`, `source_linux.go`, `source_other.go`, `snapshot.go`, `json.go` and corresponding tests                           | Source-root traversal, overlap checks, bounded snapshot copying/fingerprints and strict JSON. |
| `internal/signal/accountimport/source_sql.go`, `source_sql_cgo.go`, `source_sql_purego.go`, `source_schema.go`, `source_records.go` and corresponding tests | Private SQLite opening, schema/type validation and bounded row readers.                       |
| `internal/signal/accountimport/convert.go`, `convert_protocol.go`, `convert_contacts.go`, `convert_settings.go` and corresponding tests                     | Transactional conversion using the existing protocol, key and privacy helpers.                |
| `internal/signal/accountimport/snapshot_test.go`, `importer_test.go`, `importer_nocgo_test.go`, `converter_backend_test.go`                                 | Materialized source fixtures, exact reopened values, refusals and subprocess acceptance.      |
| `internal/store/import_data.go`, `import_data_test.go`; `imports.go`, `publication_test.go`                                                                 | Conversion transaction, atomic trust seeding, checked publication and regression coverage.    |
| `cmd/account_import.go`, `_test.go`; `cmd/account.go`, `cmd/root.go`                                                                                        | Flags, importer injection and account-subcommand wiring.                                      |
| `internal/output/account_import.go`, `_test.go`; `cmd/testdata/account_import_plain.golden`, `account_import_json.golden`                                   | Safe plain/JSON result, fixed count/exclusion shape and output tests.                         |
| `scripts/test-account-import-converter.sh`, `justfile`, `.github/workflows/test-cgo.yaml`                                                                   | Both-backend converter subprocess checks and Linux network-syscall acceptance.                |
| `docs/account-import-guide.md`, `docs/account-import.md`, `docs/json.md`, `docs/dev.md`, `README.md`, `PLAN.md`, spec status and this plan                  | User guide, output contract, verification procedure and honest completion records.            |

The public types are fixed here so tasks cannot diverge:

```go
// internal/signal/account_import.go; no backend imports.
type AccountImportOptions struct { SourceDir, SourceAccount, DataDir string }
type AccountImportFormat struct { Registry, AccountJSON, SQLite int }
type AccountImportCounts struct {
    Identities, Sessions, PreKeys, SignedPreKeys uint64
    KyberPreKeys, LastResortKyberPreKeys, SenderKeys uint64
    Recipients, Groups, ChatTimers uint64
}
type AccountImportResult struct {
    Account Account
    Counts AccountImportCounts
    Excluded []string
    SourceFormat AccountImportFormat
}

// internal/app/account_import.go.
type AccountImporter interface {
    Import(context.Context, signal.AccountImportOptions) (signal.AccountImportResult, error)
}
func ImportAccount(context.Context, AccountImporter, signal.AccountImportOptions) (signal.AccountImportResult, error)

// internal/signal/accountimport/importer.go or importer_nocgo.go.
type Importer struct { /* private per-instance hooks; zero value usable, no construction I/O */ }
func (Importer) Import(context.Context, signal.AccountImportOptions) (signal.AccountImportResult, error)

// internal/store/import_data.go; use the context on the same dbutil root.
func (*Store) ImportTransaction(context.Context, func(context.Context) error) error
func (*Store) SeedImportedIdentity(context.Context, string, IdentityRecord) error

// internal/store/imports.go; the callback runs once, before intent, under the registry lock.
func (*Import) PublishChecked(AccountEntry, func() error) error

// Command/output extensions.
func WithAccountImporter(app.AccountImporter) Option
func (*Printer) AccountImport(signal.AccountImportResult) error
```

`Identities` counts source identity rows, not both target copies. `KyberPreKeys` includes the `LastResortKyberPreKeys` subset. All counts cover retained converted records in both local namespaces; count the self recipient once, including when reconstructed from the required own JSON fields. `Groups` counts group-v2 master keys, not copied cached state. Count fields always render, including zeros.

## Task 1: Define the offline app boundary and backend stub

**Files:** New neutral DTOs, `internal/app/account_import.go`/tests, and importer real/stub entry files from the file map.

**Interfaces:** Produces the public boundary above. Add `signal.ErrInvalidAccountImport`, `ErrUnsupportedAccountImport`, `ErrAmbiguousAccountImport`, `ErrAccountImportSourceChanged`. The real implementation remains unexposed in the command tree until Task 7.

- [ ] Write `TestImportAccount` with a recording importer: forward the exact three request strings and context once, return the exact result, wrap errors while preserving `errors.Is`, and never need `app.New` or a `signal.Client`. Reject an empty source directory before calling the importer.
- [ ] Run `CGO_ENABLED=0 go test -count=1 -run '^TestImportAccount' ./internal/app/`; expect missing definitions before implementation.
- [ ] Add the DTOs and standalone forwarding use case. In `!cgo && !libsignal_go`, implement `Importer.Import` to return `signal.ErrCGORequired` without accessing either supplied path. Real files use `cgo || libsignal_go`; non-Linux refuses before destination creation.
- [ ] Add `TestAccountImportNoBackend` with inaccessible/nonexistent source/destination paths; verify the backend-required error and an unchanged filesystem. Run untagged no-cgo tests and compile the real boundary with `CGO_ENABLED=0 go test -tags libsignal_go -run '^$' ./internal/signal/accountimport/`; expect PASS.
- [ ] Commit only this task's paths: `feat(import): define offline account import use case`.

## Task 2: Acquire a bounded, unchanged source snapshot

**Files:** Source, snapshot, strict JSON, limit and test files in the map.

**Interfaces:** Internal `openSnapshot(ctx context.Context, opts signal.AccountImportOptions) (*snapshot, error)`, `(*snapshot).Revalidate(ctx context.Context) error`, `(*snapshot).Close() error`. A snapshot owns its temp directory, pinned source descriptors, selected registry/JSON values, private SQL path and absence/listing fingerprints; it exposes no target account yet. Source SQL consumption arrives in Task 3.

- [ ] Write `TestAccountImportSnapshot` for the settings-root layout, single/explicit/ambiguous selection, duplicate nested JSON keys, trailing JSON, size limits, missing JSON, unsupported markers, LIVE identity mismatch and absent/null/wrong-typed required fields. Assert selection never falls through after failure.
- [ ] Write `TestAccountImportPaths`: traversal/absolute/basename violations, required symlinks (including root/ancestors), dangling links, FIFO/device files without blocking, source equal to/inside/above destination, nonexistent destination ancestors, hard-linked required files into an existing destination, and physical aliases. Assert refusal before destination creation and no source writes.
- [ ] Run the named tests on the pure-Go backend; expect missing snapshot behavior. Implement Linux directory-descriptor traversal with no-follow component opens, bounded regular-file reads/copies and destination identity checks. Resolve defaults/`~/` without calling the creating `store.OpenDir`. Open no source SQL connection. Close partial resources on every failure.
- [ ] Write `TestAccountImportSourceChanged` using per-instance barriers: truncate, same-length edit, replace/rebind a parent or file, add/remove/replace WAL/SHM, add a cached message in an initially empty cache, and cancel during a streamed copy/hash. Revalidate from the rooted paths as well as held file descriptors. Assert the changed-source/cancellation error and private-workspace cleanup. Exercise JSON/DB/WAL byte limits with lowered test limits and one real 4 MiB JSON boundary, avoiding multi-gigabyte fixtures.
- [ ] Implement final fingerprint/absence/listing revalidation and rerun all snapshot/path/change tests with `CGO_ENABLED=0 go test -tags libsignal_go -count=1 -run '^TestAccountImport(Snapshot|Paths|SourceChanged)' ./internal/signal/accountimport/`; expect PASS. Keep test hooks per instance, never global mutable barriers.
- [ ] Commit: `feat(import): read stopped source snapshots without mutation`.

## Task 3: Validate source SQL, configuration and supported state

**Files:** SQL openers, schema/row readers, `source_records.go` and associated external tests.

**Interfaces:** `(*snapshot).openSource(ctx context.Context) (*sourceDB, error)` owns only private-copy SQLite handles; `(*sourceDB).validate(ctx context.Context, snap *snapshot) error`; `(*sourceDB).Close() error`. Bounded readers feed one row at a time to Task 4/5 converters, with explicit SQL-null and typed-service-ID representations. Do not accumulate every protocol blob in memory.

- [ ] Materialize full schema-31 source trees in `snapshot_test.go` from the committed schema and Java records, with invented credentials. Supplement runtime test rows only; retain original corpus files/checksums. Add explicit `retry-failed-messages=0` to successful fixture variants because source absence means true. No Java generator execution or replacement corpus is needed.
- [ ] Write `TestAccountImportSourceSQL` and `TestAccountImportPendingWork`: wrong version with plausible tables, wrong shape/types/constraints, corrupt DB, executable views/triggers, duplicates, bad typed addresses, SQL NULL versus false, invalid booleans/enums, excessive rows/blob length, and all three nonempty send-log tables. Retry absent/true/mistyped and cached messages must refuse; false plus empty cache/logs passes. Empty cache subdirectories may remain; never delete them.
- [ ] Run tests to observe refusal/validation failures. Implement private-copy SQLite handling for each existing driver, checking raw schema before querying source tables and bounding SQL length/blob allocation before `Scan`. Preserve copied WAL/SHM names; allow recovery only on the private copy, never use source `immutable=1` to bypass WAL. Disable trusted-schema execution and avoid driver diagnostics containing source values.
- [ ] Add `TestAccountImportUnsupportedState`: legacy/blocked/nonsharing groups; blocked PNI-only and number-only recipients; nondefault mute/archive/hidden/story flags; unsupported phone sharing; unmappable direct timers; unknown privacy/key-value types. Reject namespace/address conflicts without inventing an ACI. Permit unblocked addressless contacts only when their supported fields can be represented faithfully.
- [ ] Add `TestAccountImportWAL`: keep committed rows solely in an uncheckpointed WAL, copy/import them, compare source files/listings/modes before and after success, refusal and cancellation. Run SQL/pending/unsupported/WAL tests on both backends; expect PASS. Source readers must not instantiate signal-cli's mutating Java loader.
- [ ] Commit: `feat(import): validate the supported signal-cli source contract`.

## Task 4: Convert protocol records and seed trust/allocators atomically

**Files:** `internal/store/import_data.go`/tests; `convert.go`, `convert_protocol.go`/tests.

**Interfaces:** The proposed store methods above plus `convert(ctx context.Context, target *store.Store, source *sourceDB, snap *snapshot) (signal.AccountImportCounts, error)`. Inside `target.ImportTransaction`, insert/load the device and pass the transaction-bearing context to every fork and parent writer. Task 5 extends that same conversion transaction; it does not introduce another commit.

- [ ] Write `TestImportDataTransaction` and `TestSeedImportedIdentity` in `store_test`: device/protocol/facade/allocator/guard writes roll back together on failure or cancellation; all source trust levels produce exact protocol strings `UNTRUSTED`, `TRUSTED_UNVERIFIED`, `TRUSTED_VERIFIED` and matching facade strings. Refuse seeding outside the transaction or over an existing identity. ACI observations are dirty with local key/trust, remote unknown; PNI gets no ACI observation. History timestamps remain zero, previous key nil, pending event false, sync outbox empty.
- [ ] Run to observe missing APIs, then implement `ImportTransaction` on the store's existing dbutil root. `SeedImportedIdentity` validates source-independent DTOs and inserts both identity copies plus the ACI observation in that transaction; it must not call a normal user trust command or rely on `SaveIdentityKey`'s default trust.
- [ ] Write `TestAccountImportProtocol`: both local namespaces, equal ACI/PNI UUIDs, IDs including zero, component EC reconstruction, retained old signed/last-resort keys, signatures, exact millisecond timestamps, malformed Kyber encoding/metadata and full session inspection. Check every current/archive local identity and registration binding; pending-prekey creation timestamps remain seconds, distinct from signed/Kyber millisecond timestamps. Preserve historical archived remote keys; require the current state, when present, to agree with its known remote identity entry. Never require time-dependent sender usability as structural validity.
- [ ] Implement protocol conversion with the pinned backend-neutral APIs and full serialized records. Validate public/private agreement using key derivation or a cryptographic pair check, including Kyber: the pinned `session.DeserializeKyberPreKeyRecord` exposes `PublicKey`/`SecretKey`, and their `Encapsulate`/`Decapsulate` methods allow a pair check without replacing a key. Keep these dependency types inside the facade tree and still deserialize with the selected backend. If the APIs cannot prove an invariant, record the missing prerequisite and stop. Validate sender UUID BLOBs, distribution IDs, device IDs and namespace tags. Keep retained incoming sender keys, but leave outbound sender-key info empty and do not turn local outbound records into receive authorization.
- [ ] Write `TestAccountImportAllocatorSeed` for both local namespaces: empty/depleted/sparse stores, absent/`-1` active references, active ID 0, other negative references, active-key mismatch, next IDs at/below maxima, exhaustion and rollover. After inserting records call existing `SeedPreKeyAllocator` for ACI and PNI using original next/active values. A mutation failure at each record category must roll back every category. Reopen and compare bytes, trust copies, allocator state and counts, including no new history/outbox/events.
- [ ] Run `CGO_LDFLAGS="-L $PWD/third_party/lib" go test -race -count=1 -run '^(TestImportDataTransaction|TestSeedImportedIdentity|TestAccountImportProtocol|TestAccountImportAllocatorSeed)' ./internal/store/ ./internal/signal/accountimport/`, then the pure-Go equivalents; expect PASS. Commit: `feat(import): convert protocol state with durable trust and allocators`.

## Task 5: Convert contacts, groups, keys and guarded privacy

**Files:** `convert_contacts.go`, `convert_settings.go` and tests; extend `convert.go` inside its existing transaction. Reuse `overlay.go` unchanged unless a demonstrated source-contract mismatch requires review.

**Interfaces:** Internal `convertContacts(ctx context.Context, target *store.Store, device *mstore.Device, source *sourceDB) (signal.AccountImportCounts, error)` and `convertSettings(ctx context.Context, target *store.Store, device *mstore.Device, source *sourceDB, snap *snapshot) error`. Dependency types stay inside the facade tree. Accumulate counts without counting a source row twice.

- [ ] Write `TestAccountImportContactsGroups`: validated typed associations, contact/profile names and keys, false/true blocking and profile sharing, `needs_pni_signature`, direct timers (`0..4294967295` seconds/version), source group ID derived from its 32-byte master key, malformed/mismatched keys and disabled group profile sharing. Number-only recipients retain only representable fields; no fake ACI or implicit trust. Import no group cache, endorsements, source permission-denied/left claim or outbound share list.
- [ ] Write `TestAccountImportKeysPrivacy` using the existing Java storage-key and overlay vectors: derived-only, master-only, pool-only, agreeing/mismatching forms, all absent, malformed supplied forms, own/recipient profile-key disagreement, valid/malformed backup key, nil SQL settings and explicit false. The effective account record must match Java overlay behavior. An unavailable encrypted device name remains unknown.
- [ ] Run these tests to observe missing conversion. Implement via the existing recipient/group stores, `MergeChatTimers`, `AccountOverlay`, `AccountGuards` and `SeedPrivacyGuards`. Compare cached local/storage-ID values before applying D5; seed blocking/profile-sharing guards under both validated typed IDs. Protect even values that happen to equal cached records. Store the own profile key under the self ACI, preserve valid key forms and optional backup key, and leave ephemeral backup key absent.
- [ ] Write `TestAccountImportFirstStorageFetch`: through an actually converted and reopened account, apply conflicting, absent, repeated/older, genuinely matching newer and subsequently changed authenticated snapshots. Verify raw/effective values, block caches, story audiences, own key, settings and trust both immediately and after restart; inject rollback. Target storage highwater begins unset, while guard `ArmedVersion` is source provenance. PNI trust remains independent.
- [ ] Reopen and compare all converted values/counts; ensure inbox, poll/pin projections, sync outbox, block overrides, target storage cache and sync timestamps start empty/unknown. Run the contact/group/key/privacy/first-fetch tests under both backends and with cgo `-race`; expect PASS. Commit: `feat(import): preserve supported contacts and privacy decisions`.

## Task 6: Integrate checked publication and real-import acceptance

**Files:** Importer orchestration, store checked publication/tests, `importer_test.go`, `converter_backend_test.go`, `scripts/test-account-import-converter.sh`, and `justfile`.

**Interfaces:** Complete `Importer.Import` and `PublishChecked` above. Use `BeginImport` → `Build` with the one conversion transaction → snapshot revalidation → `PublishChecked(entry, validate)` → cleanup. Returned result is nonzero only after successful publication. The existing publication `Close` semantics govern pre-/post-intent cleanup.

- [ ] Write `TestPublishChecked`: validator runs once under the registry lock after collision checks and before any ownership marker/intent; a validator error preserves `errors.Is`, creates no selectable account, and leaves a cleanly removable uncommitted stage. Existing `Publish` callers and crash hooks retain behavior.
- [ ] Run it to observe failure; add the checked wrapper without changing rename/recovery ordering. Check cancellation before publication intent, but finish publication or report recovery-required once intent may be durable. A late cleanup/output failure must not misreport that the published account disappeared.
- [ ] Write `TestAccountImportPublication`: final-path/ACI/number/PNI collisions (including orphan, symlink and unlinked entry), concurrent imports of the same source, unrelated registry mutation, changed source while blocked on registry lock, cancellation during conversion/final validation and actual process termination before/after intent. Verify destination registry preservation, source byte identity and exact recovery/retry results. Reuse Stage 4's barriers rather than replacing its crash matrix.
- [ ] Add `TestAccountImportConverterBackendStep` and its shell runner: compile native and pure-Go test binaries into `/tmp`, materialize existing corpus scenarios as source snapshots, convert with one backend and reopen/continue with the other, in both starting orders. Retain the Stage 1 direct-restoration runner and ledger unchanged; distinguish its Java legs from this converter evidence. Verify all record categories, trust/guards/allocators and queued/session/group continuation through converted state. The first outbound group send must produce a fresh key under a fresh distribution ID, never rebind an imported key.
- [ ] Add `TestAccountImportOfflineProcess`: use the real importer in a subprocess for success and refusal while tracing `socket`/`connect` syscalls with Linux `strace -f`; fail on AF_INET/AF_INET6 socket creation, including failed connection attempts, and retain the existing nonnetwork fixture flow. Trace only the already-built subprocess, not Go builds or module downloads. Require an observable passing trace, not a silently skipped check. `strace` is installed in the planning environment; CI must install it in the differential job before this runner is made required.
- [ ] Add this converter runner to `just test-diff` and `strace` setup to `.github/workflows/test-cgo.yaml`'s differential job. Run the runner, scoped race/pure-Go tests and existing `scripts/test-account-publication.sh`; expect PASS. Commit: `feat(import): publish validated offline conversions recoverably`.

## Task 7: Expose the command and stable output

**Files:** Command, app and output paths/goldens in the map; root/account wiring and `docs/json.md`.

**Interfaces:** `WithAccountImporter(app.AccountImporter)` sets a separate root option, defaulting to `accountimport.Importer{}`. `newAccountImportCmd` receives configuration, that importer and the printer factory. It calls `app.ImportAccount` without `clientOpener.open`. `Printer.AccountImport` uses the DTO defined above.

- [ ] Write `TestAccountImportCommand`: require `--source-dir`, accept optional `--source-account`, reject positional arguments and supplied global `--account` (including explicitly empty, env and config values) with `--source-account` guidance. Verify flag > env > config for destination/output, cancellation forwarding, source selection and importer-call count. Install a client factory that fails the test if called, on successful and failing paths.
- [ ] Write plain/JSON goldens with an invented account, exact counts/format markers/exclusions, and unknown name/link/sync timestamps. JSON is `{"version":1,"accountImport":{"account":...,"counts":...,"excluded":[...],"sourceFormat":{"registry":2,"accountJSON":11,"sqlite":31}}}`. Count keys are lower camel case (`signedPreKeys`, `lastResortKyberPreKeys`, `chatTimers`); all count keys and exclusion entries are stable. Never include source paths, raw bytes or secrets.
- [ ] Run tests to observe missing command/output. Implement help's stopped-snapshot requirement, flags, standalone app call and renderer. Keep schema version 1; this adds a document rather than changing a field's meaning. Plain output says local publication completed and credentials/delivery remain unverified. No network validation/overwrite/discard/confirmation flag is introduced.
- [ ] Add `TestAccountImportOutputFailure` for a failing stdout writer after a real conversion: the account remains published, a new attempt returns collision, and the error/guide direct the user to `account show`. Add error/log tests with a synthetic secret marker in malformed JSON, SQLite values, path fields and protobuf bytes; capture verbose stderr too and assert the marker never appears.
- [ ] Run `CGO_ENABLED=0 go test -count=1 ./cmd/ ./internal/app/ ./internal/output/` and equivalent real-backend command/output tests plus `just fmt`/`just lint`; expect PASS. Update only the two new command goldens. Commit: `feat(cli): add offline account import and versioned output`.

## Task 8: Document and verify Stage 5 honestly

**Files:** Guide, README, JSON/dev docs, format-map status, spec status, this plan, `PLAN.md`; no Stage 6 completion marks.

**Interfaces:** User-facing example: `go-signal --data-dir <destination> account import --source-dir <stopped-snapshot> [--source-account <E.164-or-ACI>]`. Explain the source root containing `data/accounts.json`, eligibility, source exclusions, refusal/remediation, unknown timestamps, exhausted counters, absent storage keys, and local-only success.

- [ ] Write `docs/account-import-guide.md`: stop daemon/timers and copy a complete snapshot including WAL/SHM, protect the copy, stop older go-signal processes before import, and keep the original client stopped after switching. Do not advise deleting pending work to force acceptance. Upgrade unsupported source formats only in a separate copy with signal-cli, then stop it and take a new snapshot. Describe source-cache/unsupported-preference exclusions and the no-overwrite rule.
- [ ] Document the intent/recovery contract, a lost stdout/result after publication, collision on retry, and safe inspection through existing account commands. Do not suggest deleting arbitrary orphans or retrying with overwrite. Connected validation is a later operation; no automatic send/sync follows import.
- [ ] Add a developer evidence table mapping the Stage 5 criteria to tests, backend order and actual command results. Run all commands below after integration; fix only failures attributable to approved work. If a baseline/tool/criterion is blocked, record it and leave the relevant item open rather than claiming support.
- [ ] Reread the design/map against every task and inspect every changed path. Only after the full Stage 5 criteria pass, tick Stage 5 and its parent implementation checkbox, update the format map/spec status and record the implemented deviations. Keep Phase 16's fixture/live checkbox and “sends and receives without re-linking” acceptance open for Stage 6.
- [ ] Run formatting/checks again for the final records. Commit: `docs(import): document and record offline converter acceptance`; ship the reviewed implementation branch only when its own checks pass. Drafting this plan alone closes no implementation item.

## Verification and acceptance ledger to fill during implementation

Run from the repository root with `GOWORK=off`, `JUST_TEMPDIR=/tmp`, caches outside the project and `CGO_LDFLAGS="-L $PWD/third_party/lib"` for direct native Go commands. The native archive must match v0.102.2. Do not regenerate existing Java fixtures or their ledger.

| Criterion                                    | Required evidence                                                                                                                                                                 |
| -------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Exact converted/reopened values              | Tasks 4–6 tests through the real importer, both backends and both starting orders, all retained record categories.                                                                |
| Original source unchanged, including WAL     | Full file/listing/mode snapshots for success, refusal, cancellation and process crash; committed-WAL-only rows are present in the target.                                         |
| Invalid input/size/path/cancellation refusal | Tasks 1–4 negative tests, exact production bounds plus lowered-limit streaming cases, no selectable partial account, no destination creation on overlap/platform/backend refusal. |
| Privacy/trust/allocator persistence          | Converted-account conflicting first fetch, restart, alignment/later change, rollback, namespace isolation, exhausted/depleted counters and no outbox/history fabrication.         |
| Publication and retries                      | Source check under registry lock, collisions, real process termination, recovery on either backend, output failure after publication.                                             |
| No network calls                             | Real compiled-importer success/refusal subprocess traces plus command client-factory trap.                                                                                        |
| Output and guide                             | Plain/JSON goldens, fixed exclusion/count contract, secret-redaction cases and reviewed user guide.                                                                               |

Integrated commands:

```sh
just fmt
just check
just check-purego
just test-fork
just test-diff
CGO_ENABLED=0 go test -count=1 ./...
scripts/test-account-import-converter.sh
scripts/test-account-publication.sh
```

`just test-diff` retains the existing protocol/backend/publication prerequisites and includes the converter runner after Task 6. The Stage 1 `--java` exchange evidence remains separate: this batch reuses its immutable material and does not claim new Java-generated protocol coverage. Live Signal tests are excluded and cannot be inferred from this ledger.

For this documentation-only PR, run `just fmt`, `just check`, `git diff --check`, link/path checks and an inline source/API/spec coverage review. Record actual results in the PR; the implementation ledger above remains unfilled.

## Decisions requiring plan review

1. **Final source rehash:** D7 preserves the required check under the registry lock but can exceed its usual short critical section. Review that tradeoff before implementation; changing the check's strength requires a design amendment.
2. **Conservative privacy freshness:** D5 arms equal as well as conflicting local values and does not decode the source manifest. Review this conservative protection policy and its consequence that release waits for a strictly newer matching remote snapshot.
3. **Pending retry default:** the Java default means a source without an explicit false retry flag is refused even with an empty cache. This follows the approved no-pending-work boundary; confirm the guide makes the limitation clear without suggesting source mutation.
4. **Exhaustion and unknown keys:** D6 preserves exhausted counters and absent key forms instead of adding an unapproved minimum headroom or inventing keys. Confirm that local import success remains distinct from a later connected operation's ability to replenish/sync.

These are review points for the concrete plan, not permission to relax acceptance. No source account, server credentials or phone session is needed to implement Stage 5 as specified. Stage 6 still requires a separately arranged dedicated stopped source account, peer and phone observation.
