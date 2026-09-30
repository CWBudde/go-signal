# Own-profile Text Updates Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [x]`) syntax for tracking.

**Goal:** Add own-profile inspection and single-attempt v1 text updates that preserve omitted fields and report accepted follow-up failures accurately.

**Architecture:** Extend the Signal facade with typed profile data and updates. Read raw authenticated current-version profiles inside the facade, perform one REST write, verify the result and notify other devices. Shared types and fake behavior precede parallel backend/application work; CLI and rendering follow the application contract.

**Tech Stack:** Go, Cobra, existing Signal facade and signalmeow `v0.2609.0-purego.9`, standard AES-GCM, SQLite recipient store, existing plain/JSON printers.

**Spec:** [Approved design](../specs/2026-09-30-own-profile-updates-design.md).

**Status:** Implemented with independent reviews, `just check` and `just check-purego` passing. PR publication is pending; live verification remains open. Execution used parallel subagents where independent, as requested by the user.

## Global Constraints

- Commands: `profile show`; `profile update --given-name --family-name --about --about-emoji`; no positional arguments.
- Nil update fields preserve; explicit empty text clears; require at least one update flag.
- Reuse the existing profile key. Defer avatar upload/removal, privacy/payment changes, key rotation, v2, remote storage writing and MCP tools.
- Preserve avatar, payment address, phone-number-sharing preference and badges from the fetched snapshot. V1 does not prevent simultaneous edits by another device.
- Name padding: 53/257 bytes; about: 128/254/512; emoji: 32. Reject invalid UTF-8 and NUL. Nonempty family names have at most 256 input bytes.
- Require the current-version 497-byte credential, matching ACI and valid raw profile. Check the local key after request preparation, every raw read and before writing.
- Limit facade-owned HTTP response bodies to 1 MiB. The dependency's forced-cache reader has no equivalent bound.
- Send at most one profile PUT; refuse mutation redirects; no websocket mutation retry, payment-dropping fallback or automatic conflict retry.
- PUT 401 means unlink; PUT 403 rejects the write without unlinking; PUT 412 means unsupported v2.
- Mark acceptance before reading a successful write response body. Never present a proposed profile as a confirmed final profile.
- After acceptance, attempt verification, cache/persistence and notification stages with the existing context. Preserve accepted results and all follow-up errors.
- Keep stdout empty on errors. Plain successful updates say `Updated profile` or `Profile unchanged`. JSON schema version remains 1.
- All tests use external packages, with narrow `_test.go` facade adapters where necessary. Global HTTP transport tests run serially.
- Work on `feat/own-profile-updates`; preserve the existing dirty read-only `reference/signal-cli` submodule. Workers do not edit plans, commit, push or run repository-wide formatters.
- Controller runs `just fmt`, `just lint`, `just check` and `just check-purego` before shipping a PR to main. Live verification remains open without credentials.

## Review Focus

1. A redirect or nil response must not cause a second PUT or forward account credentials to another host (Task 2).
2. A stale local key must not turn a suppressed payment address into an empty replacement, including on show/no-op paths (Task 2).
3. A family-only or multiword name must retain its delimiter and spaces, including byte-boundary validation before account opening (Tasks 1 and 4).
4. An accepted HTTP response with an unreadable body or a failed self-sync lacking an error must retain acceptance and request inspection (Tasks 2–4).
5. A failed forced-cache refresh must not make a later facade read return stale display text; verified local persistence is still attempted (Task 2).

## Ownership and ordering

| Task                      | Owner                         | Allowed paths                                                                                                                                | Prerequisite  |
| ------------------------- | ----------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------- | ------------- |
| 1. Shared contract/fake   | Controller or one implementer | `internal/signal/profile*.go`, `client.go`, `signaltest/profile*.go`, `signaltest/fake.go`                                                   | Plan approval |
| 2. Real backend           | Backend agent                 | `internal/signal/meow_profile*.go`, `profile_wire*.go`, `profile_crypto*.go`, `profile_export_test.go`, necessary profile mutex in `meow.go` | Task 1        |
| 3. App use cases          | App agent                     | `internal/app/profile.go`, `profile_test.go`                                                                                                 | Task 1        |
| 4. CLI/output             | CLI agent                     | `cmd/profile*.go`, `cmd/root.go`, `cmd/testdata/profile*.golden`, `internal/output/profile*.go`                                              | Task 3        |
| 5. User docs/roadmap      | Controller                    | `README.md`, `docs/json.md`, `docs/dev.md`, `PLAN.md`, this plan/spec                                                                        | Tasks 2 and 4 |
| 6. Integrated review/ship | Controller and fresh reviewer | Scoped fixes coordinated with original owner                                                                                                 | Tasks 1–5     |

Tasks 2 and 3 are independent once Task 1 freezes the public API and fake. Task 4
can start when Task 3 finishes, while Task 2 continues. Never dispatch two writers
in the same Go package. Only the controller updates shared records and history.

Task 1 extends `Client` before the real methods exist, so cgo/libsignal_go builds
are temporarily incomplete until Task 2. Use the backend-free check below for
Task 1/app/CLI red-green cycles; do not add temporary product stubs. The controller
defers their commits until the integrated backend builds and required lint passes.

### Task 1: Shared profile contract, validation and account-aware fake

**Files:** Create `internal/signal/profile.go`, `profile_test.go`,
`internal/signal/signaltest/profile.go`, `profile_test.go`; modify
`internal/signal/client.go` and `internal/signal/signaltest/fake.go`.

**Interfaces:**

- Produces the exact `Profile`, `ProfileUpdate`, `ProfileUpdateResult` fields in the spec.
- Produces `(ProfileUpdate).Check() error` and `(ProfileUpdate).Apply(Profile) (Profile, bool, error)`, where the bool says text differs.
- Extends `Client` with `OwnProfile(context.Context) (Profile, error)` and `UpdateOwnProfile(context.Context, ProfileUpdate) (ProfileUpdateResult, error)`.
- Produces sentinels `ErrInvalidProfileUpdate`, `ErrProfileKeyUnavailable`, `ErrProfileKeyChanged`, `ErrProfileV2Unsupported`, `ErrInvalidProfile`, `ErrProfileRejected`, `ErrProfileVerification`.
- Fake fields: `Profiles map[string]signal.Profile`, `OwnProfileErr`, `UpdateProfileErr`, `ProfileFollowUpErr error`, `ProfileVerificationFails bool`.
- Fake `ProfileUpdates() []ProfileUpdateCall`; each call records `ACI string`, a cloned `Update signal.ProfileUpdate`, and `Result signal.ProfileUpdateResult`. Record each attempted mutation, including failures, for retry assertions. Protect state with the existing mutex.

- [x] Write `TestProfileUpdateCheck` for empty requests, invalid UTF-8/NUL, about 512/513 bytes, emoji 32/33, given 257/258, family 256/257 and fully supplied combined-name overflow. Representative assertions:

```go
value := strings.Repeat("a", 257)
if err := (signal.ProfileUpdate{FamilyName: &value}).Check(); !errors.Is(err, signal.ErrInvalidProfileUpdate) {
    t.Fatalf("family length error = %v", err)
}
value = ""
if err := (signal.ProfileUpdate{About: &value}).Check(); err != nil {
    t.Fatal(err)
}
```

- [x] Write `TestProfileUpdateApply`: omitted family survives changing a multiword given name; empty about clears; ACI/avatar remain identical; unchanged text returns false; a fetched omitted component can overflow the merged name; family-only encoding includes its leading delimiter.
- [x] Write fake tests `TestProfileFakeAccountIsolation`, `TestProfileFakeNoOpAndFailures`, `TestProfileFakeLifecycle`, and `TestProfileFakeCallCopies`. Assert selected-account changes only, one recorded attempt, no state change on rejection, persisted update plus accepted error on follow-up failure, ACI-only unverified result, lock/closed/unlinked/cancelled behavior, and snapshot independence from caller pointer mutation.
- [x] Run `CGO_ENABLED=0 go test -count=1 -run 'TestProfile' ./internal/signal ./internal/signal/signaltest`; record the failing assertions or undefined API before implementation.
- [x] Implement the contract, shared validation/merge and fake methods. Follow existing fake connection/unlink helpers; check cancellation before mutation. A missing seeded profile returns `ErrInvalidProfile`; do not fabricate a remote profile from contact display text.
- [x] Rerun the scoped command until all tests pass. The controller reviews this shared API before handing its files to dependent tasks; no worker mutates them during the parallel phase.

### Task 2: Raw-profile crypto, one-attempt transport and real backend

**Files:** Create `internal/signal/profile_crypto.go`, `profile_crypto_test.go`,
`profile_wire.go`, `profile_wire_test.go`, `meow_profile.go`,
`meow_profile_test.go`, `profile_export_test.go`; modify `meow.go` only to add the
per-client profile-update mutex. Backend implementation/adapters/tests use
`//go:build cgo || libsignal_go`; shared contract remains backend independent.

**Interfaces:**

- Consumes Task 1's types, checks, merge and sentinels; implements its two `Client` methods.
- Private `rawOwnProfile` retains encrypted `Name`, `About`, `AboutEmoji`, `PaymentAddress`, `PhoneNumberSharing`, `Credential []byte`, `Avatar string`, `Badges json.RawMessage`, `Capabilities map[string]bool`, and the response account identifier.
- Private `profileWriteRequest` uses exact server JSON properties: `commitment`, `version`, `name`, `about`, `aboutEmoji`, `paymentAddress`, `phoneNumberSharing`, `avatar`, `sameAvatar`; it has no `badgeIds` property. Use byte slices for nullable ciphertext and strings/bools for version/avatar flags.
- Private `encryptProfileText(key libsignalgo.ProfileKey, value string, sizes []int) ([]byte, error)` and `decryptProfileText(key libsignalgo.ProfileKey, encrypted []byte, sizes []int) (string, error)` use standard AES-GCM and the spec's padding sizes.
- Private `decodeOwnProfile(raw rawOwnProfile, ownACI string, key libsignalgo.ProfileKey) (Profile, error)` preserves name splitting, checks the credential, identifier, capabilities and ciphertext sizes, and decrypts text without flattening.
- Private `prepareOwnProfileUpdate(raw rawOwnProfile, current Profile, key libsignalgo.ProfileKey, update ProfileUpdate) (profileWriteRequest, Profile, bool, error)` preserves untouched wire values and computes changed text only.
- Private `profileHTTPRequest(ctx context.Context, device *mstore.DeviceData, method, path string, body []byte) ([]byte, bool, error)` returns body, confirmed-write acceptance and error. Acceptance is false for GET.
- Private `profileUpdateHooks` has `fetch func(context.Context) (rawOwnProfile, Profile, error)`, `checkKey func(context.Context) error`, `write func(context.Context, profileWriteRequest) (bool, error)`, `refresh func(context.Context) error`, `persist func(context.Context, Profile) error`, `notify func(context.Context) error`.
- Private `updateOwnProfileOnce(ctx context.Context, ownACI string, key libsignalgo.ProfileKey, update ProfileUpdate, hooks profileUpdateHooks) (ProfileUpdateResult, error)` orchestrates the spec stages. Export aliases/wrappers only from `profile_export_test.go` for external tests.

- [x] Write `TestProfileCryptoRoundTripAndBoundaries` and `TestProfileCryptoInvalidCiphertext` using fixed test keys and independently decrypted values. Cover 53/54/257-byte names, 128/129/254/255/512-byte about, 32-byte emoji, multibyte UTF-8, family-only/multiword names, independent random nonces, tampering, truncation and invalid padded lengths. Assert ciphertext lengths include 28 bytes overhead and no data is returned on authentication failure.
- [x] Write `TestDecodeOwnProfilePreflight` and `TestPrepareOwnProfilePreservation`: missing/wrong-sized credential, wrong ACI, v2, missing/malformed capabilities, malformed JSON/base64, invalid/multiple delimiters and ciphertext sizes fail. Omitted name/about/emoji/payment/privacy stay byte-identical; avatar flags are true and the marshaled request has no `badgeIds`. Empty optional text clears; no-op does not encrypt or write.
- [x] Run `CGO_ENABLED=0 go test -tags libsignal_go -count=1 -run 'Test(ProfileCrypto|DecodeOwnProfile|PrepareOwnProfile)' ./internal/signal` and record the red result before implementation. Initial compilation can also fail because real facade methods are still absent.
- [x] Implement the crypto/wire functions. Never use flattened `types.Profile` to prepare a write. Their green run follows the real entry-point implementation below; do not add product stubs to make intermediate builds pass.
- [x] Write serial transport tests `TestProfileHTTPRequestOnce`, `TestProfileHTTPRequestAcceptance`, `TestProfileHTTPRequestLimits`, `TestProfileHTTPRequestRejectsRedirect`: check Basic auth/path/headers; one PUT on network/5xx errors; no second host/request on 301/302/307/308; 200 acceptance survives response-read error; GET success never marks acceptance; 1 MiB limit and trailing JSON refusal; 401/403/412 map correctly. Use existing `SetSignalTransport` and a copied HTTP client with redirect refusal, retaining transport/timeout.
- [x] Run `CGO_ENABLED=0 go test -tags libsignal_go -count=1 -run '^TestProfileHTTPRequest' ./internal/signal` and record the red result; missing real methods can still prevent compilation at this point.
- [x] Implement `profileHTTPRequest`. Do not call `web.SendHTTPRequest` or the websocket helper for the PUT. Its green run follows the real entry-point implementation below.
- [x] Write `TestUpdateOwnProfileOnce` with injected hooks. Count fetch/write/check/refresh/persist/notify calls. Cover initial fetch/check failures, post-read key changes on show/no-op, pre-write key change, no-op, rejected/uncertain PUT, accepted body error, verification read error/mismatch, independent cache/persistence failures, notification failure and cancellation. Core accepted-error assertions:

```go
if !result.Accepted || !result.Changed || result.Verified || result.Profile.ACI != ownACI || result.Profile.GivenName != "" {
    t.Fatalf("unverified accepted result = %+v", result)
}
if writes != 1 || notifications != 1 || !errors.Is(err, io.ErrUnexpectedEOF) {
    t.Fatalf("write/sync/error = %d/%d/%v", writes, notifications, err)
}
```

- [x] Write `TestOwnProfileFreshnessAndLifecycle`, `TestProfilePersistencePreservesRecipient`, and `TestProfileSyncFailureWithoutCause`. Use narrow test adapters/seeded SQLite to prove raw reads bypass stale display caches, stored unrelated contact data survives, rejected PUT 403 does not record unlink, PUT 401 does, and missing self-sync cause becomes an explicit error. The fake offline connection needs a signalmeow client initialized from the seeded device; do not dereference the nil client left by `ConnectOffline`.
- [x] Run `CGO_ENABLED=0 go test -tags libsignal_go -count=1 -run 'Test(Profile|DecodeOwnProfile|PrepareOwnProfile|UpdateOwnProfile|OwnProfile)' ./internal/signal` and record the red result before implementing orchestration.
- [x] Implement `updateOwnProfileOnce` and the two real facade methods. Derive the version from the key without double hex encoding; obtain the credential request through the backend helper and recheck the captured key after preparation/read and before submission. Refresh uses `RetrieveProfileByID(ctx,self,0)`; persist verified raw text even if refresh fails; notification is attempted after every confirmed acceptance, with all errors retained.
- [x] Rerun `CGO_ENABLED=0 go test -tags libsignal_go -count=1 -run 'Test(Profile|DecodeOwnProfile|PrepareOwnProfile|UpdateOwnProfile|OwnProfile)' ./internal/signal` until all crypto, wire, transport, orchestration and lifecycle tests pass without stubs.
- [x] Run `CGO_LDFLAGS="-L $PWD/third_party/lib" go test -race -count=1 -run 'Test(Profile|DecodeOwnProfile|PrepareOwnProfile|UpdateOwnProfile|OwnProfile)' ./internal/signal`. Return changed files, both backend commands/output, red-green evidence and any remaining criteria. The controller reviews the complete backend before accepting this task.

### Task 3: Typed application use cases

**Files:** Create `internal/app/profile.go` and `internal/app/profile_test.go`.

**Interfaces:** Consumes Task 1's `signal.Client` methods and profile types.
Produces `(a *App) ProfileShow(ctx context.Context) (signal.Profile, error)` and
`(a *App) ProfileUpdate(ctx context.Context, update signal.ProfileUpdate) (signal.ProfileUpdateResult, error)`.
No Cobra, printing or backend types.

- [x] Write `TestProfileShow`, `TestProfileUpdate`, `TestProfileUpdateInvalidBeforeConnect`, `TestProfileUpdateAcceptedError`, `TestProfileUseExistingConnection`, and `TestProfileAccountSelection`. Use the frozen fake. Assert exactly one mutation attempt, merged omitted/empty values, accepted result retained on wrapped follow-up errors, no connect on invalid input, reuse of connected clients and only the selected account modified.

```go
out, err := use.ProfileUpdate(t.Context(), signal.ProfileUpdate{About: &about})
if !errors.Is(err, followUpErr) || !out.Accepted || !out.Verified || len(fake.ProfileUpdates()) != 1 {
    t.Fatalf("accepted result/error = %+v/%v", out, err)
}
```

- [x] Run `CGO_ENABLED=0 go test -count=1 -run '^TestProfile' ./internal/app` red.
- [x] Implement the exact two app methods: validate update before connecting, use `connectSendOnly`, delegate once, return partial accepted results and wrap the error with command context.
- [x] Rerun the scoped backend-free tests green. When Task 2 is ready, the controller reruns app tests with cgo/race and pure-Go tags and reviews the diff. Return files, red-green commands and test output; no shared/fake/plan edits.

### Task 4: CLI and plain/JSON rendering

**Files:** Create `cmd/profile.go`, `profile_show.go`, `profile_update.go`,
`profile_test.go`; modify `cmd/root.go`. Create `internal/output/profile.go` and
`profile_test.go`. Create goldens `cmd/testdata/profile_show_plain.golden`,
`profile_show_json.golden`, `profile_update_plain.golden`,
`profile_update_json.golden`, `profile_unchanged_plain.golden`,
`profile_unchanged_json.golden`, `profile_clear_json.golden`.

**Interfaces:** Consumes Task 3's app methods. Produces
`newProfileCmd(*clientOpener, *printerFactory) *cobra.Command`,
`newProfileShowCmd(*clientOpener, *printerFactory) *cobra.Command`,
`newProfileUpdateCmd(*clientOpener, *printerFactory) *cobra.Command`,
`(*output.Printer).Profile(signal.Profile) error`,
`(*output.Printer).ProfileUpdate(signal.ProfileUpdateResult) error`, and
`output.ProfileJSON` with `aci`, `givenName`, `familyName`, `about`,
`aboutEmoji`, optional `avatarPath`. Root wires the parent command once.

- [x] Write `TestProfileCommandsGolden`, `TestProfileClearAndOmittedFlags`, `TestProfileCommandValidationBeforeOpen`, `TestProfileCommandFailures`, `TestProfileCommandTree` and output tests `TestProfileRenderingEscapesControls`, `TestProfileJSONEmptyTextFields`. Cover all planned goldens, no positional args, no-flags update, combined-name byte overflow, family-only/multiword names, invalid UTF-8/NUL, unknown flag, unchanged update, account flags and empty stdout on all errors.

```go
out, err := run(t, fake, "profile", "update", "--about=")
if err != nil || fake.Profiles[ownACI].About != "" || fake.Profiles[ownACI].FamilyName != "Smith" {
    t.Fatalf("clear/omission = %q/%v", out, err)
}
```

- [x] Run `CGO_ENABLED=0 go test -count=1 -run '^TestProfile' ./cmd ./internal/output` red.
- [x] Implement constructors and printers. Use `Flags().Changed()` to build pointer updates and `Check()` before opening an account. JSON envelopes are `{version,profile}` and `{version,profile,changed,accepted,verified}`; empty text fields remain present and schema version stays 1. Plain text quotes user-controlled values with `strconv.Quote` to prevent control-character injection. Return app errors before printing, including accepted and uncertain errors.
- [x] Generate only the named profile goldens with `UPDATE_GOLDEN=1 CGO_ENABLED=0 go test -count=1 -run '^TestProfile' ./cmd`; inspect each fixture, then rerun without the environment variable. Do not rewrite unrelated fixtures.
- [x] Return changed files and test evidence. The controller reviews app/CLI/output integration and runs affected suites on both backends after the real implementation is ready.

### Task 5: Documentation, roadmap and live-check procedure

**Files:** Modify `README.md`, `docs/json.md`, `docs/dev.md`, `PLAN.md`, and this
plan's progress checkboxes. Update the spec status only to reflect actual approval.

**Interfaces:** Documents the implemented public commands/output; no new code or
integration environment is created. Depends on confirmed behavior from Tasks 2–4.

- [x] Add README examples for show, setting given/family names and clearing about. Explain byte limits, omission semantics, account selection, avatar preservation, v1/v2 limit, concurrent edits and inspect-before-retry errors.
- [x] Document both JSON envelopes and fields, including `changed`, `accepted`, `verified`, explicit empty text and no output on failure. Keep schema version 1.
- [x] Add a separately enabled manual live-check procedure in `docs/dev.md`: dedicated disposable linked account, both backends, capture original profile/text/metadata, verify update and phone refresh, verify avatar/payment/privacy/badges, restore original text and verify restoration. Require explicit opt-in; do not run mutations here because credentials are absent. Explain the backend's incoming LOCAL_PROFILE/storage/cache limitations.
- [x] Re-read the live roadmap. Close implementation, offline tests and documentation only after controller verification, with a distinct unchecked live-verification child. Retain all earlier pending group live checks. Do not close the “Group management and profile updates” heading or unrelated Later items.
- [x] Run `just fmt` and `git diff --check`; inspect docs against command help and the inspected goldens. Update planning records once after final verified behavior is known.

### Task 6: Integrated verification, independent review and PR

**Files:** All task-owned changes; no changes to reference submodule, dependency
pins or database schema.

**Interfaces:** Consumes the complete implemented CLI; produces verified commits
and a PR targeting `main`, without merging.

- [x] Inspect `git status --short`, every changed path and complete diff; reconcile worker reports against allowed ownership and the spec. Run affected tests locally on both backends, including serial transport failure paths.
- [x] Run `just fmt`, then `just lint`. Request fresh code review for spec compliance, accepted/uncertain outcomes, encrypted preservation, mutation retries, key checks, lifecycle and test coverage. Resume each owner for fixes; controller resolves shared-file changes and records rulings in the execution ledger.
- [x] Run fresh `just check` and `just check-purego`. Use `bin/check-tmp` for both `TMPDIR` and `GOTMPDIR` when the default temp filesystem is too small. Inspect exit codes and output. Do not ship failing checks or mark live acceptance passed based on offline tests.
- [x] Mark completed plan tasks and roadmap items using controller evidence. If review/fixes changed code, rerun affected checks and full checks before claiming success.
- [ ] Stage only task-owned files and make conventional logical commits once the repository's formatting/lint requirements pass. Preserve the existing design commit. Inspect branch history and any existing PR; push normally and create/update the PR to main using a temporary body file.
- [ ] Verify PR URL/base/head/status, remove task-owned temporary files, and report delivered commands, passed checks, v1 concurrency/v2 limits and pending live verification. Do not merge or push directly to main.
