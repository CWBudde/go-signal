# Account-import Stage 4 verification

Date: 2026-10-25. Status: Stage 4 complete.

This records Phase 16 Stage 4: the shared registry and open protocol and recoverable
publication in `internal/store`, with the client moved onto it. It is an offline
prerequisite. It adds no converter, imports no account and establishes no phone or server
acceptance. Stage 5 (offline converter, app/CLI/output and user guide) is next. Design:
[importer spec](superpowers/specs/2026-10-10-account-import-design.md), "Publication and
recovery". Decisions: [plan](superpowers/plans/2026-10-25-account-import-publication.md),
including where the implementation differs from the reviewed draft. No fork change.

## What changed

- **Registry lock.** `<data-dir>/accounts.lock` (flock) is held around every registry read and
  mutation, account-directory creation for linking, staging allocation and publication. It is
  tried without blocking for up to 2 s (`ErrRegistryBusy`). Holders never wait for an account
  lock. `accounts.json` writes now also sync the data directory.
- **One select-open-lock step.** `Dir.OpenRegistered` chooses the entry, checks the account
  directory and database (`ErrAccountMissing` if gone, nothing created) and their generation,
  and tries the account lock, all under the registry lock.
  - Connected mode keeps the lock, then migrates.
  - Snapshot mode needs no lock. It migrates only if the lock is free; if another process
    holds the account, an outdated database is `ErrAccountInUse` and is left alone.
  - Passing a snapshot's generation makes a later connected open fail with
    `ErrAccountChanged` if the account was replaced meanwhile.
- **Client.** `device` opens snapshots; Connect and Unlink open connected with the snapshot's
  generation (`lockAccount`). Linking creates the directory under the registry lock. Unlink
  removes the entry and moves the directory to `.removed/` under the registry lock, then
  deletes it. Logout marking is a locked read-modify-write.
- **Staging.** `Dir.BeginImport` allocates `.imports/<token>/` with an owner lock held until
  `Close`. `Import.Build` creates the account with a builder callback, checkpoints and closes
  the database, reopens it without migrating to check the logged-in device, syncs files and
  directories and records the SHA-256 digest.
- **Publication.** `Import.Publish` holds the registry lock throughout:
  1. it recovers other attempts and refuses an entry with the ACI, number or PNI, or anything
     at the final path (`ErrImportCollision`);
  2. it writes the ownership marker, then the intent (entry, token, digest), each durably;
  3. it renames the account with `renameat2(RENAME_NOREPLACE)` and syncs both parents;
  4. it appends the entry, then removes marker, intent and stage.

  After the intent, a failure is `ErrRecoveryRequired`.

- **Recovery** runs before every registry use. It skips attempts whose owner lock is held,
  removes attempts without an intent, and rolls committed ones forward from the staged or
  renamed location after checking marker and digest. An already registered exact entry is
  only retired, without rehashing. Disagreeing evidence is `ErrRecoveryRequired`, leaving
  every file as it is.
- **Platform.** Publication is Linux-only (`publish_linux.go`); elsewhere `BeginImport`
  returns `ErrImportUnsupported` before touching anything.

## Evidence

| Spec evidence                      | Tests                                                                                                                                                                               |
| ---------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Concurrent registry updates        | `TestRegistryConcurrentUpdates`: four writer processes (40 entries and marks), an unlink and a publication in parallel; all survive                                                 |
| Lock order and contention          | `TestRegistryLockOrder`, `TestOpenRegisteredModes` (a held account fails within a second, registry lock released), `TestRegistryBusy`                                               |
| Stale selection vs unlink/reimport | `TestStaleSelection` (helper process unlinks or reimports), `TestConnectAfterReplacement` (client: Connect after unlink or relink; nothing recreated)                               |
| Noncreating, nonmigrating opens    | `TestOpenRegisteredMissing`, `TestSnapshotNoMigrate`                                                                                                                                |
| Orphan and collision refusal       | `TestPublishCollision`: entry, unlinked entry, orphan directory, symlink, same number, same PNI; `TestPublishImport` (republish refused)                                            |
| Crash at each durable boundary     | `TestPublicationCrash` (helper process exits after each of the eight steps) and `scripts/test-account-publication.sh` (crash with one backend, recover with the other, both orders) |
| Recovery refusals                  | `TestRecoveryEvidence`: staged and renamed digest, marker token, missing marker, both locations, conflicting entry; files unchanged after two attempts                              |
| Active attempts, invisibility      | `TestRecoverySkipsActiveStage`, `TestPublishInterrupted` (stopped after each step: not visible before registration, then rolled forward or removed)                                 |
| Rename and sync failures           | `TestPublishInterrupted` (`renamed`, `synced`: explicit `ErrRecoveryRequired`, then roll-forward)                                                                                   |

**Mutation checks.** These faults were each caught:

- accepting any generation (`TestStaleSelection/reimport` and
  `TestConnectAfterReplacement/after_relink` fail);
- retiring the intent right after the rename, before registering (`TestPublicationCrash`
  `renamed`, `synced` and `registered` fail, and so does the script).

## Verification

Final commands use `GOWORK=off`. Every one exited 0:

| Command (`GOWORK=off`)                                                                      | Evidence                                                                       |
| ------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------ |
| `just check`                                                                                | Formatting, lint, native race suite (including the new store and client tests) |
| `just check-purego`                                                                         | Pure-Go vet, lint and suite                                                    |
| `just test-fork`                                                                            | Fork suites, unchanged                                                         |
| `just test-diff`                                                                            | Includes `scripts/test-account-publication.sh` (16 crash/recovery runs)        |
| `CGO_ENABLED=0 go test -count=1 ./...`                                                      | Untagged no-cgo suite                                                          |
| `go test -race -v -run '^(TestPublish\|TestRecovery\|…)' ./internal/store/`, both backends  | All 13 new store tests pass; `TestConnectAfterReplacement` passes on both      |
| `scripts/test-prekey-allocators.sh`                                                         | 12 actions, exact ledger                                                       |
| `CGO_LDFLAGS="-L $PWD/third_party/lib" scripts/test-account-import-compatibility.sh --java` | All 264 actions, 48 Java legs                                                  |

The Java step used the retained private JDK 25.0.2 / Maven 3.9.11 toolchain and cache. The
compatibility ledger is byte-identical to the [Stage 1 ledger](account-import-compatibility-ledger.tsv)
(SHA256 `8d058a94556bbd97a48b5205c08c2af1f207fdf0590dc55523056065bd4c34bb`).

## Limitations

- **Older processes.** go-signal releases before this one don't take the registry lock: stop
  them before importing, as the spec requires. `accounts.json` stays at version 1.
- **Linux only.** Publication needs `renameat2(RENAME_NOREPLACE)`; other platforms refuse.
  On non-Unix systems the registry lock is best effort, like the account lock.
- **Manual recovery.** An `ErrRecoveryRequired` attempt blocks registry use until someone
  inspects it and moves it away; nothing deletes or adopts it automatically.
- **Fork window.** Between fork and exec, a child process briefly shares the parent's flock
  descriptors, so a released lock can look held for an instant. Recovery then skips that
  attempt until the next registry use; registry contention retries.
- **No converter.** Nothing calls `BeginImport` outside tests yet; Stage 5 adds the converter
  and `account import`.
- **Offline only.** No live account, phone or server was used.
