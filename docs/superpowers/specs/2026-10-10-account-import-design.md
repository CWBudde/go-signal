# Offline signal-cli account import

Date: 2026-10-10. Baseline: `37b7d60`, fork `v0.2609.0-purego.29`.
Status: design and Stage 1 implementation plan approved by the user on 2026-10-10/11.
Stage 1 completed on 2026-10-11: [release verification and ledger](../../account-import-compatibility.md).
Stages 2a/2b are the next prerequisites.
The [format mapping](../../account-import.md) is the source contract; this document
specifies the runtime changes needed to implement it. The Stage 1 record establishes synthetic offline Java-record interoperability for its
tested matrix; production conversion and live account acceptance remain open.

## Intent and approach

Import a stopped signal-cli linked account so that go-signal can continue its existing
sessions without relinking, changing the source, or replacing any destination account.
The import itself is offline. It preserves credentials, local ACI/PNI identities,
retained protocol records, allocator positions, supported contacts/groups and privacy
decisions. Connected operations happen later through the ordinary client.

Use a staged conversion with explicit compatibility gates and recoverable publication.
Two alternatives were considered: accepting only empty protocol stores would exclude
the accounts this feature is intended to migrate; copying rows through existing APIs
would retain unsafe allocator, privacy and publication behavior. A single broad patch
would also couple several independently testable prerequisites. Implement the stages
below separately, keeping the command unavailable until they are integrated.

## Initial supported boundary

Target the signal-cli v0.14.9 format: registry 2, account JSON 11 and SQLite schema 31.
Check schema shape, types and records as well as markers. Require registered LIVE
linked devices (`deviceId > 1`), valid nonzero ACI/PNI, both identity pairs and registration
IDs, E.164 number, password and a valid own profile key. Credentials are locally validated;
their server validity is unknown until a later connected command.

Initial publication is supported on Unix systems with working advisory locks,
same-filesystem atomic no-replace directory rename and directory sync. Linux
`renameat2(RENAME_NOREPLACE)` is the first publication implementation; other platforms
remain refused until an equivalent primitive is implemented and tested. Import must refuse platforms without
those primitives; `lock_other.go` currently always succeeds and cannot provide this
guarantee. Ordinary committed account data remains backend-compatible. Older go-signal
processes do not participate in the new registry protocol: stop them before importing.

Reject unsupported versions, namespace/address conflicts, malformed required records,
inconsistent active key references, detected allocator rollover, primary/staging accounts,
legacy groups, blocked groups, groups with disabled source profile sharing (no target
group setting), blocked PNI-only contacts without an addressable ACI, phone-sharing
`CONTACTS`, and nondefault mute/archive/
hidden/story settings that have no target representation. Require an empty pending
message cache, no pending failed-message retry, and empty send-log tables for the initial
subset. This conservative send-log restriction also excludes retained resend history;
it is a documented limitation, not evidence that every retained log entry is pending.
Never run source cleanup to make an account pass.

Avatars, attachments, sticker images, source history, storage manifests/IDs, CDSI tokens
and endorsements are not copied as runtime caches. Report their exclusion using fixed
category names. Inbox, polls and pins start empty. Preserve source storage metadata only
as private validation/provenance where needed; do not install it as target sync progress.

## Command and package boundary

Add `account import --source-dir <snapshot>` with optional
`--source-account <E.164-or-ACI>`. The source directory is the settings root containing
`data/accounts.json`, not a single account file. Omission of the selector is allowed only
when exactly one eligible registry entry exists. Never choose the first of several entries.
The destination is the global `--data-dir`. Reject a supplied global `--account` for this
command with guidance to use `--source-account`; destination selection is inapplicable.
There is no overwrite, relink, discard-unsupported-data or network-validation option.

Keep orchestration in a standalone typed `internal/app` use case with an injected importer
interface. It must not open a selected `signal.Client`, extend the live-client interface,
or print. The concrete implementation belongs in `internal/signal/accountimport`, where
dependency protocol types remain inside the facade tree. Source readers and conversion
live there; shared filesystem publication belongs to `internal/store`. `cmd` parses and
calls the use case; `internal/output` renders the typed result. Add a command option for
injecting the importer in tests, following `WithClientFactory`. Untagged no-cgo builds
return the existing backend-required error without touching source or destination.

The result contains ordinary account identifiers, imported record counts, fixed exclusion
categories and source format markers. JSON adds an `accountImport` document at schema
version 1. Never expose passwords, keys, source record bytes or message content. Keep
`LinkedAt` unknown: the source save timestamp is not the linking time. An unavailable
device name stays unknown. A successful result means local publication completed;
it does not mean server validation, phone synchronization or live delivery passed.

## Source snapshot and conversion

The caller supplies a complete copy made while signal-cli is stopped and keeps the writer
stopped afterward. Import cannot prove that an unrelated Java process is quiescent;
neither Unix flock nor repeated hashes implement Java's account-file locking protocol.
Explain this requirement in command help and the guide, without a confirmation flag.

Open the supplied tree read-only using directory-rooted filesystem operations. Reject
absolute/traversing registry paths, symlinks in required paths, special files and source/
destination overlap, including physical aliases. Resolve identities across registry,
JSON and SQL before creating any final account directory. Copy only required regular
files to a private working directory, including an existing WAL and SHM. Record stable
file identities, lengths, timestamps and streaming hashes before/after copying and again
before publication; detected changes abort. These checks detect changes, not prove
snapshot consistency. SQLite recovery and any sidecar writes occur only on the private copy.

Bound registry and account JSON to 4 MiB each, the copied SQL database plus WAL/SHM to
4 GiB total, each protocol blob to 1 MiB, total protocol rows to 1,000,000, recipient rows
to 100,000 and group rows to 10,000. Stream copying and row conversion; reject exceeded
limits before publication with the affected category. These are initial support limits,
not Signal protocol limits. Check SQLite integrity and exact expected schema/field types.
Reject duplicate JSON keys, duplicate protocol keys and ambiguous address mappings.

Create a fully migrated target database privately, then convert inside one transaction.
Use both local service namespaces and exact remote typed IDs. Reconstruct EC/signed
records from source components; deserialize full Kyber, session and sender-key records.
Validate public/private consistency, signatures, IDs, field-specific timestamp units
(signed/Kyber milliseconds, pending-session creation seconds), registration
IDs and session identity bindings, including archived states. Historical archived remote
identities may differ from the current identity-store key; do not reject that valid history
by comparing every archived remote key to the current key. Preserve archived sessions
and skipped keys; absence of a usable current sender chain is not structural corruption.
If the backend cannot inspect a required record invariant, refuse rather than bypass it.

Import both trust copies atomically. Unknown identity history stays unknown; do not emit
change events or enqueue verification outbox entries. Seed known ACI trust decisions as
protected local observations in `gosignal_storage_identities`; PNI remains independent.
Import retained incoming sender chains, but no outbound sharing authorization. The first
outbound group send must generate a fresh key under a fresh distribution ID; do not reuse
an imported outbound record with a new ID. Derive and verify group IDs from master keys.

## Durable key allocation

The source uses three independent counters per local service ID: EC, signed and Kyber.
Ordinary and last-resort Kyber keys share the Kyber counter. Its modulo is `0xffffff`,
so generated source IDs are `0..0xfffffe`; this is source policy, not a claim about the
entire Rust `uint32` range. Preserve all three next IDs and the active signed/last-resort IDs.
For the initial subset, refuse counters at/below retained maxima and inconsistent active
references. An empty retained store still uses the persisted next ID, never `MAX+1`.

Add fork-owned allocator metadata scoped by account, local typed service ID and key kind.
Batch generation reserves a range atomically before producing/storing keys; reservation
survives deletion, restart and interrupted generation. Failed reservations consume no IDs;
failed generation may leave gaps but must not reuse reserved IDs. Refuse exhaustion before
wrapping. The importer seeds this metadata with the records in its conversion transaction.
Retain active references for future signed/last-resort rotation; current ordinary refill
must not overwrite them. Existing accounts need a one-time migration seeded from their
retained maxima; already lost consumption history cannot be reconstructed. New
provisioning must initialize allocator state alongside its fixed-ID signed/last-resort
keys. Count ordinary Kyber keys separately from retained last-resort keys. Merely adding
unused next-ID columns is insufficient.

Tests cover sparse/depleted stores, local ACI/PNI isolation, parallel reservations,
restart, failure after reservation, ordinary Kyber consumption, retained last-resort
keys and exhaustion. Source last-resort replay history is unavailable; do not fabricate it.

## Storage keys and privacy reconciliation

Add a distinct persisted fork device field for the derived storage-service key.
Keep `MasterKey` semantics intact. A common resolver chooses a valid derived key or derives
from a valid master/entropy pool, and checks agreement when multiple forms are present.
Never HMAC an already-derived source `storageKey` again. Route explicit/background fetch,
connect key-request decisions and account sync through the resolver. Support authenticated
legacy derived-key and entropy-pool sync updates consistently, including restart and
replacement of superseded forms. Reject disagreement during import; never infer a master
key from a derived key. Own profile/backup keys require their separate length/type checks.

Store durable account-local privacy guards with entity kind/typed ID, field, local
presence/value, protection flag, raw remote presence/value and manifest observation.
Guard imported blocking, profile sharing, supported receipt/typing/link-preview/delivery/
phone-privacy settings and own profile key. Overlay explicit local configuration onto
the validated source AccountRecord using Java's construction rules. Preserve explicit
false and distinguish absent settings; refuse settings that cannot be represented
faithfully. Retain source SQL presence as provenance; Java's effective defaults are true
for typing/read receipts/sealed-sender indicators/link previews, false for unlisted number,
and UNKNOWN for absent phone-sharing configuration. Do not infer those values by treating
an absent SQL row as false. JSON and self-recipient profile keys must agree.

Install optional fork pre-write projection hooks before receive workers start. Storage
application retains the untouched authenticated snapshot for freshness/identity checks
and computes a separate effective projection for SQL, events and postcommit account
publication. Use a private clone, not mutation of the raw update. Profile-sharing assignment
must support both true and false for this path; the current one-way whitelist update is
insufficient. Guards, identities, own key, account record and observation highwater commit
atomically. Preserve existing transaction-safe block-cache behavior; failed commit must
publish neither projected settings nor events.

Protected fields survive conflicting/older/repeated snapshots without a timeout. Release
one field only when a complete strictly newer raw authenticated snapshot contains its
matching semantic value. Proto3 scalar booleans omitted from a present complete record
mean false; SQL presence, protobuf scalar defaults and a missing remote record are distinct.
A missing record/contact, unknown enum, absent required key, or equality introduced by
the overlay cannot release protection. Keep unrelated records applicable. Later explicit local
commands rearm affected guards atomically with their writes; import and reconciliation
do not create outgoing sync work. A local command's normal explicit synchronization
behavior is retained. A later changed remote value may apply after genuine alignment.

Recipient pre-write hooks must also cover versionless message-request acceptance, own
profile keys from sent transcripts/group updates, and profile refresh. Such arrivals may
process unrelated content but cannot replace protected values or clear guards. Parent
story-audience filtering and outgoing blocked-list construction must use effective
protected values, including imported contacts absent from raw remote storage. There are
currently no fork blocked-list/configuration handlers; any future handler must use the
same policy. Existing temporary block overrides remain separate from import guards.

Incoming block checks must resolve the exact typed sender service ID to its validated
recipient mapping. The current fork treats `theirServiceID.UUID` as an ACI even when the
sender is a PNI. Add a typed lookup/cache path before accepting imported blocked contacts
that carry both ACI and PNI; never equate identities merely because their UUIDs match.
PNI-only blocked contacts remain refused until outgoing complete-block-list and facade
selection support them as well. Test ACI/PNI mappings and equal UUIDs from unrelated
recipients; preserve shared contact blocking only for a validated recipient association.

## Publication and recovery

Add a destination-wide registry lock used by account readers, all registry mutations,
account-directory creation and import publication/recovery. Keep the account flock for
connected activity. Define nonblocking lock acquisition and a consistent order; do not
wait for an account lock while holding the registry lock. Existing operations that already
hold an account lock may briefly acquire the registry lock; a contending operation fails
or retries without holding the registry lock. There must be no read/modify/write lost update.
Refactor private unlocked helpers so recovery does not recursively acquire this lock.

Account selection, registered-directory lookup, account-lock acquisition and identity
revalidation must be one operation under this protocol. For connected/mutating use,
acquire the existing account flock nonblockingly under the registry lock, revalidate
the selected registry entry and directory/lock-file generation, then open/migrate/load
device state while retaining the account flock. Never create a missing directory or
database while opening a registered account; that is corruption/removal, not a new account.
Creation is reserved for explicit linking or private staging. Refactor `device`, Connect
and Unlink accordingly; individual locks around the current helpers are insufficient.

Preserve read-only inspection of an account used by another process through an explicitly
noncreating, nonmigrating read-only snapshot whose existing directory/database generation
is pinned under the registry protocol. It must not supply cached device state to a later
Connect or mutation: reacquire the account flock, revalidate selection/generation and
load fresh state. A stale selection after unlink/reimport must fail or reselect, never
recreate an orphan or connect an obsolete lock inode. Cover this with subprocess barriers.

Build under a private `.imports/<random-token>/` directory on the destination filesystem,
with an owner lock. Allocate this directory through the registry protocol, then release
the registry lock during conversion. Hold the owner lock for the entire attempt; unrelated
readers must not remove an active stage. Directories are 0700 and files 0600. Close and
checkpoint the private target database, ensure its contents and directories are synced,
and reopen/validate it before the short final publication section. Never rename an open
SQLite connection and continue using a DSN pointing at the former staging path.

Under the registry lock, recover prior interrupted attempts, reread the registry and
recheck collision/source stability. Any existing ACI entry or filesystem entry at the
final path is a collision, including an orphan, symlink or unlinked account. Reject another
entry for the same source number/PNI as ambiguous rather than importing a second clone.
Do not call replacing `PutAccount` or creating `Lock(aci)` as an import collision guard.

Persist and sync a publication intent containing the exact ordinary registry entry,
random ownership token and staged database digest. Store a matching private ownership
marker with the staged account. After intent, cancellation cannot report a simple clean
abort: finish publication or return a recovery-required error. Rename the account into
place without replacing a filesystem entry, sync the destination parent, atomically
append the registry entry and sync its parent, then durably retire the intent. Publication
holds the registry lock throughout; a fully synced intent is the commitment to roll forward.

Every registry read/mutation and account creation checks recovery before proceeding.
Recovery acquires the abandoned attempt's owner lock and rolls forward only when the
intent, ownership marker, account identifiers and database digest agree. Resume from
either staged or renamed location; recognize an already committed exact entry without
adding it twice. Do not hash active account databases after an import is already committed:
normal ratchet progress is expected. Conflicting/missing evidence returns an actionable
recovery-required error; never delete or adopt an arbitrary orphan. Uncommitted abandoned
stages can be removed only with ownership proof and no publication intent. A fresh import
of an already published account remains a collision, not permission to overwrite it.

Current and future client selection cannot observe an incomplete imported account.
Crash tests must exercise actual process termination at durable boundaries, not only
returned errors. Directory-sync and rename failures require explicit recovery outcomes.

## Implementation stages and acceptance

Each stage needs its own reviewed implementation plan and tests before dependent work:

| Order | Deliverable                                                       | Evidence required                                                                                                                                                     |
| ----- | ----------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 1     | Source compatibility corpus and backend-neutral record inspection | Genuine Java 0.103.0 fixtures; current/archived/skipped and prekey/group exchanges continue on both backends                                                          |
| 2a    | Fork allocator reservation                                        | Depleted/restart/failure/exhaustion cases and separate ordinary/last-resort counts                                                                                    |
| 2b    | Fork storage-key lifecycle                                        | Java derivation vectors; derived-only and key-update sync paths                                                                                                       |
| 3     | Durable privacy guards, typed blocking and effective projection   | First conflicting fetch, restart, missing/false values, alignment/later change, typed sender/cache isolation, bypass handlers, raw consumers and rollback/cache cases |
| 4     | Shared registry/open protocol and recoverable publication         | Concurrent import/link/unlink/logout updates, stale selection versus unlink/reimport, orphan/collision refusal and subprocess crashes at each publication boundary    |
| 5     | Offline converter, app/CLI/output and user guide                  | Exact reopened values, source byte identity including WAL, invalid input/size/path/cancellation refusal, golden output and no network calls                           |
| 6     | Dedicated live acceptance                                         | Phone-observed queued/new receive, direct/group sends, receipts and backend switching without relinking                                                               |

Stages 1–4 provide separately testable prerequisites, not a completed importer. Stage 5
can complete the implementation checkbox only after those prerequisites pass. Stage 6
is required to close the fixture/live acceptance checkbox and Phase 16 itself. Keep
the acceptance criterion intact when a prerequisite or account session is unavailable.

Generate synthetic fixtures with a standalone Java harness outside the reference tree,
pinned to `org.signal:libsignal-client:0.103.0` with recorded artifact/JNI checksums.
Use source JSON/component/SQL serialization rules without invoking signal-cli's mutating
account loader. Match source ordinary-Kyber deletion and last-resort retention, rather
than assuming Java's generic in-memory test store has those semantics. Fixture accounts
are invented and have no real credentials. Commit fixture records as bounded encoded
data and schema/insert scripts; generated databases remain ignored. Java is needed only
for reproducible fixture generation/interoperability checks, not release builds or import.

Stage 1 established a private Java 25.0.2/Maven 3.9.11 toolchain with the verified
0.103.0 artifact and completed bidirectional Java/native v0.102.2/pure-Go exchanges,
including persisted backend switching. Later format expansion needs equivalent evidence;
precomputed inbound ciphertext or successful deserialization alone cannot prove outbound/
source compatibility. No live source account was used.

Run scoped race tests and pure-Go tests for each delivered stage, then `just fmt`,
`just check`, `just check-purego`, `just test-fork`, `just test-diff` and untagged no-cgo
tests for integrated code/fork changes. This design-only change requires `just check`
and source/API review. Live tests stay opt-in with a dedicated stopped source account,
phone and peer; no such session has been used for this design.

## Design evidence

- Registry replacement and missing global serialization: [accounts.go](../../../internal/store/accounts.go);
  account-directory creation before flock: [lock.go](../../../internal/store/lock.go);
  non-Unix no-op locks: [lock_other.go](../../../internal/store/lock_other.go).
- Source counter policy: [ServiceConfig.java](../../../reference/signal-cli/lib/src/main/java/org/asamk/signal/manager/config/ServiceConfig.java),
  [SignalAccount.java](../../../reference/signal-cli/lib/src/main/java/org/asamk/signal/manager/storage/SignalAccount.java).
- Existing identity protection: [migration 12](../../../internal/store/upgrades/12-storage-identity.sql),
  [reconciliation](../../../internal/signal/meow_identity_storage.go).
- Raw-storage consumers: [blocked lists](../../../internal/signal/meow_contacts.go),
  [story audiences](../../../internal/signal/meow_story_audiences.go).
- Pinned fork API evidence is recorded in the [format map](../../account-import.md):
  `keys.go`, `store/prekey_store.go`, `store/device.go`, `storageservice.go`,
  `receiving.go`, `contact.go` and `groups.go` were also inspected at `.29`.
