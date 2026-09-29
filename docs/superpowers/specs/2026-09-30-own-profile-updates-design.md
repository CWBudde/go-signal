# Own-profile text updates

Date: 2026-09-30

Status: written spec approved on 2026-09-30; implementation plan awaiting review.

## Intent and scope

Users of a linked account need to inspect and change their own Signal profile from
the CLI. This is the next implementation item in `PLAN.md` under “Later / on
demand”, following the merged group-management commands. The user approved the
restricted v1 text-update approach and requested useful parallel subagent work.

Add `profile show` and `profile update` with `--given-name`, `--family-name`,
`--about` and `--about-emoji`. Omitted flags preserve the existing field; an
explicit empty value clears it. Require at least one update flag. Preserve
avatar, payment address, phone-number-sharing preference and badges. Reuse the
existing profile key. Avatar upload/removal, privacy/payment changes, profile-key
rotation, profiles v2, remote storage writing and MCP tools are deferred.

Both commands fetch the server profile for the selected account. Update performs
at most one profile write. An unchanged request is a no-op. The implementation
must work with the cgo and `libsignal_go` backends.

V1 has no compare-and-swap against another device. Preservation is relative to
the fetched snapshot; simultaneous phone edits or key rotation can race this
operation. Help and user documentation must state this limitation and recommend
avoiding concurrent profile edits. This scope does not claim conflict protection.

## Architecture and public contract

Follow the existing `cmd` → `internal/app` → `internal/signal` layering, rendering
through `internal/output`. Signalmeow, UUID and backend crypto types stay inside
the facade. Do not bump dependencies or change the database schema.

Add facade types:

```go
type Profile struct {
    ACI        string
    GivenName  string
    FamilyName string
    About      string
    AboutEmoji string
    AvatarPath string
}

type ProfileUpdate struct {
    GivenName  *string
    FamilyName *string
    About      *string
    AboutEmoji *string
}

type ProfileUpdateResult struct {
    Profile  Profile
    Changed  bool
    Accepted bool
    Verified bool
}
```

`OwnProfile(context.Context) (Profile, error)` and
`UpdateOwnProfile(context.Context, ProfileUpdate) (ProfileUpdateResult, error)`
extend `Client`. Nil pointers preserve fields. Both methods require a connection;
send-only mode is sufficient. App use cases `ProfileShow` and `ProfileUpdate`
connect through the existing `connectSendOnly` helper. Validate supplied text
before opening the CLI account or connecting the app. A given name alone cannot
exceed 257 UTF-8 bytes; a nonempty family name cannot exceed 256 because its
encoding adds a delimiter. When both name fields are supplied, validate their
combined encoded size immediately. With an omitted component, also validate the
merged name after fetching that component.

`Changed` and `Accepted` become true only after confirmed acceptance of a write.
`Verified` means a subsequent raw read matched the submitted profile and preserved
metadata. A no-op returns the fetched profile with `Changed=false`,
`Accepted=false`, `Verified=true`. An unsuccessful or uncertain submission returns
no purported final profile. Accepted follow-up failures return an accepted result
alongside an error; before successful verification that result contains only the
account ACI, with `Verified=false`.

Use sentinel errors for invalid text/update input, missing or stale profile key,
unsupported profiles v2, malformed profile data, rejected profile writes and
verification failure. Wrap causes with `%w`. Keep unlink, lock, connection-loss,
closed-client and cancellation behavior consistent with existing facade methods.

## Raw reads and freshness

The pinned signalmeow reader flattens the given/family NUL separator and omits
payment/privacy/badge fields. It is unsuitable as an update source. Add a private
raw response type retaining encrypted fields, avatar path, badge metadata,
capabilities, account identifier and the profile credential response.

Fetch an authenticated versioned self profile, deriving the requested version
from the selected account's existing profile key. Include an expiring-profile-key
credential request using signalmeow's existing request helper. A nonempty,
497-byte credential is required as evidence that the requested version is
current: the server omits it for an older version. Reject a missing credential,
missing encrypted name, mismatched account identifier, malformed ciphertext or
unsupported v2 capability. Confirm that the local key still matches the snapshot
after constructing the request, after every raw read (including show and no-op),
and immediately before writing. This catches observed local key changes without
claiming protection against cross-device races.

The server may hide payment data for an older version even while returning that
version's name. Never treat such a response as an authoritative empty payment
address. Once current-version evidence is established, nullable optional fields
represent absence; retain the fetched null/empty representation when preparing
the write. Require a capabilities object and validate its values. Unknown
capabilities do not themselves cause a failure.

Show also uses this fresh raw read so it preserves the name split and refuses an
outdated local key. Facade-owned raw HTTP response bodies have a fixed maximum
of 1 MiB. The dependency's forced cache-refresh reader has no equivalent bound.
For facade responses, reject oversized, invalid or trailing JSON and invalid
base64; do not print raw bodies, keys, credentials or authentication material in
errors.

## Text and crypto rules

Reject invalid UTF-8 and embedded NUL in supplied text. Preserve spaces and other
valid text exactly. Do not require that about-emoji is one grapheme: its protocol
constraint is encoded byte length. Empty given and family names are permitted;
the resulting name is still an encrypted padded value.

The internal name is the given name followed by a NUL and the family name when
the latter is nonempty. Decrypt by removing only trailing padding, retaining the
interior delimiter. Reject multiple interior delimiters. A family-only name
retains its leading delimiter. Validate the merged name, including the delimiter,
against the maximum UTF-8 byte length.

| Field         | Plaintext padding sizes in bytes | Maximum input bytes |
| ------------- | -------------------------------- | ------------------- |
| Combined name | 53, 257                          | 257                 |
| About         | 128, 254, 512                    | 512                 |
| About emoji   | 32                               | 32                  |

Choose the smallest fitting padding size for changed fields. Empty about/emoji
values use an empty ciphertext to clear the field. Nonempty values use
AES-256-GCM, a fresh cryptographically random 12-byte nonce, no additional data
and a 16-byte tag, serialized as nonce followed by ciphertext and tag. Reuse
standard crypto primitives; do not add custom cryptography. Untouched ciphertext
is copied byte for byte, including name when neither name flag is supplied.

## Single write and preservation

Build one authenticated `PUT /v1/profile` request. Derive the commitment and
version with the existing libsignal facade methods. The version method already
returns hex text; do not encode it again. Include the merged encrypted text and
the original payment/privacy ciphertext. Set `avatar=true` and `sameAvatar=true`.
Omit `badgeIds` entirely, because an empty list changes badge visibility.

Use a facade-owned HTTP request helper calling the configured Signal HTTP client
once. Set the existing Basic-auth, JSON, user-agent and Signal-agent headers. Do
not follow mutation redirects: use a copied HTTP client with a redirect policy,
preserving its configured transport and timeout without mutating the shared client.
Do not use the websocket request helper, which automatically retries unanswered
requests, or the HTTP wrapper's unsynchronized request counter. Tests replace the
existing HTTP transport serially.

GET authentication failures keep existing unlink semantics. PUT HTTP 401 means
the device is unlinked; PUT 403 is a rejected write, because it can mean payments
are forbidden, and must not mark the device unlinked. HTTP 412 is unsupported v2,
including a migration after the initial read. Other non-success responses and
transport failures are returned without retrying, dropping payment data or
changing fields to make the request acceptable. An unanswered request or server
failure can leave the outcome uncertain and instructs the user to inspect before
retrying.

Record acceptance as soon as a successful HTTP status is received, before reading
the response body. The unchanged-avatar response can be empty; do not require an
upload-form document. A later body error must retain acceptance.

## Verification, local state and device notification

After confirmed acceptance, independently attempt the required follow-up stages
once each, retaining all errors:

1. Fetch the raw current self profile again. Verify changed text, untouched text
   ciphertext, payment/privacy ciphertext, avatar path and badge metadata against
   the prepared snapshot. Compare parsed badge metadata semantically while retaining
   list order. A mismatch is an accepted verification failure and never triggers a
   second PUT.
2. After successful raw verification, refresh signalmeow's in-memory profile through
   its forced-read API (`refreshAfter=0`), then persist the verified display profile
   through `RecipientStore.LoadAndUpdateRecipient`. Preserve the key and all unrelated
   recipient fields. Use the raw read for the facade result and exact name split.
   A forced-cache refresh failure is reported even if persistence succeeds. The
   dependency can leave its previous in-memory profile appearing fresh after this
   failure; only the raw result and verified persisted record are authoritative.
   Test failure reporting and that subsequent facade reads bypass that cache. Do
   not replace or modify signalmeow's private cache maps unsafely.
3. Send a `FetchLatest.LOCAL_PROFILE` sync to the account's other devices, following
   the Java reference. Attempt notification even when verification or persistence
   failed after acceptance. Check `WasSuccessful`; a failed self-sync may lack a
   populated backend error, so provide a concrete fallback error.

The account lock protects this CLI process from another local command. Register
the operation with the existing close/wait lifecycle and serialize profile updates
within a client. Every stage respects context cancellation; no background task
outlives the operation. Do not automatically create new uncancelled requests to
complete failed stages.

Signalmeow currently ignores incoming LOCAL_PROFILE notices and has no remote
storage writer. This feature sends the refresh notice to other devices and updates
its local recipient cache; it does not claim remote storage reconciliation. Fresh
raw reads remain authoritative for these commands. Clearing a name may expose a
stale name elsewhere if a later storage sync fills an empty cached display name;
document this backend limitation and keep it distinct from the server profile.

## CLI and output

Commands take no positional arguments. Capture Cobra `Flags().Changed()` so omitted
and explicitly empty flags differ. Reject no-flag updates before opening an account.
Use the existing client opener, close behavior and printer factory.

Plain show prints the ACI, given name, family name, about, about emoji and avatar
path with clear labels and escaped control characters consistent with existing
renderers. Plain update prints `Updated profile` or `Profile unchanged` followed by
the profile. Do not display ciphertext, credential or key material.

JSON show has `{version, profile}`. The profile object has `aci`, `givenName`,
`familyName`, `about`, `aboutEmoji` and optional `avatarPath`; empty text fields
remain present. JSON update adds `changed`, `accepted` and `verified`. Use the
existing schema version, since this is additive. On error, stdout remains empty
as in the existing mutation commands. Confirmed acceptance or uncertainty is made
explicit in stderr; the typed app result remains available to future callers.

## Verification and acceptance

Write tests first, with external test packages and narrow export-test adapters.
Use serial HTTP-transport tests and injected orchestration functions for write,
read, cache/persist and notification failure paths. The fake stores profiles by
selected account and mirrors validation, no-op, locking and accepted-error behavior.

Offline acceptance covers:

- Omitted versus explicit-empty fields; multiword, family-only and non-ASCII names;
  padding boundaries, malformed UTF-8, NUL and tampered/truncated ciphertext.
- Current-version credential gating and observed local key changes; missing keys,
  malformed responses, v2 refusal and no write after preflight failure.
- Byte-exact omitted-field preservation, unchanged avatar and omitted badgeIds;
  no-op behavior and one PUT on transport, response, rejection and follow-up errors.
- PUT 403 versus 401, acceptance surviving response-read errors, verification
  mismatch, cache/persistence failure and failed sync with no backend error.
- Account selection, lock contention, closed/unlinked clients, cancellation and
  preservation of unrelated recipient metadata.
- App failure propagation and plain/JSON command goldens, including explicit clears,
  unchanged values and empty stdout on errors.

Run `just check` and `just check-purego` after integration. User docs and
`docs/json.md` describe the final output and limits. Update `PLAN.md` honestly:
implementation, offline verification and documentation can close separately;
live verification remains unchecked until actually performed.

Live acceptance uses a separately enabled disposable account on both backends,
preserves and restores the original profile, checks phone/device propagation and
avatar/payment/privacy/badge preservation, and verifies restoration from the server.
No live credentials are currently configured. Do not run profile mutations against
an arbitrary personal account.

## Parallel work and shipping

The controller owns the public contract, planning records and Git history. Finish
shared facade types, validation and fake behavior before parallel implementation.
Then assign real backend and backend tests to one agent and application use cases
and tests to another. They work in disjoint packages against the completed shared
contract. CLI/output work follows the app result contract. Review and run integrated
checks before creating a PR targeting main. Preserve the pre-existing dirty
`reference/signal-cli` submodule and never stage it.

## Evidence

- Pinned dependency: `github.com/cwbudde/mautrix-signal v0.2609.0-purego.9`,
  `pkg/signalmeow/profile.go`, `web/signalwebsocket.go`, `web/web.go`,
  `store/recipient_store.go`, `sending.go`, `receiving.go` and the cgo/pure-Go
  `pkg/libsignalgo/profilekey*` implementations.
- Read-only Java reference: `Profile.java`, `ProfileHelper.java`,
  `ManagerImpl.updateProfile` and `SyncHelper.sendSyncFetchProfileMessage`.
- [Signal server request fields](https://raw.githubusercontent.com/signalapp/Signal-Server/main/service/src/main/java/org/whispersystems/textsecuregcm/entities/CreateProfileRequest.java).
- [Signal server controller: legacy writes, credential freshness and payment visibility](https://raw.githubusercontent.com/signalapp/Signal-Server/main/service/src/main/java/org/whispersystems/textsecuregcm/controllers/ProfileController.java).
- [Signal server avatar and badge behavior](https://raw.githubusercontent.com/signalapp/Signal-Server/main/service/src/main/java/org/whispersystems/textsecuregcm/util/ProfileHelper.java).
- [Signal Android profile encryption](https://raw.githubusercontent.com/signalapp/Signal-Android/main/lib/libsignal-service/src/main/java/org/whispersystems/signalservice/api/crypto/ProfileCipher.java).
- The pinned `protobuf/org/signal/chat/profile.proto` defines v2 expected-version
  and expected-data-hash protection; no corresponding high-level writer exists.
