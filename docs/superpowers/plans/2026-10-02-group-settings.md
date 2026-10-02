# Group Settings Implementation Plan

**Goal:** Add `groups update <group>` for description, disappearing timer, announcement mode and edit/add-member permissions.

**Architecture:** Extend the existing Signal facade, typed application use cases and Cobra command tree. Fetch fresh state and submit one combined change through the existing direct single-attempt group transport, then fetch the accepted state. No dependencies or migrations change.

**Approved design:** The user approved the in-chat plan and requested implementation on 2026-10-02.

## Constraints and interfaces

- Add `signal.GroupUpdate` with optional `Description *string`, `TimerSeconds *uint32`, `AnnouncementsOnly *bool`, `MembersCanEditAttributes *bool` and `MembersCanAddMembers *bool`; `Check() error` rejects an empty update or invalid UTF-8 description.
- Add `Client.UpdateGroup(context.Context, string, GroupUpdate) (Group, error)` and `app.GroupsUpdate(context.Context, UpdateGroupRequest) (signal.Group, error)`, where the request contains `Group string` and `Update signal.GroupUpdate`.
- CLI flags: `--description`, `--timer` (uint32 seconds, zero disables), `--announcements-only` (including explicit false), `--edit-permission members|admins`, `--add-member-permission members|admins`. Omitted flags preserve settings; empty description clears it. Titles continue through `groups rename`.
- Validate before opening an account. Check every supplied field against original, fresh membership and permissions before comparing values; description/timer require attribute-edit access, other fields require a full administrator. Unknown access remains conservative as in existing commands.
- Submit at most one patch; no conflict retries. Return a fresh group on success/no-op and preserve accepted ID/revision with inspection guidance on follow-up failures. Notification failures retain existing logging semantics. Never print the master key.
- Review clarification: `ErrGroupUpdateUncertain` marks transport/response-decoding failures whose acceptance the dependency cannot expose. Keep the accepted result empty, preserve the cause and include the group ID, attempted revision and inspection guidance. Explicit rejection statuses remain rejection errors. Compare requested permissions with raw enums so unknown values can be normalized to administrators.
- Add optional JSON permission booleans (explicit false for fetched groups, absent for inaccessible entries), plain permission lines, and retain output schema version 1.
- Preserve the original worktree and modified reference submodule. Keep live acceptance separate and unchecked.

## Task 1: Facade, policy and backend

- [x] Write policy tests for omission/empty/zero/false, membership and original-state permissions, combined changes, preservation, no-op and revision overflow; observe failures.
- [x] Implement shared policy, facade interface and account-aware fake update behavior.
- [x] Write transport tests for exact changes, one patch, current fetch, no-op, conflict, cancellation/unlink and accepted failures; observe failures.
- [x] Implement real facade updater and minimally generalize direct group-change helper/log wording.
- [x] Verify signal package on pure-Go and cgo/race backends and review the result.

## Task 2: Application and CLI

- [x] Write application tests for resolution, early validation, send-only connection, atomic updates, unchanged revision, account isolation and failure/no-retry behavior; observe failures.
- [x] Implement typed request/use case.
- [x] Write CLI preflight, explicit-value, plain/JSON and error tests; observe failures.
- [x] Implement command registration and flag-presence handling, reusing group rendering.
- [x] Verify application and command tests on pure Go and cgo/race.

## Task 3: Output, documentation and delivery

- [x] Write output tests for true/false permissions, inaccessible omission and secret exclusion; observe failures.
- [x] Implement additive output fields and plain lines; review changed command goldens.
- [x] Update README, JSON reference, development live procedure and PLAN.md's stale Phase 4 note. Tick only verified implementation, leaving both-backend live acceptance open.
- [x] Obtain independent backend and CLI/output reviews; resolve material findings.
- [x] Run `just fmt`, `just check`, `just check-purego`, and `CGO_ENABLED=0 go test ./...`.
- [x] Commit the verified batch on `feat/group-settings`, push and open a PR:
      [#18](https://github.com/CWBudde/go-signal/pull/18).

## Review focus

- Mixed permitted and forbidden flags must fail atomically against original permissions, even when one supplied privileged value is unchanged.
- Sparse updates must avoid the dependency's retry/rebase path, including its optional-pointer dereferences.
- Server-accepted changes must survive malformed replies and failed follow-up fetches without inviting blind retries.
- Explicit empty, zero and false values must survive flag parsing and optional-field handling.
- Shared group output changes must preserve inaccessible-state omission and secret exclusion.
