# Account Import Compatibility Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Establish genuine Java libsignal 0.103.0 record compatibility through strict inspection and continued offline exchanges on both go-signal backends.

**Architecture:** Add a nonmutating PQ-state validator to libsignal-go and a shared session-record inspector to the mautrix-signal shim. A standalone Java harness generates synthetic source-format fixtures; test-only restoration into real target SQLite stores exercises bidirectional exchanges, reopening and backend switching. Release verified fork changes and pin immutable versions before recording Stage 1 complete.

**Tech Stack:** Go, protobuf, both SQLite drivers, native libsignal v0.102.2, libsignal-go, Java 25, Maven and Signal's libsignal-client 0.103.0 artifact.

**Spec:** [Approved account import design](../specs/2026-10-10-account-import-design.md), Stage 1; [source format mapping](../../account-import.md).

## Global Constraints

- Source candidate: signal-cli v0.14.9; registry 2, account JSON 11, SQLite schema 31; registered LIVE linked devices (`deviceId > 1`). Compatibility remains conditional on this stage's evidence.
- Keep native libsignal at v0.102.2 and the matching submodule revision. Do not edit `reference/signal-cli` or either submodule checkout.
- Use only synthetic accounts, invented credentials and offline local protocol operations. No real account directories or Signal server calls.
- Bound each protocol blob to 1 MiB. Inspect at most 40 archived sessions, 5 receiver chains per state and 2,000 skipped keys per receiver chain; protobuf recursion limit 64.
- Preserve complete record bytes, archived states, skipped keys, typed ACI/PNI scope and field-specific timestamps: signed/Kyber milliseconds, pending-session creation seconds.
- Ordinary Kyber keys are deleted upon successful source use; last-resort keys are retained. Source replay history is unavailable; never invent it.
- Java is required only for fixture generation and interoperability checks, never release builds or the eventual import command.
- No production converter, command, allocator, storage-key lifecycle, privacy guard or publication protocol belongs in this stage. Stages 2a–6 remain separate reviewed plans.
- Work directly on main in go-signal, preserving unrelated changes. Implement these coupled tasks sequentially with useful read-only reconnaissance and independent review; the controller owns planning records and history.
- Develop fork changes in writable isolated checkouts. An ignored, temporary Go workspace may connect them for verification; committed modules use immutable fork versions, with no `replace` or committed `go.work`.

## Review Focus

1. Unknown nested/archive/PQ encoding must fail explicitly instead of being dropped or interpreted as disabled PQ; Tasks 1–2 pin this behavior.
2. A session with only archives, expired pending state or a historical remote identity must remain inspectable without inventing a usable current sender chain; Tasks 2 and 4 cover it.
3. Optional ID zero, signed pending IDs and seconds/milliseconds must retain their distinct meanings; Tasks 2–4 assert exact values.
4. Authentic but tampered, replayed or out-of-order ciphertext must preserve the appropriate skipped-key, failure and consumption state across reopening; Tasks 3–4 exercise it.
5. Wrong Java/JNI artifacts, missing toolchains or a skipped continuation leg must fail the explicit acceptance runner, rather than turn partial evidence into compatibility; Tasks 3–5 enforce it.

---

## File and verification boundaries

Paths in Tasks 1 and 2 are relative to the named fork checkout. Remaining paths are relative to go-signal. Tests use external `_test` packages. New Go compatibility files use `//go:build cgo || libsignal_go`; the untagged no-cgo build receives no protocol dependency through them.

For native commands, set `CGO_LDFLAGS="-L /home/christian/Code/go-signal/third_party/lib"`. Run local fork tests through an ignored workspace containing go-signal and the two writable forks, then rerun release acceptance with `GOWORK=off`. Do not treat successful decode or equality after reserialization as exchange evidence.

### Task 1: Strict supported PQ-state validation

**Files (libsignal-go fork):**

- Create: `spqr/state_validate.go` — bounded, nonmutating validation.
- Create: `spqr/state_validate_test.go` — external-package contract tests.

**Interfaces:**

- Consumes: `spqr.SerializedState` (alias of `[]byte`), existing state/chain/variant decoders and `spqr.ErrInvalidState`.
- Produces: `func ValidateState(raw SerializedState) error`; new `ErrUnsupportedState` and `ErrStateLimit` sentinels. All failures also match `ErrInvalidState`; unsupported encodings and exceeded bounds additionally match their specific sentinel.

- [ ] **Step 1: Write `TestValidateStateSupportedForms`, `TestValidateStateUnknownEncoding`, `TestValidateStateShape`, `TestValidateStateLimits` and `TestValidateStateNonMutation`.** Assert acceptance of empty V0, initial V1 negotiation with minimum V0/no established chain, established V1 with a chain, and negotiated-down V0 retaining a chain. Unknown root/nested fields and enum values match `ErrUnsupportedState`; malformed/truncated or missing required variant/authenticator/chain structure matches `ErrInvalidState`; 1 MiB + 1 matches `ErrStateLimit`. Recursion beyond 64 must fail without a panic; a decoder recursion failure may match `ErrInvalidState` without a limit subclass. Input hashes remain equal on success and failure; errors contain no serialized keys or bytes. Core assertions include:

```go
if err := spqr.ValidateState(nil); err != nil { t.Fatal(err) }
if err := spqr.ValidateState([]byte{0xf8, 0x07, 0x01}); !errors.Is(err, spqr.ErrUnsupportedState) { t.Fatal(err) }
if err := spqr.ValidateState(make([]byte, (1<<20)+1)); !errors.Is(err, spqr.ErrStateLimit) { t.Fatal(err) }
```

- [ ] **Step 2: Run `CGO_ENABLED=0 go test -count=1 -run '^TestValidateState' ./spqr`.** Expect compile failure for the missing API before implementation.
- [ ] **Step 3: Implement `ValidateState` in `spqr/state_validate.go`.** Retain unknown fields during bounded protobuf decoding; recursively inspect known messages/enums before invoking existing private V1 and chain decoders. Check the required shape and key lengths for each recognized variant against the reviewed serialization contract; accepting a nil authenticator through the current decoder is insufficient. Nonempty bytes with no V1 inner can be valid negotiated-down V0, while an unknown future inner cannot. Do not advance state, generate randomness, normalize encapsulation bytes or alter `DecodeState`, `Send` or `Recv` behavior. Return only category/context errors.
- [ ] **Step 4: Run the scoped command, `CGO_ENABLED=0 go test -count=1 ./...` and `CGO_ENABLED=1 go test -race -count=1 ./spqr`.** Require all tests to pass; ensure existing SPQR vectors still pass.
- [ ] **Step 5: Commit only this fork's validator and tests:** `feat(spqr): validate supported serialized state`. Record the reviewed commit; publication waits for Task 5's integrated evidence.

### Task 2: Backend-neutral session inspection

**Files (mautrix-signal fork):**

- Create: `pkg/libsignalgo/sessionrecord_inspect.go` — common inspector and error sentinels.
- Create: `pkg/libsignalgo/sessionrecord_inspect_test.go` — external-package tests for both builds.

**Interfaces:**

- Consumes: Task 1's `spqr.ValidateState`, its error classifications, and the shared generated storage/PQ protobufs.
- Produces the following API; all returned byte slices and optional scalar pointers are independent copies:

```go
func InspectSessionRecord(raw []byte) (SessionRecordInspection, error)

type SessionRecordInspection struct {
    Current  *SessionStateInspection
    Archived []SessionStateInspection // Source order, newest first.
}
type SessionStateInspection struct {
    Version, LocalRegistrationID, RemoteRegistrationID uint32
    LocalIdentityPublic, RemoteIdentityPublic []byte
    SenderChainPresent bool
    ReceiverChainCount, SkippedMessageKeyCount int
    PendingPreKey *PendingPreKeyInspection
    PendingKyberID *uint32
    PQRatchet PQRatchetInspection
}
type PendingPreKeyInspection struct {
    PreKeyID *uint32
    SignedPreKeyID int32
    TimestampSeconds uint64
}
type PQRatchetInspection struct {
    Present bool
    Version uint32
    Negotiating bool
    MinVersion uint32
}
```

`ErrMalformedSessionRecord`, `ErrUnsupportedSessionRecord` and `ErrSessionRecordLimit` are distinct `errors.Is`-matchable sentinels. Failure returns zero inspection. `Current == nil` means structural absence, independent of time and `HasCurrentState`'s usable-sender-chain test. Empty fresh records are valid; present invalid states are errors.

- [ ] **Step 1: Write `TestInspectSessionRecordCurrentAndArchived`, `TestInspectSessionRecordArchivedOnly`, `TestInspectSessionRecordPendingMetadata`, `TestInspectSessionRecordUnknownEncoding`, `TestInspectSessionRecordMalformedArchive`, `TestInspectSessionRecordLimits`, `TestInspectSessionRecordPQRatchet` and `TestInspectSessionRecordNonMutation`.** Assert exact identities/registration IDs/archive order, historical remote-key differences, empty and archive-only records, optional nil versus ID 0, signed pending IDs and seconds. Include an old pending timestamp that makes sender usability expire without removing structural current-state metadata. Reject malformed archived bytes even when root decoding succeeds; classify unknown fields at every nesting level and unknown versions/PQ enums as unsupported. Assert limit boundaries and one-over cases, input hash equality and result-copy isolation; distinguish all four PQ forms from Task 1. Core assertions include:

```go
got, err := libsignalgo.InspectSessionRecord(nil)
if err != nil || got.Current != nil || len(got.Archived) != 0 { t.Fatal("invalid empty-record inspection", err) }
_, err = libsignalgo.InspectSessionRecord([]byte{0xf8, 0x07, 0x01})
if !errors.Is(err, libsignalgo.ErrUnsupportedSessionRecord) { t.Fatal(err) }
```

- [ ] **Step 2: Run `CGO_ENABLED=0 go test -tags libsignal_go -count=1 -run '^TestInspectSessionRecord' ./pkg/libsignalgo`.** Expect compile failure for the missing API.
- [ ] **Step 3: Implement the shared inspector.** Decode `RecordStructure` while retaining unknown fields; independently decode every archived `SessionStructure` and recursively check its chains/pending metadata. Recognize encoded session versions 3 and 4, rejecting version 0 and future versions rather than mapping them. Validate encoded identity public keys and required state metadata, without comparing archived remote keys to current trust-store entries. Validate embedded PQ bytes with Task 1 before reporting PQ metadata. Enforce the global bounds and preserve source bytes; errors identify field category/archive index without key material. Existing FFI, record serialization, stores and `HasCurrentState` remain unchanged. Structural support for version 3 does not establish Java 0.103.0 exchange coverage for that version.
- [ ] **Step 4: Run the scoped test on pure-Go and `CGO_ENABLED=1` native builds; run both full shim suites, native with `-race`.** Require matching inspection output and passing existing cross-backend tests. No Rust/submodule changes are needed.
- [ ] **Step 5: Commit only the two inspector files:** `feat(libsignalgo): inspect current and archived session metadata`. Keep the local workspace for the continuation work; release pins are Task 5.

### Task 3: Genuine Java source-format corpus

**Files (go-signal):**

- Create: `scripts/account-import-java/pom.xml` — exact generator dependencies/toolchain.
- Create: `scripts/account-import-java/src/main/java/org/gosignal/accountimport/ImportFixtures.java` — generation, self-test and continuation entry point.
- Create: `scripts/account-import-java/src/main/java/org/gosignal/accountimport/SourceProtocolStore.java` — source-oriented ordinary/last-resort Kyber behavior and fixture persistence.
- Create: `scripts/generate-account-import-fixtures.sh` — private toolchain/cache invocation and provenance checks.
- Create: `internal/signal/accountimport/testdata/java-0.103.0/{README.md,provenance.json,corpus.json,accounts.json,account.json,schema.sql,rows.sql}` — bounded encoded records and synthetic source layout.
- Modify: `.gitignore` — Java build output and generated SQLite databases only.

**Interfaces:**

- Consumes: approved source mapping and public Java protocol APIs from the exact 0.103.0 artifact; no signal-cli loader.
- Produces: `ImportFixtures.main(String[] args)` actions `self-test`, `generate <new-output-dir>`, and `continue <exchange-dir>`. Shell generator takes one new output directory. Existing paths are refused; promotion of regenerated fixtures is an explicit reviewed diff.
- `corpus.json` has format version 1; `accounts`, `scenarios` and `expectedMetadata`. Each account carries typed local service IDs, device/registration IDs, identity components and source-shaped protocol rows. Binary values use standard padded base64. Scenarios have stable IDs, explicit local/remote typed IDs, original Java peer-state bytes, ordered message type/ciphertext/expected-plaintext steps and expected key-consumption outcomes. Metadata uses the inspector fields from Task 2; timestamps include their unit in the field name.
- Exchange directories contain `request.json` and atomic `response.json`, each format version 1. Request fields: `scenarioID`, `step`, `senderServiceID`, `receiverServiceID`, `messageType`, `ciphertext`, `expectedPlaintext`. Response fields: the same scenario/step and typed sender/receiver IDs, `messageType`, `ciphertext`, `expectedPlaintext`, and `peerStateSHA256`. Service IDs are objects with `type` (`ACI` or `PNI`) and `uuid`. Message types are `prekey`, `signal`, `sender-key-distribution` and `sender-key`; responses additionally allow `ack` for processed distribution messages, with empty ciphertext/plaintext. Java keeps and reopens its evolving private peer state in that directory, seeded once from the original corpus; the request cannot replace it with Go-generated state.

- [ ] **Step 1: Write harness `self-test` assertions and shell negative cases.** Ordinary Kyber successful use removes the key, last-resort use retains it, identities are distinct across ACI/PNI, and failed generation leaves no promoted corpus. Require a failure for missing Java/Maven, wrong artifact/JNI hashes, unsupported source markers and an existing output directory. Check synthetic credentials and fixed format markers without emitting keys in logs. After an actual successful decrypt, the source-store assertions are:

```java
assert !ordinaryStore.containsKyberPreKey(ordinaryID);
assert lastResortStore.containsKyberPreKey(lastResortID);
```

- [ ] **Step 2: Establish the generator toolchain in private scratch storage and run the assertions against an empty harness.** Require Java/Javac 25 and Maven; record their exact versions. A missing toolchain is unfinished work, not a skipped acceptance test. Do not install system-wide tooling or use the older available libsignal jar as a fallback.
- [ ] **Step 3: Implement the pinned harness and wrapper.** Use `org.signal:libsignal-client:0.103.0` from [Signal's Maven repository](https://build-artifacts.signal.org/libraries/maven/org/signal/libsignal-client/0.103.0/libsignal-client-0.103.0.pom), Java release 25, `maven-compiler-plugin:3.14.0`, `maven-dependency-plugin:3.8.1` and `jackson-databind:2.20.0`. Resolve in a private Maven cache; record every resolved coordinate and SHA-256 plus the matching embedded JNI library/platform and its checksum in provenance. Use that artifact's bundled JNI, refusing external-library overrides. Record generator source/POM/wrapper hashes alongside the parent revision and reviewed source-format reference revisions, so uncommitted generator bytes are identifiable. Enable Java assertions (`-ea`) for every harness action. Java's own RNG means regeneration checks invariants, not byte-identical random keys.
- [ ] **Step 4: Generate all required synthetic scenarios.** Cover local ACI and PNI stores; EC/signed components; serialized ordinary and last-resort Kyber; current, archived-only and archived-plus-current sessions with skipped direct messages; queued prekey messages; incoming sender-key records, distribution messages and skipped group messages. Include actual Java PQ negotiation/established states reached by exchanges, optional prekey ID 0 and exact timestamp metadata where the Java API can produce them. Make permanent pending-session cases independent of fixture age: queue an authenticated Java reply that acknowledges the session before attempting new Go sender encryption; archive scenarios use acknowledged sessions. Preserve exact stored timestamps. Inspector tests separately classify deliberate expiry, and archive-only reception does not imply an outbound current session exists. Hand-built invalid/boundary protobufs stay in inspector tests and must never be labeled Java-generated. Use stable synthetic E.164/account/device fields, valid profile-key bytes and source JSON 11/registry 2. Emit SQLite 31 schema and insert scripts from reviewed source serialization rules, including correct typed address and UUID BLOB encodings; generate no committed database files. Do not invoke a mutating Java account loader or claim storage-service protobuf/derivation coverage, which belongs to later stages.
- [ ] **Step 5: Run `scripts/generate-account-import-fixtures.sh <private-new-dir>` and harness `self-test`.** Require every scenario, artifact/native checksum and expected metadata entry. Materialize `schema.sql`/`rows.sql` in temporary SQLite and verify integrity, schema 31 and expected row types/counts. Independently review records against the mapping, compare scenario invariants after a second generation, and promote one complete corpus. If Java 0.103.0 emits unsupported encoding, retain the diagnostic and leave Stage 1 open rather than deleting that scenario.
- [ ] **Step 6: Commit the harness, wrapper, corpus and ignore entries:** `test(import): add genuine Java 0.103.0 protocol corpus`. Include provenance and synthetic-data notice; do not commit caches, temporary toolchains or databases.

### Task 4: Real SQLite restoration and continued exchanges

**Files (go-signal):**

- Create: `internal/signal/accountimport/doc.go` — package boundary only; no importer API.
- Create: `internal/signal/accountimport/fixtures_test.go` — external-package corpus parsing and test-only restoration helpers.
- Create: `internal/signal/accountimport/compatibility_test.go` — inspection, persistence and exchange tests.
- Create: `scripts/test-account-import-compatibility.sh` — separate native/pure-Go processes and optional mandatory-Java acceptance mode.
- Modify: `justfile:test-diff` — require the new Go-only persisted corpus runner alongside existing differential tests.

**Interfaces:**

- Consumes: Tasks 1–3, `store.OpenDir`/`OpenAccount`, real fork device/protocol stores and ordinary libsignalgo encryption/decryption APIs. Follow `internal/store/backend_switch_test.go` for account-store lifetime and `scripts/test-backend-switch.sh` for separate test binaries.
- Test-only helpers: `loadJavaCorpus(t *testing.T) javaCorpus`, `restoreJavaAccount(t *testing.T, dir string, accountID string)`, and `runJavaCompatibilityStep(t *testing.T, dir string, action string)`. `javaCorpus` mirrors Task 3's format, rejecting unknown fields, duplicate IDs, bad base64 and exceeded record bounds.
- `TestJavaAccountImportBackendStep` consumes `GOSIGNAL_IMPORT_COMPAT_DIR`, `GOSIGNAL_IMPORT_COMPAT_ACTION` (`init`, `advance`, `export-java`, `consume-java`) and `GOSIGNAL_IMPORT_COMPAT_SCENARIO`. Missing variables skip only this subprocess entry point, never ordinary tests or the runner.
- Runner accepts no option (committed-corpus Go-only checks) or `--java` (also execute real Java continuation). `--java` must verify Task 3 provenance, execute every declared Java leg and fail on missing tools or evidence. Neither mode contacts Signal.

- [ ] **Step 1: Write `TestJavaAccountImportSessionInspection`, `TestJavaAccountImportProtocolContinuation`, `TestJavaAccountImportConsumption`, `TestJavaAccountImportFailureState` and the subprocess step test.** Assert exact current/archive identities, optional IDs, seconds/milliseconds and input hashes after SQLite reopen. Decrypt expected queued/out-of-order direct and group messages, generate replies/new messages, and require exact plaintext at the other endpoint. Successful prekey use consumes EC/ordinary Kyber and retains signed/last-resort keys; replay and tamper failures leave protocol records unchanged. Test ACI/PNI scope independently, including equal UUID values in different namespaces. Historical archived remote keys need not match current identity-store keys; no current sender chain is required to inspect an archive-only source. In each current/archive table case, assertions include:

```go
if !reflect.DeepEqual(expectedMetadata, gotMetadata) { t.Fatal("metadata differs") }
if sourceRecordSHA256 != reopenedRecordSHA256 { t.Fatal("stored record changed") }
if !bytes.Equal(expectedPlaintext, decryptedPlaintext) { t.Fatal("plaintext differs") }
```

- [ ] **Step 2: Run `CGO_ENABLED=0 go test -tags libsignal_go -count=1 -run '^TestJavaAccountImport' ./internal/signal/accountimport`.** Expect failures for missing restoration/continuation helpers, then implement the helpers without a production converter.
- [ ] **Step 3: Implement restoration and ordinary exchanges.** Validate session bytes with Task 2 before insertion; reconstruct EC/signed records from original components, preserving signatures/IDs/millisecond timestamps. Check public/private consistency, identity signatures and Kyber ID/timestamp agreement with source rows. Load complete Kyber/session/sender-key bytes through the normal target stores in a private test account. Reopen and verify local identity/registration scope before exchange. Preserve incoming group records; the first outbound group exchange creates a fresh key and distribution ID through `NewSenderKeyDistributionMessage`, which Java processes before decrypting Go group messages. Do not seed an outbound sharing authorization, call Connect, publish a registry entry or silently rewrite source records. Persist exchange/replay fixtures separately from ordinary protocol tables and close all DB handles between subprocess steps.
- [ ] **Step 4: Implement the runner.** Compile the native test binary with `CGO_ENABLED=1` and `-race`, and the pure-Go binary with `CGO_ENABLED=0 -tags libsignal_go`. For each scenario, run both native→pure-Go→native→pure-Go and pure-Go→native→pure-Go→native sequences against the same database files, reopening at every step. In `--java` mode seed Java's peer once, export a Go reply for Java to decrypt using that retained peer, have Java emit the next reply/new direct or group message, then consume it in the other Go backend. Repeat in both starting orders. Include both Java-created imported receiver state and a fresh prekey initiation toward Java's retained receiver keys. Require an explicit ledger of completed scenario/namespace/direction/backend legs; omission is failure.
- [ ] **Step 5: Run both scoped suites, native with `-race`; run `scripts/test-account-import-compatibility.sh` and `scripts/test-account-import-compatibility.sh --java`.** Require all scenario steps and their durable consumption/replay/tamper assertions. Existing inbound vectors alone cannot satisfy `--java`. Add the no-option runner to `just test-diff`, keeping Java opt-in so ordinary builds/checks remain Java-free.
- [ ] **Step 6: Commit only Task 4 paths:** `test(import): continue Java sessions across both Go backends`. Record the actual runner ledger and any unsupported combinations; no skipped scenario can close Stage 1.

### Task 5: Release verified forks and record the compatibility boundary

**Files:**

- Modify (mautrix-signal fork): `go.mod`, `go.sum` — pin the released libsignal-go validator.
- Modify (go-signal): `go.mod`, `go.sum` — pin both immutable reviewed fork releases.
- Modify (go-signal): `docs/account-import.md`, `docs/dev.md`, `docs/maintenance.md` — scope, generator/runner instructions and fork API maintenance.
- Modify (go-signal): `PLAN.md`, `docs/superpowers/specs/2026-10-10-account-import-design.md`, this plan — verified Stage 1 status only.

**Interfaces:**

- Consumes: exact reviewed fork commits, checksum-verified Java corpus, Task 4 ledger and immutable version pins.
- Produces: released validator/inspection APIs and a documented, tested record/state compatibility matrix. Format markers alone remain insufficient to admit arbitrary accounts.

- [ ] **Step 1: Review the integrated changes and acceptance evidence before publishing.** Run the controller's review plus a useful independent whole-change review. Confirm the inspector has no mutating call paths, fixtures are synthetic/genuine, Java peer state was not substituted, every required exchange leg ran and native pin/submodules are unchanged. Resolve findings and rerun affected tests.
- [ ] **Step 2: Publish the libsignal-go commit under the next unused patch/fork tag permitted by that repository's release policy.** Verify the immutable tag resolves to the reviewed commit; record the resolved version. Pin it in the mautrix-signal fork, then run both full shim suites (native with `-race`) against released modules. Publish the reviewed shim under its next unused purego release tag and verify its commit. Tag selection must inspect existing remote tags; never overwrite a tag or guess that a version is free.
- [ ] **Step 3: Pin both released modules in go-signal, tidy and rerun with `GOWORK=off`.** Remove the test workspace from the acceptance environment. Check fetched module/source checksums and direct/transitive libsignal-go version agreement. No `replace`, local workspace or unreviewed upstream bump may remain in the committed graph.
- [ ] **Step 4: Run final acceptance:**

```sh
GOWORK=off just fmt
GOWORK=off just check
GOWORK=off just check-purego
GOWORK=off just test-fork
GOWORK=off just test-diff
GOWORK=off CGO_ENABLED=0 go test -count=1 ./...
GOWORK=off CGO_LDFLAGS="-L $PWD/third_party/lib" scripts/test-account-import-compatibility.sh --java
```

All commands must exit 0. Keep the completed Java/backend ledger with the verification record and report exact tested combinations. A compatibility failure requires a separately reviewed fix or an explicit unresolved Stage 1 entry, never weaker acceptance.

- [ ] **Step 5: Document only verified results.** In `docs/account-import.md`, distinguish inspected encodings from exchange-tested states, list source/target artifact versions and the synthetic offline nature of the evidence. Add generator and both runner modes to `docs/dev.md`; document the two fork APIs/provenance in `docs/maintenance.md`. Record Stage 1 as a completed nested prerequisite under Phase 16 only after all evidence passes. Leave the parent importer implementation, fixture/failure/live acceptance and Phase 16 completion criteria unchecked. Identify Stages 2a/2b as the next prerequisites; no live phone/server support is established.
- [ ] **Step 6: Run affected documentation checks and commit the pins/documentation/planning records:** `feat(import): establish Java protocol compatibility prerequisites`. Ship directly on main, verify remote synchronization and preserve unrelated changes. Mark this plan's completed steps as they land; unfinished or failed steps stay open.

## Current handoff

The user approved the architectural design on 2026-10-10. This Stage 1 plan is awaiting written-plan review; no validator, inspector, harness, corpus, converter or command has been implemented by this planning batch. Existing native/Pure-Go interoperability tests are useful baselines, not Java 0.103.0 acceptance. Implementation preserves the user's direct-on-main preference and uses scoped agents where useful; no execution-method selection is needed again.
