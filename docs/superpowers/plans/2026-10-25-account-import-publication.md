# Registry Protocol and Publication Implementation Plan

Status: **completed** (2026-10-25); see the [verification record](../../account-import-publication.md).
The user reviewed the draft on 2026-10-25 and accepted the proposed answers to all four open
questions: store-only writes keep working through a noncreating, nonmigrating snapshot; link
reuses an unregistered directory of its ACI; `accounts.json` stays at version 1; the crash
hook is test-only. No fork change was needed.

## As implemented (deviations from the draft)

- **Creating helpers stay for link and tests (D4).** `Dir.Lock` and `OpenAccount` still create
  the account directory, but only `LinkStore` (under the registry lock) and tests call them.
  Registered accounts are opened only through `OpenRegistered`. Its connected mode creates a
  missing lock file inside an existing account directory, since accounts seeded without ever
  connecting have none; it never creates a directory or database.
- **Snapshots migrate only under the account lock (D3).** If the account lock is free, a
  snapshot takes it for the migration and then releases it. If it is held, the database must
  already be at this build's versions (a newer compatible one is accepted), otherwise
  `ErrAccountInUse`.
- **Stale selection fails (D3).** A Connect or Unlink after the account was replaced is
  `ErrAccountChanged`; after an unlink with nothing left to select it is `ErrNotLinked`. A new
  command selects afresh.
- **`MarkUnlinked` (D6)** is a registry-locked read-modify-write keyed by ACI, without a
  generation check: its caller holds the account lock, so nothing else can unlink or relink the
  account meanwhile.
- **Unlinking missing data (D6).** `unlink --local-only`, or unlinking an account the server
  already removed, drops the entry without a lock when its directory or database is gone.
- **Marker before intent (D8).** The ownership marker is written before the intent, so a crash
  between them leaves no intent and the attempt is cleaned up rather than needing manual
  recovery.
- **Recovery (D9)** logs and skips entries in `.imports/` that aren't attempt tokens, removes
  an attempt directory that never got its owner lock if it is empty, and deletes leftovers in
  `.removed/`. A refusal names the attempt and what disagrees, and blocks every registry
  operation until resolved.
- **Hook (D11).** The crash hook is a per-`Dir` field set from `export_test.go`, so parallel
  tests don't share it. Tests that fork helper processes run sequentially: between fork and
  exec, a child briefly shares the parent's flock descriptors, which can make a released lock
  look held to a parallel test. Recovery then only skips the attempt until the next registry
  use.
- **Non-Unix.** The registry lock is best effort there, like the account lock, and publication
  is refused (`ErrImportUnsupported`).

The sections below are the reviewed draft, kept unchanged.

**Goal:** Complete Phase 16 Stage 4. Every registry read and mutation, account selection, open
and lock acquisition follows one protocol, so concurrent link, unlink, logout and import cannot
lose an update, recreate a removed account or connect an obsolete one. A converted account can
be built privately and published atomically, and every interrupted publication either rolls
forward or reports that recovery is required.

**Architecture:** `internal/store` gains a destination-wide registry lock, a single
select-open-lock operation with generation checks, noncreating opens, a private staging area
under `.imports/`, an intent-based publication sequence using Linux
`renameat2(RENAME_NOREPLACE)`, and recovery that runs before every registry use.
`internal/signal` moves `device`, Connect, Unlink, link and logout marking onto it. No fork
change and no user-facing command: the converter and `account import` are Stage 5.

**Spec:** `docs/superpowers/specs/2026-10-10-account-import-design.md`, "Initial supported
boundary" (platform paragraph), "Publication and recovery" and the Stage 4 row.

## Global Constraints

- Scope is Stage 4 only. Stage 4 provides the staging and publication API that the Stage 5
  converter will call, and imports no account. Its tests build synthetic staged databases.
- No lock wait while holding the registry lock. The account flock is only ever tried
  nonblocking under it.
- Never create a missing directory or database while opening a registered account. Creation is
  reserved for linking and private staging.
- Never delete or adopt an arbitrary orphan. Only an attempt with ownership proof may be removed
  or rolled forward.
- Publication is refused on platforms without the required primitives. Ordinary account data
  stays backend-compatible.
- Commits go to go-signal `main`. No committed `replace` or `go.work`.

## Current behaviour (go-signal `main` at `8e77c6a`)

| Area               | Behaviour                                                                                                                                                                                                                          | Location                                                                            |
| ------------------ | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------- |
| Registry writes    | `PutAccount`, `MarkUnlinked` and `RemoveAccount` read, modify and write `accounts.json` with no lock, so two processes can lose an update. The temp-file rename doesn't sync the parent directory.                                 | `internal/store/accounts.go:72`, `:97`, `:119`, `:141`                              |
| Opening            | `OpenAccount` creates a missing account directory and database, and always migrates, even while another process holds the account.                                                                                                 | `internal/store/store.go:35`                                                        |
| Account lock       | `Dir.Lock` creates the account directory. On non-Unix systems, `tryLock` always succeeds.                                                                                                                                          | `internal/store/lock.go:22`, `internal/store/lock_other.go:13`                      |
| Selection and open | `selectAccount` reads the registry, `device` opens the database, and Connect and Unlink take the flock afterwards. These are separate steps, so an unlink and relink in between can recreate an orphan or lock a stale lock inode. | `internal/signal/meow.go:607`, `:569`, `:216`; `internal/signal/meow_devices.go:82` |
| Store-only methods | `withStore` and `storeDevice` open the database without the flock. They serve reads and go-signal's own table writes (inbox, overrides, identity trust) while another process, such as `mcp serve`, holds the account.             | `internal/signal/meow_inbox.go:127`, `internal/signal/meow_identity.go:592`         |
| Link               | `StoreProvisionedDevice` takes the lock and creates the directory and database before `finishLink` writes the registry entry, so a crash in between leaves an unregistered directory. Relinking an ACI replaces its entry.         | `internal/store/linkstore.go:130`, `internal/signal/meow.go:538`                    |
| Unlink             | `removeLocal` closes the database, removes the entry, then deletes the directory while holding the flock.                                                                                                                          | `internal/signal/meow_devices.go:139`, `internal/store/accounts.go:119`             |
| Logout             | `markUnlinked` sets `UnlinkedAt` by a read-modify-write.                                                                                                                                                                           | `internal/signal/meow.go:727`                                                       |
| Staging            | None.                                                                                                                                                                                                                              |                                                                                     |

## Decisions

- **D1 Registry lock.** `<data-dir>/accounts.lock` (0600, flock), separate from `accounts.json`
  so the file being replaced is never the locked one.
  - It is taken nonblocking with a short bounded retry (about 2 s, backing off from 10 ms),
    otherwise it fails with `ErrRegistryBusy`. Critical sections are short and never wait on
    another lock, so the retry only covers ordinary contention.
  - One locked entry point, `Dir.withRegistry(func(*registry) error)`, covers every registry
    read and mutation, account-directory creation and publication. The `registry` value carries
    the unlocked helpers (read, write, recover), so recovery never re-locks.
  - `writeAccounts` also syncs the data directory after the rename.
- **D2 Lock order.** A process holding an account flock may briefly take the registry lock (for
  example, logout marking during receive). Under the registry lock, the account flock is only
  tried nonblocking, and a failure releases the registry lock before returning
  `ErrAccountInUse`. No path waits for one lock while holding the other.
- **D3 One select-open-lock operation.** `Dir.OpenRegistered(ctx, selector, mode)` returns the
  entry, the open `Store`, the lock (connected mode) and the recorded generation.
  - The **generation** is the device and inode of the account directory, its lock file and
    `account.db`, recorded under the registry lock.
  - **Connected mode:** under the registry lock, select the entry, open the lock file without
    creating it, try the flock nonblocking, revalidate the entry and the generation, then release
    the registry lock. Migrate and load while still holding the flock.
  - **Snapshot mode:** under the registry lock, select the entry and pin the generation, then open
    without creating and without migrating. A database whose schema is older than this build is
    refused with `ErrAccountInUse` if another process holds it, and is otherwise migrated in
    connected mode first. See open question 1 for writes.
  - **Later Connect:** never reuses a snapshot's device. It runs the connected mode again, and a
    changed generation or entry is `ErrAccountChanged`. It fails rather than silently reselects;
    an explicit command rerun reselects.
  - `device`, `storeDevice`/`withStore`, CheckLock, Connect, Unlink, `finishLink` and
    `markUnlinked` are refactored onto it. Adding locks around the current helpers is not enough.
- **D4 Noncreating opens.** A registered account whose directory, lock file or database is
  missing is `ErrAccountMissing`, an actionable corruption error. `Dir.Lock` stops creating
  directories. Only `LinkStore` (D5) and staging (D7) create.
- **D5 Link.** `LinkStore.open` creates or reuses the account directory under the registry lock
  and takes the flock nonblocking there. `finishLink` writes the entry under the registry lock
  while still holding the flock. Relinking an already registered ACI keeps today's behaviour.
  An unregistered directory at that path is open question 2.
- **D6 Unlink and logout.**
  - Unlink, holding the flock, takes the registry lock, removes the entry, syncs, then renames the
    directory to `.removed/<token>` and syncs the data directory. It deletes it after releasing
    the registry lock. A stale selector then finds no entry and no directory, and recreates
    nothing. Leftover `.removed/` entries are deleted by the next registry use.
  - `MarkUnlinked` is a registry-locked read-modify-write that only touches the entry if its ACI
    and generation still match.
- **D7 Staging.** `Dir.BeginImport(ctx)` allocates `.imports/<random-token>/` under the registry
  lock, creates `owner.lock` in it and holds that flock for the whole attempt, then releases the
  registry lock.
  - Directories are 0700, files 0600. The account is built at `.imports/<token>/<aci>/` through a
    builder callback. Stage 5's converter is that callback; Stage 4 tests use a synthetic one.
  - Finishing the stage:
    1. `PRAGMA wal_checkpoint(TRUNCATE)`, then close the database.
    2. Fsync `account.db` and every staged directory.
    3. Reopen read-only and check that the schema versions are current and the device for the
       ACI exists.
    4. Record the database's SHA-256 digest, then close it.

    No open SQLite handle survives a rename.
- **D8 Publication.** `(*Import).Publish(entry)` holds the registry lock throughout:
  1. Run recovery (D9).
  2. Reread the registry.
  3. Refuse collisions: an entry with the ACI, any filesystem entry at `<data-dir>/<aci>`
     (`Lstat`, so an orphan, a symlink or an unlinked account counts), or another entry with the
     same number or PNI (ambiguous). Replacing `PutAccount` and creating `Lock` are not used as
     guards.
  4. Write `.imports/<token>/intent.json` (version, the exact registry entry, token, digest) by
     temp file, fsync, rename and directory fsync. Also write the marker
     `<aci>/.import-owner` holding the token, fsynced. From here on, cancellation finishes the
     publication or returns `ErrRecoveryRequired`.
  5. `renameat2(stage/<aci>, <data-dir>/<aci>, RENAME_NOREPLACE)`, then fsync the data
     directory.
  6. Append the entry, using the registry write plus data-directory fsync.
  7. Retire: remove the marker, fsync the account directory, remove `intent.json`, fsync the
     stage directory, then remove the stage directory.
- **D9 Recovery.** Before every registry use, scan `.imports/` and `.removed/`:
  - An attempt whose owner lock can't be taken is active and is skipped.
  - **No intent:** a stage with ownership proof (its own `owner.lock` and token-named directory)
    is removed. Anything unexpected is left alone and reported.
  - **Intent, account still staged:** roll forward from D8 step 5, after checking the marker
    token, the ACI and the digest.
  - **Intent, account renamed, no entry:** roll forward from step 6, after checking the marker
    and the digest. The account is still invisible, so nothing has opened it.
  - **Intent, exact entry already present:** retire without rehashing, since normal ratchet
    progress may already have changed the database. The entry is never added twice.
  - **Conflicting or missing evidence** returns `ErrRecoveryRequired`, naming the path and what
    disagrees. Nothing is deleted or adopted.
- **D10 Platform.** Publication is in `publish_linux.go` (`golang.org/x/sys/unix.Renameat2`,
  already a dependency). Elsewhere, `BeginImport` returns `ErrImportUnsupported` before touching
  anything. Directory sync uses `File.Sync` on an opened directory, with errors surfaced as
  explicit recovery outcomes.
- **D11 Evidence harness.**
  - A test-binary step runner (the `scripts/test-prekey-allocators.sh` pattern),
    `scripts/test-account-publication.sh`, run by `just test-diff`. Each step is one process; a
    hook terminates it with `os.Exit` at a named durable boundary.
  - Subprocess barriers (files polled by the steps) order concurrent processes for the race
    cases.
  - Both builds (cgo and `libsignal_go`) run the steps in both starting orders, as the allocator
    script does.

## Tasks

1. `internal/store`:
   - `registry.go`: lock, `withRegistry`, unlocked helpers, synced writes. The existing public
     registry functions become locked wrappers.
   - `open.go`: `OpenRegistered`, generations, noncreating open, snapshot and connected modes.
   - Changes to `lock.go`, `store.go` and `linkstore.go` for D4 and D5.
2. `internal/store`: `imports.go` (staging, finish, intent, publish), `publish_linux.go` and
   `publish_other.go`, `recovery.go`. Crash hooks are set only from `export_test.go`.
3. `internal/signal`: move `device`, `storeDevice`, `withStore`, CheckLock, Connect, Unlink,
   `finishLink` and `markUnlinked` onto D3, D5 and D6, keeping today's errors and exit codes
   (`ErrAccountInUse`, `UnlinkedError`). The `signaltest` fake gets the new errors only where
   command tests need them.
4. Tests (listed in the test matrix), the step runner script and the `justfile` hook-up.
5. Docs: verification record `docs/account-import-publication.md`, the data-dir layout on
   AGENTS.md's `internal/store` line (`accounts.lock`, `.imports/`, `.removed/`), the spec
   status line and PLAN.md.

## Test matrix (spec Stage 4 evidence)

| Evidence                       | Test                                                                                                                                                                                                                                                                         |
| ------------------------------ | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Concurrent registry updates    | `TestRegistryConcurrentUpdates` (subprocesses): link entry writes, unlink, `MarkUnlinked` and publish run in parallel against the same registry, and every expected entry and mark survives.                                                                                 |
| Lock order                     | `TestRegistryLockOrder`: a holder of an account flock takes the registry lock while another process holds the registry lock and tries that flock; neither waits forever.                                                                                                     |
| Stale selection vs unlink      | `TestStaleSelectionUnlink` (barriers): A pins a snapshot, B unlinks, A's Connect fails with `ErrAccountChanged` and no directory or database is recreated.                                                                                                                   |
| Stale selection vs reimport    | `TestStaleSelectionReimport`: as above, then B publishes a new account with the same ACI; A never connects the old lock inode.                                                                                                                                               |
| Noncreating open               | `TestOpenRegisteredMissing`: missing directory, lock file or database is `ErrAccountMissing` and creates nothing.                                                                                                                                                            |
| Snapshot does not migrate      | `TestSnapshotNoMigrate`: an old-schema database held by another process is refused, not migrated.                                                                                                                                                                            |
| Orphan and collision refusal   | `TestPublishCollision/*`: existing entry, orphan directory, symlink, unlinked account, same number, same PNI. Each is refused and leaves the stage intact.                                                                                                                   |
| Crash at each durable boundary | `scripts/test-account-publication.sh`: exit after stage allocation, after the build, after intent sync, after rename, after the parent sync, after the registry write, during retirement. The next process recovers to exactly one published account or a clean destination. |
| Recovery refusals              | `TestRecoveryEvidence/*`: wrong digest, wrong marker token, missing marker, ACI mismatch and an entry that differs. Each gives `ErrRecoveryRequired` and changes nothing.                                                                                                    |
| Active attempt protection      | `TestRecoverySkipsActiveStage`: a stage whose owner lock is held is neither removed nor rolled forward.                                                                                                                                                                      |
| Sync and rename failures       | `TestPublishSyncFailure`: injected rename and directory-sync errors return explicit recovery outcomes.                                                                                                                                                                       |
| Invisible until committed      | `TestPublishInvisible`: at every boundary before the registry write, selection and `Accounts` do not show the account.                                                                                                                                                       |
| Platform                       | `TestImportUnsupported` (non-Linux build): `BeginImport` refuses and creates nothing.                                                                                                                                                                                        |

Two mutation checks are planned before committing: dropping the generation revalidation (the
stale-selection tests must fail), and retiring the intent before the registry write (the crash
runner must fail).

## Verification

Use `GOWORK=off`, with `GOTMPDIR`/`TMPDIR` on disk:

- `just fmt`, `just check`, `just check-purego`, `just test-fork`, `just test-diff` (including the
  new step runner), and untagged `CGO_ENABLED=0 go test ./...`;
- scoped `-race` runs of the new tests on both backends;
- `scripts/test-prekey-allocators.sh` and the unchanged 264-action/48-Java-leg compatibility
  ledger, since opening is refactored underneath both.

No phone or server is used.

## Open questions for review

1. **Writes without the account flock (D3).** Store-only methods write go-signal's own tables
   while another process, such as `mcp serve`, holds the account. Proposed: keep that, through a
   snapshot that is noncreating and nonmigrating but opened read-write; SQLite already serializes
   writers. The alternative is the spec's strict read-only snapshot, which breaks those commands
   while `mcp serve` runs.
2. **Unregistered directory at link (D5)**, left by a crashed link. Proposed: linking that ACI
   adopts it, as today. Import never adopts. The alternative is refusing and asking for manual
   removal.
3. **`accounts.json` version.** Proposed: it stays 1, and `accounts.lock` is the only new file.
   Older go-signal processes ignore the lock, so they must be stopped before importing, as the
   spec says. Bumping the version would make them refuse instead, which also breaks plain
   downgrades.
4. **Crash hook.** Proposed: an unexported hook set only from `export_test.go`, compiled into the
   test binary only. The alternative is a build tag.
