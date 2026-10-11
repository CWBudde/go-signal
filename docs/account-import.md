# signal-cli account import: format mapping

This is the Phase 16 source and compatibility specification, reviewed on 2026-10-10.
Stage 1 protocol compatibility was verified on 2026-10-11; see the results below.
There is no import command yet. No source account has been imported or used on Signal's
servers; the remaining Phase 16 implementation, fixtures and live acceptance are open.

## Source boundary

The first importer should target **signal-cli v0.14.9**, with registry version **2**,
account JSON version **11** and SQLite `PRAGMA user_version = 31`. These are independent
format versions, not application version numbers. The files do not prove which application
release last wrote them, so acceptance must check the actual schema, field types and records
as well as version markers. No broader release range is established by this investigation.

Evidence comes from the read-only reference checkout at
`2a9a4b1f3cbb030e10f3bdeb26df7f666d44ccf3`. That checkout declares
`0.14.10-SNAPSHOT`; its entire `lib/` tree and `gradle/libs.versions.toml` are identical to
release v0.14.9 (`3997429779095ff3a2d64c877126e84fce9852aa`). The only differences from
that tag are the root build version and changelog. This establishes source-format
equivalence, not runtime interoperability.

| Source                                      | Evidence                                                                                                                                                                                                                                                                                                                                  |
| ------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Registry version and entry shape            | [AccountsStore.java](../reference/signal-cli/lib/src/main/java/org/asamk/signal/manager/storage/accounts/AccountsStore.java), [AccountsStorage.java](../reference/signal-cli/lib/src/main/java/org/asamk/signal/manager/storage/accounts/AccountsStorage.java)                                                                            |
| JSON version, loading and serialized fields | [SignalAccount.java](../reference/signal-cli/lib/src/main/java/org/asamk/signal/manager/storage/SignalAccount.java), constants at lines 119–120, loader at 549, `Storage` at 2117                                                                                                                                                         |
| SQLite schema version and migrations        | [AccountDatabase.java](../reference/signal-cli/lib/src/main/java/org/asamk/signal/manager/storage/AccountDatabase.java), version 31 at line 36; [Database.java](../reference/signal-cli/lib/src/main/java/org/asamk/signal/manager/storage/Database.java), `PRAGMA user_version`                                                          |
| Target device and protocol tables           | Fork [DeviceData](https://github.com/CWBudde/mautrix-signal/blob/7f2481fad0c11ad2b3917a4979bd70f70d3ed04b/pkg/signalmeow/store/device.go) and [schema 27](https://github.com/CWBudde/mautrix-signal/blob/7f2481fad0c11ad2b3917a4979bd70f70d3ed04b/pkg/signalmeow/store/upgrades/00-latest.sql), pinned by go.mod to `v0.2609.0-purego.30` |

Candidate inputs are registered **LIVE linked devices** with `deviceId > 1`, valid nonzero
ACI and PNI, both local identity pairs and registration IDs, an E.164 number and a nonempty
password. Device 1 is a primary account and remains outside this project's linked-device
scope. Pending linking, unregistered accounts and staging environments must be rejected.
Local validation cannot prove that the server still accepts these credentials.

Older JSON/SQLite formats must be rejected with instructions to upgrade a separate copy
using signal-cli and create a fresh stopped snapshot. The importer must never invoke
signal-cli's loader to perform migration: it opens the account JSON for writing, may save
migrated data and runs SQLite migrations. Newer formats require a separate compatibility
review and fixtures. A version marker alone cannot permit unknown protocol encodings.

## Layout and snapshot

Given a signal-cli configuration root, `PathConfig` puts account data under `data/`.
`data/accounts.json` selects an entry by number or ACI; its `path` is an opaque basename,
not necessarily the phone number. The registry stores `path`, `environment`, `number`
and `uuid`. The selected JSON is `data/<path>` and its SQLite database is
`data/<path>.d/account.db`. The adjacent directory can also contain cached messages and
a storage manifest. Attachments, avatars and stickers live outside `data/`.
See [PathConfig.java](../reference/signal-cli/lib/src/main/java/org/asamk/signal/manager/internal/PathConfig.java)
and `SignalAccount.getFileName/getUserPath/getDatabaseFile` (lines 504–538).

The import source must be a coherent snapshot made while signal-cli is stopped; stop any
daemon and scheduled invocations too. JSON and SQLite do not form one atomic source
transaction. Retain the database's WAL when it contains committed data. Reading only
`account.db` with `immutable=1` can miss those rows. Java's account lock is a `FileChannel`
lock on the JSON file; our destination `flock` on `<aci>/lock` does not protect that source.
A portable source-lock implementation or an enforceable stopped-snapshot contract is a
required implementation decision. Never run both clients with the copied device state:
their ratchets and consumed prekeys would diverge.

Reject absolute/traversing registry paths, symlinks that escape the selected root,
ambiguous account selection and mismatches between registry and JSON identity/environment.
Open existing source files read-only. If the SQLite driver cannot read WAL safely without
creating sidecars, read a private copy of the complete stopped snapshot. Do not checkpoint,
change pragmas persistently or create lock files in the source. Source-file hashes and
directory listings before/after import belong in the failure-recovery acceptance tests.

The destination is a new `<data-dir>/<aci>/account.db` plus our version-1 registry entry.
Use our migrations to create it, rather than renaming signal-cli's database. Both a registry
entry and an existing account directory are collision evidence, including unlinked entries
and orphan directories. [PutAccount](../internal/store/accounts.go) replaces matching ACIs
and [OpenAccount](../internal/store/store.go) creates or opens existing directories, so
neither is an import collision guard. Publication needs destination-wide serialization and
crash recovery across the account-directory rename and registry replacement; a per-account
lock and SQLite transaction alone do not make these two filesystem changes atomic.

## Account and protocol mapping

Here `account_id` is always our local ACI. For source `account_id_type`, ACI is **0** and
PNI is **1**; map it to the corresponding **local** typed service ID, never to a remote
recipient. ACI addresses are canonical UUID strings; PNI addresses use `PNI:<uuid>`.
Keep them distinct even when their UUIDs are equal. Current protocol tables use `address`
directly; older recipient-ID/UUID-based schemas are excluded rather than guessed.

| Source data                                                                                                                | Destination and conversion                                                                                                         | Required checks / limitations                                                                                                                                                                                                                                                                                                                                                    |
| -------------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| JSON `aciAccountData` / `pniAccountData`: `serviceId`, `registrationId`, base64 `identityPublicKey` / `identityPrivateKey` | `DeviceData` ACI/PNI, registration IDs and reconstructed `IdentityKeyPair`; serialize through libsignalgo into `signalmeow_device` | Validate kind, nonzero IDs, key encoding and public/private agreement; preserve both original key pairs and IDs. Never generate replacement identities.                                                                                                                                                                                                                          |
| JSON `deviceId`, `number`, `password`                                                                                      | `DeviceData.DeviceID`, `Number`, `Password`; matching `accounts.json` entry                                                        | Preserve device ID/password verbatim after validation. `BasicAuthCreds` uses `<ACI>.<deviceId>`; import must not provision a new device. JSON `timestamp` is a save time, not a linking time: leave `LinkedAt` unknown.                                                                                                                                                          |
| JSON `encryptedDeviceName`                                                                                                 | No direct registry field conversion                                                                                                | It is ciphertext, not a display name. Leave local `DeviceName` unknown until a supported decryption/server lookup; do not store ciphertext as its name.                                                                                                                                                                                                                          |
| SQL `session(account_id_type,address,device_id,record)`                                                                    | `signalmeow_sessions(account_id,service_id,their_service_id,their_device_id,record)`                                               | Deserialize the whole `SessionRecord`; preserve current and archived states, pending prekeys and skipped-message keys. Check local identity/registration consistency and remote typed address/device. Do not rebuild a fresh session from a peer key.                                                                                                                            |
| SQL `pre_key(account_id_type,key_id,public_key,private_key)`                                                               | Reconstruct `PreKeyRecord`; serialize into `signalmeow_pre_keys`, `is_signed=false`                                                | Preserve ID and key components; verify reconstructed public key. Do not treat a public-key blob as a serialized record.                                                                                                                                                                                                                                                          |
| SQL `signed_pre_key`: same components plus `signature,timestamp`                                                           | Reconstruct `SignedPreKeyRecord`; same target table, `is_signed=true`                                                              | Preserve original signature and millisecond timestamp; verify signature against the corresponding local identity public key. Keep retained old keys needed for queued messages, not only the active ID.                                                                                                                                                                          |
| SQL `kyber_pre_key(account_id_type,key_id,serialized,is_last_resort,stale_timestamp,timestamp)`                            | Deserialize the complete `KyberPreKeyRecord`; `signalmeow_kyber_pre_keys` including `is_last_resort`                               | Unlike EC prekeys, the source stores a serialized record. Validate supported KEM/key encoding and identity signature; compare record ID/timestamp with row metadata. Preserve last-resort classification; one-time consumption and repeated last-resort use need separate tests.                                                                                                 |
| JSON next-ID and active-ID prekey metadata; SQL `stale_timestamp`                                                          | No equivalent allocator metadata in the current target schema                                                                      | Requires allocator work before general import is safe; see below. Stale timestamps are cleanup metadata, not permission to discard retained private keys.                                                                                                                                                                                                                        |
| SQL `identity(address,identity_key,added_timestamp,trust_level)`                                                           | Both `signalmeow_identity_keys` and `gosignal_identities`                                                                          | Preserve exact typed identity and trust atomically. Source enum ordinals are 0 untrusted, 1 trusted-unverified, 2 trusted-verified; facade strings are `untrusted`, `trusted-unverified`, `trusted-verified`. Use protocol trust strings separately. Unknown enums fail closed. `added_timestamp` is the current key's recorded addition time; older key history is unavailable. |
| SQL `sender_key(address,device_id,distribution_id,record,created_timestamp)`                                               | `signalmeow_sender_keys`, UUID distribution ID and deserialized `SenderKeyRecord`                                                  | Preserve incoming sender chains and skipped keys. Validate source UUID BLOB encoding; target record has no separate creation-time column.                                                                                                                                                                                                                                        |
| SQL `sender_key_shared(address,device_id,distribution_id,timestamp)` and `group_v2.distribution_id`                        | Related to `signalmeow_outbound_sender_key_info`, but not a direct copy                                                            | Do not assume a remembered share proves current trust/eligibility. First import should leave outbound sharing metadata empty and rotate/re-distribute our outbound group keys; imported receive records remain usable. Verify that the first send cannot reuse an imported outbound key under a fresh distribution ID.                                                           |

Source reconstruction is defined by the Java stores:
[sessions](../reference/signal-cli/lib/src/main/java/org/asamk/signal/manager/storage/sessions/SessionStore.java),
[EC prekeys](../reference/signal-cli/lib/src/main/java/org/asamk/signal/manager/storage/prekeys/PreKeyStore.java),
[signed prekeys](../reference/signal-cli/lib/src/main/java/org/asamk/signal/manager/storage/prekeys/SignedPreKeyStore.java),
[Kyber prekeys](../reference/signal-cli/lib/src/main/java/org/asamk/signal/manager/storage/prekeys/KyberPreKeyStore.java),
[identities](../reference/signal-cli/lib/src/main/java/org/asamk/signal/manager/storage/identities/IdentityKeyStore.java),
[sender keys](../reference/signal-cli/lib/src/main/java/org/asamk/signal/manager/storage/senderKeys/SenderKeyRecordStore.java).
The source and target use libsignal record serialization for sessions, Kyber and sender keys.
The source's tracked `libsignal-version` is **0.103.0**, newer than our native **v0.102.2**.
That is a conversion candidate, not proof that every Java-produced record works with our
native and pure-Go implementations. Deserialization alone also does not prove
continued decryption. Fixtures must exercise prekey, established-session, skipped-key and
group-message exchanges across both backends without resetting identities or ratchets.

For imported identities, `FirstSeen` and `ChangedAt` start unknown (zero),
`PreviousKey` is absent and `PendingEvent=false`: the source current-key
`added_timestamp` does not establish the first historical key or prove a change.
Retaining that timestamp as source provenance must not relabel it as target history.

### Prekey allocation gap

The source saves `nextPreKeyId`, `nextSignedPreKeyId`, `activeSignedPreKeyId`,
`nextKyberPreKeyId` and `activeLastResortKyberPreKeyId` independently of retained rows.
Our fork's [prekey store](https://github.com/CWBudde/mautrix-signal/blob/7f2481fad0c11ad2b3917a4979bd70f70d3ed04b/pkg/signalmeow/store/prekey_store.go)
computes the next EC and Kyber IDs as `MAX(key_id)+1`. After source consumption/deletion,
that can be below the source's next ID. A sidecar that the allocator never reads would not
solve reuse. Implementation must add durable allocator watermarks used by generation,
define wrapping/exhaustion against the actual protocol ID range and test depleted/sparse
stores, last-resort keys and restart. Reject unsupported rollover or inconsistent active
references. Do not add dummy private-key rows or silently reset counters.

Both stores delete used ordinary Kyber keys and retain last-resort keys. The reviewed Java
`markKyberPreKeyUsed` has a TODO for last-resort signed-prekey/base-key reuse tracking;
there is no such history to recover from this source schema. Import must not claim to
restore replay tracking that the source never stored.

## Contacts, groups, settings and storage keys

| Source data                                                                                                                           | Destination / policy                                                                                                                                                                                                                                                                                                                                                                              |
| ------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `recipient`: `aci`, `pni`, `number`, contact/profile names, `profile_key`, `blocked`, `profile_sharing`, `needs_pni_signature`        | `signalmeow_recipients`; normalize typed IDs (the target PNI column stores the bare UUID), preserve blocked state and whitelist/profile sharing. Reject conflicting address mappings. A number-only contact cannot be invented as an ACI. Do not trust an ACI because it shares a recipient row with a PNI.                                                                                       |
| JSON own `profileKey` plus the self-recipient                                                                                         | Store the 32-byte own key through `RecipientStore.StoreProfileKey` under our ACI, following provisioning. Require agreement with a populated self-recipient key; never generate a replacement.                                                                                                                                                                                                    |
| Recipient `expiration_time`, `expiration_time_version`                                                                                | `gosignal_chat_timers` for addressable ACI direct chats, after range checks. PNI-only/number-only timers have no direct target key; identify them as unsupported before publication.                                                                                                                                                                                                              |
| `group_v2.group_id,master_key`                                                                                                        | Derive the group identifier from the 32-byte master key and compare with the source ID; store base64 master key in `signalmeow_groups`. Group title/revision/left state require decoding the Java `group_data` format or a later authoritative fetch, not copying its blob into `gosignal_groups`. Source `permission_denied` does not prove a voluntary leave.                                   |
| Group cached state, endorsements and outbound share list                                                                              | Do not import as authoritative runtime state; fetch/authorize again on the first connected group operation. Incoming sender-key records are preserved separately.                                                                                                                                                                                                                                 |
| Group-v1 data and blocked groups                                                                                                      | Unsupported by the facade. Reject nonempty legacy group data or blocked-group state for the initial supported subset; silently dropping blocks could change delivery/privacy behavior.                                                                                                                                                                                                            |
| SQL `key_value` configuration and self-recipient `storage_record`                                                                     | Reconstruct/validate an `AccountRecord` with known receipt/privacy settings; do not assume the SQL blob is our protobuf type. Source `StorageSyncModels` overlays local settings onto the saved record. Preserve explicit false and distinguish absent settings; local configuration can be newer than cached storage.                                                                            |
| JSON `accountEntropyPool`, `pinMasterKey`, `storageKey`                                                                               | Different key stages; see below.                                                                                                                                                                                                                                                                                                                                                                  |
| JSON `mediaRootBackupKey`                                                                                                             | Candidate `DeviceData.MediaRootBackupKey` after encoding/length validation. Source has no matching provisioning ephemeral backup key; leave it absent.                                                                                                                                                                                                                                            |
| Storage manifest, storage IDs, unknown storage records, CDSI tokens, send log, avatars, attachments, sticker caches and message cache | No wholesale database/cache import. Treat manifests/tokens as client-specific caches. Sticker image content, unknown storage data and message/send-log history have no complete mapping in this batch. Report exclusions explicitly; pending retry/cache work requires refusal or a proven safe disposition, not silent deletion. Our inbox, poll projections/counters and pin state start empty. |
| PIN, registration-lock state, auth-credential salt, username-link secret material and source-specific metadata                        | No current use-case mapping; do not populate unrelated keys or claim primary/registration support. Do not copy secrets into the registry, logs or diagnostics.                                                                                                                                                                                                                                    |

Privacy configuration lives in
[ConfigurationStore.java](../reference/signal-cli/lib/src/main/java/org/asamk/signal/manager/storage/configuration/ConfigurationStore.java)
under `config-read-receipts`, `config-unidentified-delivery-indicators`,
`config-typing-indicators`, `config-link-previews`, `config-phone-number-unlisted` and
`config-phone-number-sharing-mode`. Values are typed SQL values, not JSON strings. The self-recipient's `storage_record` is
an encoded AccountRecord, not a StorageRecord wrapper; its compatibility with our protobuf
must still be checked against Java-produced fixtures.
See [StorageSyncModels.java](../reference/signal-cli/lib/src/main/java/org/asamk/signal/manager/syncStorage/StorageSyncModels.java)
for local-to-account-record construction. Unsupported privacy states must fail explicitly
instead of being converted to permissive defaults. Imported local trust decisions also
need initial conflict protection in `gosignal_storage_identities`; import itself must not
enqueue verification sync or let the first storage fetch overwrite them as unknown trust.

In particular, source phone-number sharing `CONTACTS` has no distinct AccountRecord
value: Java maps it to `NOBODY`, as it does source `NOBODY`. An importer must reject
`CONTACTS` until it has an explicit supported policy, rather than silently presenting
that collapse as exact preservation. Recipient `mute_until`, `archived`, `hidden` and
`hide_story` also have no corresponding facade settings. Identify these exclusions in
the import result and reject nondefault settings unless a supported mapping is added;
do not silently re-enable a hidden story or erase a user's mute preference.

### Imported local privacy decisions

The same first-fetch conflict problem exists for blocking and account settings. Source
local changes can be newer than its saved storage record: `ConfigurationStore` rotates
the self storage ID when read-receipt settings change, and `RecipientStore.storeContact`
rotates contact storage IDs. The source may not have uploaded those changes yet.
Our pinned fork overwrites recipient `Blocked` and the device `AccountRecord` during
storage application, then publishes that account record after commit. Copying a blocked
contact or `readReceipts=false` only once could therefore undo the imported decision on
the first sync. The existing block overrides expire or retire against later manifest
versions; they are not a complete import conflict policy.

Implementation must either preserve imported decisions with durable conflict protection
integrated into storage transactions and cache publication, or refuse accounts whose
pending local changes/freshness cannot be resolved safely. Compare local columns and
configuration with the corresponding source cached records, including their storage IDs
and manifest metadata; equality to a cached record alone does not establish remote
freshness. Define when authoritative remote alignment releases protection, without
echoing an import as a new phone-side command. Apply the policy to profile sharing and
other supported privacy settings too, with unknown settings handled explicitly.
Tests must apply an older/conflicting first fetch, reopen the account, then reconcile a
matching and later changed snapshot; blocked state and explicit false receipts must
survive rejected/older updates, both in SQLite and in the live caches. This is an
implementation gate, not behavior the current stores already provide for imports.

### Storage-key gap

`SignalAccount.getOrCreateStorageKey` (lines 1730–1742) prefers the stored **derived**
`storageKey`, then derives from `pinMasterKey`, then from `accountEntropyPool`.
signalmeow's [FetchStorage](https://github.com/CWBudde/mautrix-signal/blob/7f2481fad0c11ad2b3917a4979bd70f70d3ed04b/pkg/signalmeow/storageservice.go)
takes `DeviceData.MasterKey` and derives the storage key with HMAC-SHA256 and
`Storage Service Encryption`. Copying source `storageKey` into `MasterKey` would derive
twice and fail decryption.

For an entropy pool, use the existing `DeriveSVRKey` path from provisioning and verify
against source-produced derivation fixtures. A legacy `pinMasterKey` is a master-key
candidate, not a derived storage key. When a stored derived key coexists with either,
compare derivations instead of assuming they refer to the same storage state. Derived-key-only
linked accounts require a fork/API extension accepting the derived key, or explicit rejection
for the initial supported subset. No master key can be recovered by reversing the HMAC.
Never invent a storage key or silently claim contact/group sync is available.

## Stage 1 protocol compatibility corpus

The standalone [Java harness](../scripts/account-import-java/) produces synthetic
protocol records using `org.signal:libsignal-client:0.103.0`. Its source layout follows
signal-cli v0.14.9: registry 2, account JSON 11 and SQLite 31. The committed
[provenance](../internal/signal/accountimport/testdata/java-0.103.0/provenance.json)
records artifact/JNI checksums, the actual loaded Linux amd64 testing JNI, generator
sources, source codecs and Java 25.0.2/Maven 3.9.11. It includes a genuine incoming
sender-key row with the source's 16-byte UUID BLOB encoding. No account loader or
Signal server participates in generation.

The compatibility runner restores whole records into real target account databases,
closes them between steps and switches native v0.102.2 and pure-Go backends in both
starting orders. Java continuation retains an independently seeded original Java
peer, reopening its evolving state between replies. A separate branch exercises a
Go-cloned peer; its generated state never replaces the retained Java peer.

| Coverage                         | States / operations                                                                                                                                                                                                                                                                            |
| -------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Corpus exchange acceptance       | ACI and PNI; Java version-4 sessions with initial V1 negotiation and established V1 PQ; ordinary and last-resort queued prekey messages; current, archived-only and archived-plus-current states with skipped messages; replies and fresh prekey initiation toward retained Java receiver keys |
| Group exchange acceptance        | Incoming sender keys and skipped group messages; fresh Go outbound key/distribution; Java distribution processing, group decryption and reply                                                                                                                                                  |
| Persistence / failure assertions | Original record hashes and timestamps after reopening; optional EC ID 0; EC/ordinary-Kyber consumption and signed/last-resort retention; tamper/replay preservation; equal UUID values in distinct typed ACI/PNI store keys                                                                    |
| Inspection unit tests only       | Encoded session version 3; empty V0, initial V1 allowing minimum V0 and downgraded V0 retaining a chain; expired pending sender usability; historical archived remote-identity differences; malformed, unknown, duplicate and bounded encodings                                                |

The inspection-only cases do not establish Java exchange coverage for those encodings.
All protocol material and credentials are invented. Format markers and passing
deserialization do not admit arbitrary source accounts. Stage 1 passed on 2026-10-11
with immutable `libsignal-go v0.7.1-cw.6` and `mautrix-signal v0.2609.0-purego.30`:
12 scenarios in both starting orders, 264 actions and 48 real Java continuation legs.
The [verification record](account-import-compatibility.md) includes release commits,
checksums, command results and the complete ledger. The importer and live acceptance
remain separate stages.

See [the developer guide](dev.md#offline-java-account-import-compatibility) for the
Java-free backend runner, required Java continuation mode and regeneration procedure.

## Implementation and acceptance gates

The map and Stage 1 protocol prerequisite are complete; functional importer support
remains contingent on the remaining Phase 16 PLAN items. The initial supported subset must refuse inputs that hit an unresolved conversion
above. In particular, allocator persistence, storage-key handling, privacy/account-record
construction and conflict protection, account-level protocol validation and safe filesystem
publication must be resolved before an importer can be called supported.

The implementation should validate and convert offline into a private staging directory,
then publish only after every required record passes. Keep secrets in 0700 directories
and 0600 files; never print credentials, key bytes, message bodies or raw failing records.
Bound input sizes and row counts, reject duplicate/inconsistent protocol keys and check
SQLite integrity. Treat a malformed required record as an account-level failure. A partial
import must not be selectable, overwrite an existing account or trigger network activity.

Acceptance needs sanitized **synthetic** fixtures from the supported Java schema with
genuine protocol records, including both ACI/PNI stores, both identity trust copies,
archived/skipped session keys, signed/ordinary/last-resort Kyber prekeys, depleted next-ID
counters, sender keys, blocked contacts and local privacy conflicts, explicit false receipt settings and both storage
key forms. Compare the converted values after reopening the destination on both backends.
Add unknown-version, malformed-key/record, path/symlink, source-WAL, collisions, concurrent
registry updates, interrupted-publication and retry tests; verify the source stays byte-identical.

Finally, use a dedicated stopped signal-cli linked account to confirm credentials, queued
and new incoming messages, direct/group sends, receipts and backend switching without
re-linking, with phone-side observation. Local source/schema inspection and `just check`
do not satisfy that live criterion. Phase 16 remains open until it passes.
