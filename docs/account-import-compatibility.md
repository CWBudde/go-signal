# Account-import Stage 1 verification

Date: 2026-10-11. Status: Stage 1 complete.

This records the synthetic offline prerequisite for Phase 16. It does not establish
production conversion or phone/server acceptance. Source candidate: signal-cli v0.14.9,
registry 2, account JSON 11, SQLite 31; Java libsignal-client 0.103.0. Native target remains
libsignal v0.102.2. Corpus SHA256:
`e43dd0654a07aecf8e71faf5b997cb193968b69eba278f236b07e3cad50637eb`.

## Provenance and generation

JDK 25.0.2 and Maven 3.9.11 generated two independent corpora. Source SQLite31
integrity, foreign keys, eighteen STRICT tables, exact protocol rows and the genuine
incoming sender-key sixteen-byte UUID BLOB were checked. Each generation contains
12 scenarios, 24 account snapshots and 16 independently checked session inspections.
Each of its 12 queued messages was decrypted after restoring original Java records.
All 275 artifact, seven generator and 28 source-file checksums were verified.

Loaded bundled Linux amd64 JNI SHA256:
`7c4c1c68bc0d9441c03fa7a4b7566d7c0e1a194d093c857d4fbc93504146f582`.
The official loader selects its testing JNI; the whole artifact remains unmodified.
The harness checks ordinary Kyber deletion, last-resort retention, failure preservation,
reopening, typed namespace separation, eleven refusal cases and two concurrent invocations.

## Exchange matrix

The final matrix passed: 12 ACI/PNI scenarios, both backend starting orders, 264 completed
actions and 48 genuine Java continuation legs. Direct legs exchange replies and initiate
a fresh prekey session toward Java's retained receiver keys. Group legs process a fresh
Go distribution before decrypting Go group messages and returning Java messages.
The same SQLite files reopen at every backend step. Separate databases isolate Go-cloned
peers from the original evolving Java peer; its immutable seed hash and increasing step
are checked. Skipped, tampered, replayed and consumed key state persists across reopen.

Inspection-only encodings include session version3, V0/negotiation-minimum-V0/downgraded
PQ shapes, expired pending usability and historical archived identity differences.
These have no Java exchange claim. See account-import.md for the precise boundary.

## Verification

Published immutable versions and verified remote commits:

| Module         | Version               | Commit                                     | Module checksum                                   |
| -------------- | --------------------- | ------------------------------------------ | ------------------------------------------------- |
| libsignal-go   | `v0.7.1-cw.6`         | `5d04ba2d2b611a9aedcbe2be944f01c07a2e2cf6` | `h1:JktzKBABc+1vYg4vmMN7eclA8jJcS5HIi8uyInfXH5s=` |
| mautrix-signal | `v0.2609.0-purego.30` | `a3a2de4f8371aaf9b02b979d98dd205c346d36ae` | `h1:enUp71hEMG9HktKvvUAdFIpdeCyfvUOt/PzpZPEWBCo=` |

Both tags and branch tips were verified after atomic publication. Fetched modified
source files match the reviewed checkouts, `go mod verify` passes and the direct and
shim-required libsignal-go versions agree. Go-signal uses Go 1.26.0 on Linux amd64.
Native libsignal and both submodule checkouts remain unchanged by this work.

All final commands use `GOWORK=off` and a private repository-backed `TMPDIR`.
Every required command exited 0:

| Command (`GOWORK=off`)                                                                      | Evidence                                                                          |
| ------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------- |
| `just fmt`                                                                                  | No formatter changes after final formatting                                       |
| `just check`                                                                                | Native race suite, zero lint issues, native pin and tidy checks                   |
| `just check-purego`                                                                         | Vet, zero lint issues, full pure-Go suite and AES assembly on six release targets |
| `just test-fork`                                                                            | Validator, shim and local protocol/lifecycle suites                               |
| `just test-diff`                                                                            | Native differential/race suites and all 96 persisted corpus actions               |
| `CGO_ENABLED=0 go test -count=1 ./...`                                                      | Untagged no-cgo suite                                                             |
| `CGO_LDFLAGS="-L $PWD/third_party/lib" scripts/test-account-import-compatibility.sh --java` | All 264 actions, 48 Java legs                                                     |

The [completed ledger](account-import-compatibility-ledger.tsv) contains 108 native,
108 pure-Go and 48 Java actions. All 24 scenario/starting-order branches have the exact
required action sequence; each Java leg returns through the other Go backend.
Ledger SHA256: `8d058a94556bbd97a48b5205c08c2af1f207fdf0590dc55523056065bd4c34bb`.

Both full shim suites passed against the released validator before shim publication;
native used race detection. Shared native/pure-Go API parity remains 524 declarations.

The first pure-Go check attempt stopped at the linter's singleton lock; its complete retry passed
after the standard linter finished. An earlier Java matrix stopped because a concurrent
build removed active classes; wrapper locking now covers the whole Java lifetime and
its two-invocation regression passes. The two complete 264-action matrices passed, first against local reviewed forks and
then against released modules; partial output is not counted. A baseline linking attempt exhausted tmpfs, so build scratch uses the
repository disk. None of these orchestration failures weakens the acceptance gate.

## Review and implementation decisions

Independent whole-change and final delta reviews found no remaining issues after
raw-wire overflow/duplicate-PQ and typed namespace regression fixes. No deferred minors.

- Skill scripts lack execute bits; run identical copies with execute bits in owned /tmp scratch — keeps installed skills unchanged; cost if wrong: helper setup only.
- Task 1/2 commits belong to separate fork repositories; record their own BASE/HEAD with verification in this shared ledger — parent-only script ranges cannot represent fork work; cost if wrong: review range bookkeeping.
- Inspect every original wire occurrence before protobuf decoding, including superseded oneof branches — decoded trees can hide unsupported fields; cost if wrong: rejects source encodings pending explicit support.
- Use fresh independent DB branches for Java and Go-cloned-peer exchanges — their random ratchets diverge, original Java state must stay original; cost if wrong: extra private account initialization.
- Exclude the generated fixture directory from treefmt — formatter changes break emitted-file provenance hashes; cost if wrong: generated formatting is controlled by the harness.
- Test helpers split into exchange/namespace files to keep restoration and state-machine contracts reviewable under full lint — no production importer behavior; cost if wrong: extra test files.
- Keep go mod tidy reclassification of existing sqlite3 v1.14.52 as direct in the shim — its existing sync_ack_test imports that driver directly; no dependency version changed — cost if wrong: tidy would restore classification.
- Merge the remote libsignal-go main documentation-only advance before release — preserves the existing CT-02 documentation and allows a normal fast-forward publication; exact tree diff reviewed — cost if wrong: an unnoticed runtime change would need fresh integrated evidence.
- Record the official loader's actual bundled testing JNI and preserve the complete publisher JAR — matches Java0.103.0 default source behavior and verified mapped bytes — cost if wrong: JNI/platform compatibility could be overstated, so evidence remains explicit Linux amd64.
