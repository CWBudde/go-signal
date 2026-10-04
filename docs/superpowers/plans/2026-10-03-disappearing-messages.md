# Disappearing-message Timer Inheritance Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make outgoing messages inherit known disappearing-message timers for each direct chat and group on both supported backends.

**Architecture:** Persist direct-chat seconds/version in the selected account's SQLite database. Learn raw message and contact-sync settings before acknowledgement, stamp each direct recipient's cloned message, and use the fork's retrieved group state to stamp group content. Repair fork sync failure propagation to preserve redelivery.

**Tech Stack:** Go 1.26, SQLite (mattn/modernc), signalmeow fork, protobuf, cgo and libsignal_go, existing facade and offline fixtures.

**Spec:** [Approved design](../specs/2026-10-03-disappearing-messages-design.md). Written spec and plan approved; offline implementation and task reviews complete. Whole-branch and final-fix reviews complete; shipping pending; phone acceptance unrun. Use scoped implementation/review subagents as requested.

## Global Constraints

- Main baseline `9e1781d`; feature branch `feat/disappearing-messages` in `/tmp/go-signal-disappearing-messages`. Spec commit `788a698`.
- Fork baseline `9cd9cbd51b0b37b575a0d8d737b35b969967bd66` (`v0.2609.0-purego.14`); create `/tmp/mautrix-signal-disappearing-messages` from that tag when execution begins. Original fork and reference checkouts remain untouched.
- Absent timer row means unknown; zero seconds explicitly disables expiry. Persist uint32 seconds/version and update existing rows only for strictly newer versions, even if seconds are identical.
- Legacy unversioned messages use version zero and can initialize only an absent row. Unknown outgoing direct chats use zero seconds/version one without persisting that fallback.
- Learn direct settings only from body-present messages or EXPIRATION_TIMER_UPDATE, including nested edits. Both fields absent without that flag leave settings untouched. A present version or explicit flag with absent seconds means zero. Ignore group metadata and bodyless controls.
- Sent sync uses destination ACI; ordinary receive uses sender ACI; self uses the account's own ACI. Reject invalid/missing/PNI-only keys. Contact sync requires both timer fields; IsFromDB metadata is ignored.
- Persist before event delivery/acknowledgement and contact-sync completion. Persistence failure emits nothing and returns false. Send-only learns settings while preserving existing message/contact acknowledgement policy.
- Stamp ExpireTimer and ExpireTimerVersion separately for each direct recipient clone. Timer read failure skips transmission to that recipient and preserves partial results.
- Group ExpireTimer comes from the same retrieved group object as GroupV2 context; clear direct-chat ExpireTimerVersion for ordinary and edit group content.
- No new public Client/SendRequest fields, output fields, timer-setting command, local deletion, universal timer default or expiry-start policy. Existing Unsupported timer-update output and SchemaVersion stay unchanged.
- Workers edit only assigned paths; no commits, pushes, shared records, dependency pins, repository-wide formatters or extra agents. Controller verifies, records rulings, commits and publishes.
- Run just fmt and just lint before commits. Full controller checks precede shipping. Publish a reviewed immutable fork tag through the established release branch, then pin it without replace and open one go-signal PR. No merge, force push or moved tags.
- No production Signal calls. Keep live phone acceptance and other Phase 12 items open.

## Review Focus

- Duplicate contact entries for one ACI with distinct versions must converge on the highest version independent of input order; equal-version conflicts keep the first accepted value (Task 1 TestChatTimersBatchOrder; Task 3 TestHandleContactTimersDuplicates).
- A newer equal-duration update followed by an old disable must retain the newer enabled setting (Task 1 TestChatTimerHighWater; Task 3 TestHandleChatTimerHighWater).
- Missing nested edit content or invalid sync destination must not panic or create a timer row (Task 3 TestHandleChatTimerMalformed).
- Close racing with timer persistence must wait for active handling and never acknowledge an unstored timer (Task 3 TestHandleChatTimerClose).
- One failed recipient timer read in a batch must leave other recipients' content/results independent and perform no send for the failed recipient (Task 3 TestSendDirectTimerPartialFailure).

## Execution and checks

Execute tasks sequentially with a fresh implementer and task reviewer for each,
then one whole-branch review. Tasks 1 and 2 have independent implementation
boundaries, but their commits and controller gates are serialized. Do not run
multiple golangci-lint processes concurrently.

At execution start, record a clean baseline before product edits. Use existing
shared library `/home/christian/Code/go-signal/third_party/lib` for cgo checks and
initialize the isolated worktree's libsignal submodule to its recorded commit.
Reuse already installed dependencies/tools; do not change unrelated module pins.

For direct cgo commands set
`CGO_LDFLAGS='-L /home/christian/Code/go-signal/third_party/lib'`.
Pure-Go commands use `CGO_ENABLED=0` and `-tags libsignal_go`.
Use writable caches and temp roots under `/home/christian/Code/go-signal/bin` or
`/tmp`; existing task-neutral tool caches may be reused. Capture failing and
passing targeted test output. Never claim a full-fork check passes if baseline
bridge/lint failures remain; record comparison evidence separately.

### Task 1: Persistent account-local direct timers

**Files:** Create `internal/store/chat_timers.go`, `internal/store/chat_timers_test.go`, `internal/store/upgrades/07-chat-timers.sql`; extend `internal/store/export_test.go` only for a narrowly scoped timer SQL fault fixture if needed.

**Interfaces:** Produce `ChatTimerRecord{ACI string; Seconds, Version uint32}`, `(*Store).ChatTimer(ctx context.Context, aci string) (ChatTimerRecord, bool, error)`, `(*Store).MergeChatTimer(ctx context.Context, rec ChatTimerRecord) error`, and `(*Store).MergeChatTimers(ctx context.Context, records []ChatTimerRecord) error`. All production code retains `cgo || libsignal_go` tags. Later facade code consumes these methods; no signal types enter store.

- [x] Write TestChatTimerUnknownAndDisabled: missing row returns found=false; merging `{ACI: peer, Seconds: 60, Version: 1}` then `{Seconds: 0, Version: 2}` retains a found=true zero row. Write TestChatTimerHighWater: 60/version3 then 60/version5 then 0/version4 remains 60/version5. Run `CGO_ENABLED=0 go test -tags libsignal_go -run '^TestChatTimer' ./internal/store/`; expect RED from the missing API.

  Core assertions after arranging each case:

  ```go
  got, found, err := data.ChatTimer(t.Context(), peer)
  if err != nil || !found || got.Seconds != 60 || got.Version != 5 {
      t.Fatalf("timer = %+v, %v, %v; want 60/version5", got, found, err)
  }
  ```

- [x] Add table `gosignal_chat_timers` with ACI primary key and checked INTEGER seconds/version in `[0,4294967295]`. Implement canonical nonzero ACI validation, reads with absence distinct from error, and strict-newer SQL upsert. Implement batch merge as one transaction with complete validation and rollback on any failure; single merge delegates to the batch path.
- [x] Add TestChatTimerOrderingAndRange, TestChatTimerLegacy, TestChatTimersBatchOrder, TestChatTimersBatchAtomic, TestChatTimerRestartAndAccounts and TestChatTimerInvalidACI. Assert equal/stale updates ignored, MaxUint32 preserved, version-zero initialization only, duplicate ordering converges, failed batch changes no row, restart retains explicit off, and two accounts retain independent timers. Invalid inputs include nil UUID, uppercase/noncanonical UUID and PNI-prefixed ID. Verify rollback after a real second-row SQL failure using a test-only trigger fixture.
- [x] Run GREEN targeted tests and all store tests on pure Go and cgo with race; run changed-file formatting and scoped lint. Record exact commands/results and self-review. Controller independently verifies, runs just fmt/lint and commits `feat: persist direct-chat disappearing timers`; task reviewer approves the actual diff before continuing.

### Task 2: Fork timer metadata and sync acknowledgement fixes

**Files (fork):** Modify `pkg/signalmeow/contact.go` (review-required strict framing), `pkg/signalmeow/events/message.go`, `pkg/signalmeow/receiving.go`, `pkg/signalmeow/sending.go`, `pkg/signalmeow/export_test.go`, `pkg/libsignalgo/PUREGO.md`. Create `pkg/signalmeow/disappearing_messages.go`, `pkg/signalmeow/disappearing_messages_test.go` and `pkg/signalmeow/sync_ack_test.go`.

**Interfaces:** Add `events.ContactTimer{ACI uuid.UUID; ExpireTimer, ExpireTimerVersion *uint32}` and `events.ContactList.Timers []ContactTimer`. Owned pointer values retain field presence. Add private `stampGroupMessage(content *signalpb.Content, group *Group)` for context/timer assignment and `(*Client).storeContactSync(ctx context.Context, data []byte) (*events.ContactList, error)` for decoding and storing one contact attachment. Existing public fork send signatures remain unchanged.

- [x] Write TestContactSyncTimers using encoded ContactDetails fixtures with distinct ACIs, skipped invalid contacts, missing fields, zero/version2 and 60/version3. Assert timer entries use successfully converted recipient ACIs, retain absent versus explicit zero, own their pointer values, and disappear entirely on failed contact transaction. Run `CGO_ENABLED=0 go test -tags libsignal_go -run '^TestContactSyncTimers' ./pkg/signalmeow/`; expect RED.
- [x] Implement storeContactSync by extracting current decode/transaction work. Append paired timer metadata only for successful contacts and return no event on any decode/store error. Stop contact handling on download failure and return false; decode/store failures also return false. Successful event delivery uses the existing handler return value. Storage-generated IsFromDB lists need no metadata.
- [x] Write TestSyncSentAckFailure and TestSyncContactAckFailure against handleSyncMessage through a narrow test export and existing offline device/attachment fixtures. For ordinary and edit sent transcripts, a false event handler must produce false acknowledgement; true must remain true. Contact download/decode/transaction failures must emit no ContactList and return false. Run RED before changing failure propagation, then assign incomingDataMessage/incomingEditMessage success results instead of discarding them.
- [x] Write TestGroupMessageTimer for ordinary and nested edit messages: supplied group revision7/duration60 yields matching GroupV2 revision7 and ExpireTimer60, duration0 preserves a present zero, and any direct version is cleared. Preserve body, rich content and timestamp; typing still receives only its group ID. Run RED, implement stampGroupMessage and call it using the one retrieved group object in SendGroupMessage. Do not add a second retrieval or direct timer cache.

  For both message envelope forms, assert the extracted DataMessage:

  ```go
  if msg.ExpireTimer == nil || msg.GetExpireTimer() != 60 ||
      msg.ExpireTimerVersion != nil || msg.GetGroupV2().GetRevision() != 7 {
      t.Fatalf("group message = %v; want timer60/revision7/no direct version", msg)
  }
  ```

- [x] Run GREEN new tests, existing group/receive regressions and affected signalmeow/libsignalgo subtrees on both backends, with cgo race where supported. Run fork formatting/lint and stub/API parity checks; compare inherited whole-fork failures against baseline. Document these fork extensions in PUREGO.md. Self-review/report; controller verifies and commits reviewed task.
- [x] Controller checks release branch and remote tags, normally publishes reviewed fork changes to purego under a fresh available `v0.2609.0-purego.N` tag, and verifies downloaded source/Origin match the reviewed commit. Update only mautrix-signal go.mod/go.sum entries. Run tidy, just fmt/lint, current facade tests on both backends and commit the pin. Record the actual version/commit for Task 3 and maintenance docs. No replace directive or unrelated dependency changes.

### Task 3: Learn and send timers through the facade

**Files:** Create `internal/signal/meow_chat_timers.go`, `internal/signal/meow_chat_timers_test.go`; modify `internal/signal/meow.go`, `internal/signal/meow_send.go`, `internal/signal/offline_export_test.go`. Update `README.md`, `docs/dev.md`, `docs/maintenance.md`; controller updates `PLAN.md` and design/plan status/evidence.

**Interfaces:** Consume Task 1 Store methods and Task 2 ContactList metadata. Produce private `(*meowClient).learnChatTimers(ctx context.Context, raw events.SignalEvent) error`, `(*meowClient).directMessage(ctx context.Context, recipient Recipient, fresh func() *signalpb.DataMessage) (*signalpb.DataMessage, error)`, and `(*meowClient).sendDirectRecipients(ctx context.Context, req SendRequest, fresh func() *signalpb.DataMessage, send func(context.Context, libsignalgo.ServiceID, *signalpb.Content) signalmeow.SendMessageResult) SendResult`. Send invokes that helper with the real `cli.SendMessage`; scoped test exports call the same helper with a recording send function. Keep dependency types inside the facade and test-only exports; no public Client/SendRequest changes.

- [x] Write TestHandleChatTimers, TestHandleChatTimerHighWater and TestHandleChatTimerMalformed using existing openOffline/Handle fixtures. Assert sender versus sync destination/self mapping, body-present empty text, explicit updates, nested edits, missing fields protection, versioned omitted-seconds disable, legacy-zero behaviour, no learning from group/bodyless controls, and nil/invalid/PNI-only destinations. Run `CGO_ENABLED=0 go test -tags libsignal_go -run '^TestHandleChatTimer' ./internal/signal/`; expect RED because Handle does not persist timers.
- [x] Implement raw event extraction in meow_chat_timers.go with canonical nonzero ACI keys. Ordinary incoming uses sender; own-sender transcripts use direct ChatID destination. Ignore group content, missing nested edits and ineligible messages. Call store merge only for eligible updates. Contact lists collect entries with both fields present and merge in one batch; IsFromDB leaves timers unchanged.
- [x] Write TestHandleContactTimers, TestHandleContactTimersDuplicates, TestHandleChatTimerPersistenceFailure, TestHandleContactTimerPersistenceFailure, TestHandleChatTimerSendOnly and TestHandleChatTimerClose. Use a deterministic test SQL trigger failure to prove no event/ack or sync completion before successful storage. Verify a replay persists/delivers after removing the fault, duplicates converge, send-only learns while returning false for message events, and an active handler blocks Close until safe completion. Use channels and test-context cancellation rather than timing sleeps; test hooks stay scoped to timer paths.
- [x] Invoke learnChatTimers inside handle's existing handling guard, before contactsStored/convert/emit. Use a bounded persistence context (existing overrideSettleTimeout), log errors and return false on failure. Preserve existing ignored-event and send-only behaviour after learning succeeds. Run GREEN receive/contact/failure tests on both backends.
- [x] Write TestSendDirectTimers, TestSendDirectTimerRestart, TestSendDirectTimerRichContent and TestSendDirectTimerPartialFailure. Recording production-helper callbacks assert two recipients get independent 30/version2 and 300/version4 protobuf fields, off remains zero/version5, self uses its own row, unknown uses zero/version1 without a stored row, and edits/controls retain rich content. Inject a SELECT failure for one ACI using a test database fixture, assert no callback for that recipient and successful unchanged wire/result for the other. A SQLite view can fail only that row via `abs(-9223372036854775808)` while returning valid rows for others; restore the table after the test. Use registered-driver access confined to test files rather than a new production reader override. Run RED before adding directMessage/sendDirectRecipients and replacing the direct loop in Send.

  Recording callback assertions include protobuf presence and recipient pairing:

  ```go
  want := map[string][2]uint32{peerA: {30, 2}, peerB: {300, 4}}
  pair := want[recipientID.String()]
  msg := content.GetDataMessage()
  if msg.ExpireTimer == nil || msg.ExpireTimerVersion == nil ||
      msg.GetExpireTimer() != pair[0] || msg.GetExpireTimerVersion() != pair[1] {
      t.Fatalf("recipient %s content = %v; want %v", recipientID, content, pair)
  }
  ```

- [x] Implement directMessage to read before cloning/stamping. Implement sendDirectRecipients with existing ACI resolution, wrapOutgoing, result conversion and partial errors; preserve timestamp and self sync behaviour. Group branch continues to use fork SendGroupMessage and never reads direct timer rows. Run GREEN all new send tests and existing send/edit/attachment/control tests on both backends.
- [x] Document timer inheritance, persistence, unknown fallback, zero-disable and receive/contact-sync refresh requirements in README. Add a disposable-account phone acceptance procedure in docs/dev covering two direct recipients with different durations, disable/stale updates, restart, note-to-self, edits and group changes on both backends. Describe client expiry as phone behaviour; do not promise local purge. Update maintenance provenance and preservation/testing instructions for the actual fork release.
- [x] Run all affected facade tests and existing output/command goldens. Confirm timer update still converts to Unsupported and SchemaVersion unchanged; add a focused regression if current assertions omit it. Self-review/report; controller verifies and commits after just fmt/lint. Task reviewer checks store+fork consumption and the raw-handler/send failure boundaries.

## Final verification, review and shipping

- [x] Record the approved plan and baseline evidence, then execute all three tasks without further task-by-task approval prompts. Record implementation rulings and tests in this plan.
- [x] Update PLAN.md with separate disappearing-message implementation and phone acceptance checkboxes; mark only offline work complete. Keep the Phase 12 done-when-phone condition, Phase 11 live tests and unrelated messaging gaps open.
- [x] Controller runs just fmt, just lint, full just check (formatting, lint, libsignal guard, cgo race suite, tidy), just check-purego (vet/lint/tests/AES assembly), `CGO_ENABLED=0 go test -count=1 ./...`, just build and just build-cgo. Require fresh passing evidence; classify any inherited fork limitations precisely. Run git diff --check and inspect the changed output/doc contracts.
- [x] Request one whole-branch review of actual main and fork ranges. Address material findings, recheck changed boundaries and keep durable review rulings. Final status records must distinguish offline success from unrun phone checks.
- [ ] Commit verified integration records, normally push feat/disappearing-messages and create one PR targeting main with concrete problem, final behaviour, fork provenance, validation and live limitation. Inspect final-head CI and report its actual outcome. No merge or live Signal calls. Clean only this plan's owned scratch workspace; retain worktrees/branches for review.

## Planning record

2026-10-03: Written spec approved. Source inspection confirmed migration 6 is the
current account schema; ContactList drops timer fields; SendGroupMessage has one
authoritative retrieval; sent-sync handler return values and contact-sync errors
currently bypass acknowledgement failure. Plan self-review covers every spec
section, matching store/fork/facade interfaces and all five Review Focus cases.
No product code, dependency pin, fork release or Signal traffic has changed.

2026-10-04: User approved plan. Baseline `just check` and `just check-purego` pass. Task 1 committed as `93445bc`; fresh store suites pass on pure-Go and cgo race, repository formatting/lint pass, and independent spec/quality review found no issues. The initial API tests were RED-first; later edge cases were added after the shared implementation. Fork affected suites pass on both backends and stub parity matches 516 declarations; inherited default lint issues and whole-tree Matrix bridge pure-Go build failure are recorded separately.

Task 2 review required strict contact framing: reject incomplete message/avatar lengths before storage while retaining valid empty records. Fixed in `0d00d3f` after regression RED showed partial persisted contacts and successful acknowledgements; scoped re-review passed. The reviewed fork release is `v0.2609.0-purego.15` at `0d00d3fd3bc86f49b89c77183f841c5eb516c25e`. Download Origin and production source match; both pure-Go CI runs passed. Real SQLite rollback is covered on cgo; pure-Go fork contact tests use a narrow controlled driver because no pure-Go SQLite dependency exists in the fork.

Controller Task 3 verification: `just fmt` changed no files, repository lint reported zero issues, libsignal guard matched v0.102.2 and cgo race/tidy passed on a full rerun. An initial run hit the unchanged fake daemon test's first-event/inbox timing gap; five focused race runs and the full rerun passed. `just check-purego`, the no-backend full suite, `just build` and `just build-cgo` passed. Product task commit `9317138`; independent task and whole-branch reviews remain pending. Phone acceptance is unrun.

Task 3 independent spec/quality review approved `18413f0..9317138` with no findings. All offline implementation tasks are complete. PLAN.md marks implementation separately from deferred phone acceptance; other Phase 12 work and Phase 11 live checks remain open. Whole-branch review and shipping follow.

Whole-branch review approved the offline implementation with no Critical or Important findings. One final fix wave addressed the inherited daemon fixture race (`2509154`) and reproduced/fixed the optional contact avatar MIME panic (`622eab1` in the fork). Scoped re-review found both addressed with no new breakage. Fresh controller affected fork suites pass on pure-Go and cgo race; API parity remains 516 declarations. The fresh immutable `v0.2609.0-purego.16` tag points to `622eab11d06f82755445d371faf33d876c686e34`; downloaded Origin and all five changed production files match. The `.15` tag remains unchanged. Final main verification and publication records follow.

Implementation rulings, in order:

- Use real cgo SQLite plus a controlled pure-Go fork contact fixture rather than add a new dependency. If wrong, backend-specific contact SQL needs stronger coverage; facade/store still exercise real modernc transactions.
- Tighten incomplete contact framing as part of the no-partial-list/ack contract. If valid phone framing is rejected, sync may redeliver until compatibility is corrected.
- Address the observed daemon fixture race and inherited avatar MIME panic in the final fix wave. This costs an additional immutable fork release; an incorrect barrier or MIME expectation would require test/compatibility adjustment.

Final integration at `.16`: controller `just check` passes (format0, lint0, libsignal v0.102.2, full cgo race suite, tidy); `just check-purego`, the no-backend suite, `just build` and `just build-cgo` pass. Both exact-head fork pure-Go CI runs pass; broad fork formatting/bridge limitations remain documented in maintenance. Phone acceptance is unrun. PR publication follows.
