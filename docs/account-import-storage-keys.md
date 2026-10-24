# Account-import Stage 2b verification

Date: 2026-10-24. Status: Stage 2b complete.

This records Phase 16 Stage 2b, the storage-key lifecycle in the mautrix-signal fork. It is
an offline prerequisite: it adds no converter, imports no account and establishes no phone
or server acceptance. Stage 3 (privacy guards, typed blocking) is next. Design:
[importer spec](superpowers/specs/2026-10-10-account-import-design.md), "Storage keys and
privacy reconciliation"; decisions: [plan](superpowers/plans/2026-10-24-account-import-storage-keys.md).

## What changed

- Fork schema v29 (compatibility v29) adds `signalmeow_device.storage_service_key`,
  exposed as `DeviceData.DerivedStorageKey`, next to the unchanged `MasterKey` and the
  account entropy pool.
- `DeviceData.ResolveStorageServiceKey` covers signal-cli's three sources
  (`SignalAccount.getOrCreateStorageKey`: stored derived `storageKey`, `pinMasterKey`,
  `accountEntropyPool`).
  - It uses a derived key as is and derives from the other two.
  - It refuses invalid forms and forms that disagree, including a master key that isn't
    the pool's.
  - The result is a typed `store.StorageServiceKey`, which `FetchStorage` now requires.
- Storage sync, the connect-time key request (sent while the key doesn't resolve), the
  bridge connector and go-signal's `account sync` and blocked-list fetch use the resolver.
  Invalid or disagreeing forms keep `account sync` asking for keys.
- `SyncMessage.Keys` follows signal-cli:
  - An entropy pool supersedes the older forms and is now stored with its master key.
  - Otherwise the legacy derived `storageService` field, read from the unknown fields, replaces
    the derived form and clears forms that no longer lead to it.
  - Updates are persisted first. A failed write fails the handler, so the message is redelivered.
- `reference/signal-cli` is pinned at `2a9a4b1` (v0.14.9-1), the revision the Stage 1 corpus
  recorded. Its storage and key handling are unchanged from the previous pin.

## Java vectors

`scripts/generate-account-import-fixtures.sh --storage-keys <dir>` runs the existing
harness with signal-cli's own key models: `com.github.turasa:models-jvm:2.15.3_unofficial_154`
(SHA256 `bc5e59e5…b79dd`) and `kotlin-stdlib:2.2.21` (`6558a3d2…9dd`), both checked by
the script.

- **Inputs.** Invented and deterministic: a 64-character pool, a byte-ramp pin master key and a
  byte-ramp stored derived key.
- **Outputs.** Master key, storage key, manifest key (version 7) and legacy item key for each source.
- **Location.** Output is in `internal/signal/accountimport/testdata/java-storage-keys/`; `vectors.json`
  SHA256 is `48b817da3d799100f982de9fbee75d68b086a7ba937119fa8725c138544cf0c4`.
- **Determinism.** A second generation was byte-identical.
- **Consumers.**
  - The fork tests embed the same values.
  - `TestJavaStorageKeyDerivation` checks the committed file against provenance and the
    published resolver.

## Published version

| Module         | Version               | Commit                                     | Module checksum                                   |
| -------------- | --------------------- | ------------------------------------------ | ------------------------------------------------- |
| mautrix-signal | `v0.2609.0-purego.32` | `60cc9eedf5b0a5e62743f496bfde075eed20dba4` | `h1:Je6O24GvQ3bmD0D+UqAi35tb1qXCnAKPU47fKUda+YY=` |

Built on `v0.2609.0-purego.31` (`74fc1aa`) with one commit.

- Before tagging, go-signal's cgo `-race`, pure-Go and untagged no-cgo suites passed against
  the local fork through an uncommitted scratch `go.work`.
- The remote branch and the tag point at the reviewed commit. The fetched module's `pkg`
  tree matches the checkout, apart from the nested libsignal submodule, which isn't part of
  the module. `go mod verify` passes.
- libsignal-go stays `v0.7.1-cw.6` and native libsignal stays `v0.102.2`. Go 1.26.0, Linux amd64.

## Verification

Fork checks before publication:

- Fork-side vet passed with both full suites: `./pkg/...` pure-Go and
  `./pkg/signalmeow/... ./pkg/libsignalgo/...` native.
- Three `-race` runs per backend of the new storage-key and allocator tests passed.
- Two mutation checks confirmed the tests catch these faults:
  - re-deriving a stored derived key (vectors, refusals, sync and restart tests fail);
  - ignoring a failed device write (the redelivery test fails).

Final commands use `GOWORK=off` against the published pin; every one exited 0:

| Command (`GOWORK=off`)                                                                      | Evidence                                                                     |
| ------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------- |
| `just check`                                                                                | Formatting, lint, native race suite, libsignal pin and tidy checks           |
| `just check-purego`                                                                         | Pure-Go vet, lint and suite                                                  |
| `just test-fork`                                                                            | Includes `TestStorageKey*` and `TestStorageServiceKey*` from the pinned fork |
| `just test-diff`                                                                            | Differential tests and the prekey allocator backend runner                   |
| `CGO_ENABLED=0 go test -count=1 ./...`                                                      | Untagged no-cgo suite                                                        |
| `scripts/generate-account-import-fixtures.sh --storage-keys`                                | Regenerated `vectors.json` byte-identical                                    |
| `scripts/test-prekey-allocators.sh`                                                         | 12 actions, exact ledger                                                     |
| `CGO_LDFLAGS="-L $PWD/third_party/lib" scripts/test-account-import-compatibility.sh --java` | All 264 actions, 48 Java legs                                                |

The Java run used the retained private JDK 25.0.2 / Maven 3.9.11 toolchain and cache. Its
ledger is byte-identical to the [Stage 1 ledger](account-import-compatibility-ledger.tsv)
(SHA256 `8d058a94556bbd97a48b5205c08c2af1f207fdf0590dc55523056065bd4c34bb`).

## Limitations

- Upgrading is one-way (compatibility v29). Going back means starting the account over
  ([dev.md](dev.md#prekey-allocators-and-downgrades)).
- A legacy derived key that disagrees with a stored pool clears the pool, as signal-cli
  would stop using it for storage. The pool isn't used for anything else yet.
- Media root backup and profile keys from keys sync are not handled (separate checks per
  the spec).
- No live account, phone or server was used; current primaries send only the pool, so the
  legacy derived path is exercised offline only.
