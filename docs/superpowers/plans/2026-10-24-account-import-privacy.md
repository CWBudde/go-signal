# Privacy Guards Implementation Plan

Status: **draft for review** (2026-10-24). No fork or go-signal code has been written for this stage.
Decisions D2–D9 are proposals; D1 was approved on 2026-10-24.

**Goal:** Complete Phase 16 Stage 3. An imported account's blocking, profile sharing, account privacy settings and own profile key must survive the first and every later storage fetch, restart and bypass write until authenticated remote storage genuinely agrees, without the import sending anything to the phone.

**Architecture:** The mautrix-signal fork gains durable privacy guards (schema v30, compatibility v30), typed ACI/PNI blocking and a pre-write projection step. Storage application keeps the authenticated raw update for freshness and identity checks, then projects a private clone through the guards. SQL writes, events and the post-commit `AccountRecord` see only the projection. Bypass writers (message requests, profile keys, profile refresh, backup import) consult the same guards. go-signal's consumers read effective values.

**Spec:** `docs/superpowers/specs/2026-10-10-account-import-design.md`, "Storage keys and privacy reconciliation" (paragraphs 2–5) and the Stage 3 row. Source map: `docs/account-import.md`, "Contacts, groups, settings and storage keys" and "Imported local privacy decisions".

## Global Constraints

- Scope is Stage 3 only. Registry/publication recovery (Stage 4), the converter (Stage 5) and live acceptance (Stage 6) stay open. Stage 3 adds the guard seed API the converter will call; it imports no account.
- Import and reconciliation never enqueue outgoing sync work. A later explicit local command keeps its normal synchronization.
- There is no timeout. A guard is released only by the rule in D4.
- Settings that can't be represented faithfully are refused, never converted to permissive defaults.
- Fork release and parent pin are immutable, with no committed `replace` or `go.work`. Commits go to go-signal `main` and the fork's `purego` branch.

## Current behaviour (fork `purego` at `60cc9ee`)

| Area            | Behaviour                                                                                                                                                                                                                                                                                                                                                                                              | Location                                                                                                                                                    |
| --------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ | ----------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Contact apply   | `Blocked` overwritten unconditionally. `Whitelisted` only ever set (never true→false) and marked changed on every sync.                                                                                                                                                                                                                                                                                | `pkg/signalmeow/storageservice.go:113`, `:117`                                                                                                              |
| Account apply   | The whole `AccountRecord` is copied into the device, and its profile key goes to `StoreProfileKey`. Published to `Store.AccountRecord` after commit.                                                                                                                                                                                                                                                   | `storageservice.go:141`, `:146`, `:412`                                                                                                                     |
| Freshness       | Every sync is a full fetch (`FetchStorage(ctx, key, 0, nil)`). No manifest version is persisted. Removed and missing records are ignored.                                                                                                                                                                                                                                                              | `storageservice.go:46`, `:174`, `:387`                                                                                                                      |
| Group records   | Only the master key is stored. `Blocked` and `Whitelisted` are ignored.                                                                                                                                                                                                                                                                                                                                | `storageservice.go:130`                                                                                                                                     |
| Blocking lookup | `IsBlocked(ctx, aci uuid.UUID)`. A PNI sender's UUID is passed as if it were an ACI. PNI-only rows don't persist blocked/whitelisted.                                                                                                                                                                                                                                                                  | `store/recipient_store.go:317`, `receiving.go:622`, `:117`                                                                                                  |
| Block cache     | `map[uuid.UUID]bool`, cleared after every `DoContactTxn`.                                                                                                                                                                                                                                                                                                                                              | `store/device.go:21`, `:100`                                                                                                                                |
| Bypass writers  | MessageRequestResponse ACCEPT sets whitelisted (BLOCK etc. only emit events). A PNI message sets `Whitelisted=false` if nil. `StoreProfileKey` raw upserts from data messages (including sent transcripts carrying our own key), stories and seven group paths. Profile refresh replaces `Profile` wholesale. An outgoing profile-key update sets whitelisted. Backup import sets blocked/whitelisted. | `receiving.go:812`, `:592`, `:957`, `:784`; `story_receive.go:23`; `groups.go:671–977`; `contact.go:77`; `sending.go:723`; `store/backup_store.go:164`      |
| Hooks           | `StorageUpdateHandler` runs inside the contact transaction, but after the writes. No pre-write hook exists.                                                                                                                                                                                                                                                                                            | `client.go:68`, `storageservice.go:401`                                                                                                                     |
| Raw consumers   | Receipt/typing gating and E164 inclusion read `AccountRecord`. go-signal's blocked-list construction, `BlockedACIs` and story-audience filtering read raw rows or raw storage records.                                                                                                                                                                                                                 | `sending.go:200`, `:760`, `:764`; `internal/signal/meow_contacts.go`; `internal/store/contacts.go:64`; `internal/signal/meow_story_audiences.go:68`, `:235` |

signal-cli reference (v0.14.9-1, `2a9a4b1`):

- `StorageSyncModels.localToRemoteRecord` builds the self `AccountRecord` from the stored record, overlays the profile key and profile, then `ConfigurationStore` values with defaults: typing/read receipts/sealed-sender indicators/link previews `true`, unlisted number `false`, phone sharing `UNKNOWN` (`lib/.../syncStorage/StorageSyncModels.java:60–94`). `CONTACTS` and `NOBODY` both map to remote `NOBODY` (`:45–50`).
- `AccountRecordProcessor.updateLocal` writes the six configuration values from a remote record (`AccountRecordProcessor.java:177–187`).
- `ContactRecordProcessor` applies `blocked`, `blockedAtTimestamp` and `whitelisted` (`ContactRecordProcessor.java:340–376`).
- An incoming blocked-list sync only ever sets blocked (`IncomingMessageHandler.java:624–639`). signal-cli has no MessageRequestResponse handling.

## Decisions

- **D1 Schema v30, compatibility v30 (approved 2026-10-24).** Older releases would apply storage without guards and undo protected values, so they refuse upgraded databases. Going back means relinking, as with v28/v29.
  - New table `signalmeow_privacy_guards`:
    - `account_id`;
    - `entity_kind` (`account`, `aci`, `pni`, `e164`, `group`) and `entity_id` (canonical typed ID, empty for `account`);
    - `field` (`blocked`, `whitelisted`, `read_receipts`, `typing_indicators`, `sealed_sender_indicators`, `link_previews`, `unlisted_phone_number`, `phone_number_sharing_mode`, `own_profile_key`);
    - `local_present`, `local_value` (encoded, see D8);
    - `protected` (bool);
    - `remote_present`, `remote_value`, `observed_version`.
    - Primary key `(account_id, entity_kind, entity_id, field)`.
  - New `signalmeow_device.storage_manifest_version` (nullable bigint): highest manifest version applied.
  - The PNI-only upsert persists `blocked` and `whitelisted`.
- **D2 Typed blocking.**
  - `IsBlocked(ctx, libsignalgo.ServiceID)` replaces the UUID form. The cache is keyed by service ID, so an ACI and a PNI with the same UUID bytes never share an entry.
  - The receive path passes the sender's typed service ID.
  - E164 and group block state are stored and guarded but not enforced on receive in this stage, which is a documented limitation. The importer keeps refusing blocked groups (mapping table).
- **D3 Pre-write projection.**
  - A fork `PrivacyProjector` interface lives in `pkg/signalmeow/store`, is backed by the guard table, and is set on `Device` before receive workers start. With no guard rows it is the identity, so behaviour without an import is unchanged apart from D5's two-way whitelist.
  - `ApplyStorage` keeps the raw `StorageUpdate` for the freshness check (`Version` > highwater) and for `StorageUpdateHandler`, which needs authenticated identities. It works on a `proto.Clone` and never mutates the raw update.
  - `processStorageInTxn` writes the projection.
  - Guards, the highwater, recipients, identities, the own key and the device `AccountRecord` commit in one `DoContactTxn`.
  - `Store.AccountRecord`, the block cache and the `ContactList` event are published only after commit. A failed commit publishes nothing and leaves the cache cleared.
  - An update whose version is not newer than the highwater still applies unguarded fields as today, but releases nothing.
- **D4 Release rule.** A protected field is released only when:
  - the fetch is a complete manifest with `Version` strictly greater than the guard's `observed_version`;
  - the raw record for that entity is present; and
  - its semantic value equals the local value.
    Proto3 booleans omitted from a present record count as `false`. A missing record or contact, an unknown enum, an absent required key, or equality introduced by the overlay never releases. Unrelated records still apply. Once released, later remote changes apply normally.
- **D5 Bypass writers use the same guards.**
  - The storage path's whitelist assignment becomes two-way (true and false), which the spec requires.
  - MessageRequestResponse ACCEPT, the PNI-message `Whitelisted=false` path, the outgoing profile-key whitelist, profile refresh and backup import pass through the projector. They may update unrelated fields but can't replace protected values or clear guards.
  - Every `StoreProfileKey` caller is routed through one guarded function. For our own ACI, a key from a sent transcript, story or group update is ignored while `own_profile_key` is protected. Keys for other recipients are unaffected.
- **D6 Local commands rearm.**
  - go-signal `contacts block`/`unblock` (`SetBlocked`) rearm the `blocked` guard for that typed ID in the same transaction as the write, with `observed_version` set to the current highwater. Future settings commands do the same for account fields.
  - `gosignal_block_overrides` stay a separate mechanism. They are applied after the fork projection, and the effective value is the override if present, otherwise the projected row. Dropping an override never clears a guard.
- **D7 Effective consumers.**
  - These read effective (projected or stored) values:
    - `sending.go` receipt/typing/E164 gating;
    - go-signal's outgoing blocked-list construction and `BlockedACIs`;
    - story-audience filtering, which also covers imported contacts that are absent from raw remote storage.
  - These stay raw, on purpose:
    - the `StorageUpdateHandler` identity reconciliation;
    - freshness and version checks;
    - storage-key handling.
  - The plan's tests assert both lists.
- **D8 Account settings encoding.**
  - Booleans encode as `0`/`1`; `phone_number_sharing_mode` uses the protobuf enum number; the own profile key is 32 raw bytes.
  - Absent (`local_present=false`) and `false` stay distinct.
  - Import refuses source sharing mode `CONTACTS` and any unknown enum.
  - The seed API takes the local values and the source cached record. It protects every field whose local value differs from the cached record. It also protects equal fields when the source storage ID or manifest metadata can't prove freshness, per the mapping.
- **D9 Java overlay vectors.**
  - Extend the existing harness with an `AccountRecord` overlay generator. It covers each configuration value present, absent and false, plus phone sharing `EVERYBODY`/`NOBODY`/absent, calling `StorageSyncModels.localToRemoteRecord`'s rules from the pinned models.
  - If those rules can't run without signal-cli's mutating account loader, the generator reimplements only the builder calls, and the plan records it as a reimplementation, not as genuine Java output.

## Tasks

1. Harness: `AccountRecordOverlayVectors` plus `generate-account-import-fixtures.sh --account-overlay <dir>`. Output goes to `internal/signal/accountimport/testdata/java-account-overlay/`, with provenance and checksums.
2. Fork `store`: the v30 upgrade (SQLite and Postgres) and `00-latest.sql`, the guard store and `PrivacyProjector`, `SeedPrivacyGuards`, typed `IsBlocked`, the typed cache, and the PNI-only blocked/whitelisted persistence.
3. Fork `signalmeow`:
   - `ApplyStorage` clone/projection/highwater/release;
   - two-way whitelist;
   - group `Blocked` stored and guarded;
   - routing of the bypass writers and the single guarded `StoreProfileKey` path;
   - typed receive lookup;
   - effective account settings for `sending.go`.
4. Fork tests (both backends, `-race`), listed in the test matrix. Then release `v0.2609.0-purego.33`.
5. go-signal:
   - pin bump;
   - `SetBlocked` rearm;
   - override/guard precedence;
   - `BlockedACIs` → typed effective blocked IDs;
   - blocked-list construction and story audiences use effective values;
   - `justfile` test-fork filters.
6. Docs: verification record `docs/account-import-privacy.md`, `docs/account-import.md` gap links, the `docs/maintenance.md` fork delta, AGENTS.md (schema v30), the spec status line and PLAN.md.

## Test matrix (spec Stage 3 evidence)

| Evidence                | Test (fork unless noted)                                                                                                                                                                     |
| ----------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| First conflicting fetch | `TestPrivacyGuardFirstFetch`: seeded blocked contact and `readReceipts=false`. A fetch with the opposite raw values leaves SQL, cache and `Store.AccountRecord` unchanged.                   |
| Restart                 | `TestPrivacyGuardRestart`: the same, after reopening the container; guards and highwater persist.                                                                                            |
| Missing vs false        | `TestPrivacyGuardMissingAndFalse`: missing record, missing contact, omitted proto3 bool (= false) and absent local value; only a present matching record releases.                           |
| Alignment, later change | `TestPrivacyGuardRelease`: an older or equal version doesn't release; a strictly newer matching record releases; the next changed record applies.                                            |
| Typed isolation         | `TestTypedBlocking`: ACI and PNI with identical UUID bytes, with blocked state and cache entries isolated; a PNI-only row persists blocked.                                                  |
| Bypass handlers         | `TestPrivacyGuardBypass/*`: MessageRequestResponse ACCEPT, PNI message, sent-transcript own key, group own key, story own key, profile refresh, backup import.                               |
| Raw consumers           | `TestPrivacyGuardRawConsumers`: `StorageUpdateHandler` receives the untouched raw update; effective gating in `sending.go`. go-signal: blocked list and story audience use effective values. |
| Rollback / cache        | `TestPrivacyGuardRollback`: a forced `StorageUpdateHandler` error publishes no projected settings, no events and no cache entries.                                                           |
| Rearm                   | go-signal `TestSetBlockedRearmsGuard`: a later conflicting fetch keeps the local command's value.                                                                                            |
| Schema floor            | `TestPrivacyGuardSchemaFloor`: v29 → v30 upgrade; a v29 reader refuses v30.                                                                                                                  |
| Java overlay            | `TestJavaAccountOverlay` (go-signal): the committed vectors match the seed encoding and refusals (`CONTACTS`, unknown enum).                                                                 |

Two mutation checks are planned before release: releasing on equal versions, and publishing the cache before commit.

## Verification

Use `GOWORK=off`, with `GOTMPDIR`/`TMPDIR` on disk:

- `just fmt`, `just check`, `just check-purego`, `just test-fork`, `just test-diff`, untagged `CGO_ENABLED=0 go test ./...`;
- scoped `-race` runs of the new tests on both backends;
- the fixture generator reproducing the vectors byte for byte;
- the unchanged 264-action/48-Java-leg compatibility ledger.

No phone or server is used. Live privacy behaviour remains Stage 6 evidence.

## Open questions for review

- D2: should E164 and group blocking be enforced on receive in this stage, or stay storage-only as proposed?
- D3: should a non-newer manifest still apply unguarded fields (proposed, matches today) or be skipped entirely?
- D6: keep the go-signal block overrides (proposed), or replace them with guards once these land?
- The fork doesn't handle incoming `SyncMessage.Blocked` or configuration syncs today. The spec requires any future handler to use the guards, and this plan adds none.
