# Maintenance

## Bumping mautrix-signal (signalmeow) and libsignal

go-signal depends on three pinned pieces that have to move together:

- signalmeow, from the fork [`cwbudde/mautrix-signal`](https://github.com/cwbudde/mautrix-signal)
  of upstream `go.mau.fi/mautrix-signal` (branch `purego`, tags `vX.YYMM.Z-purego.N`). The fork
  has its own module path, `github.com/cwbudde/mautrix-signal`, so that go-signal needs no
  `replace` and `go install github.com/cwbudde/go-signal@latest` works. Its main adaptation
  is `pkg/libsignalgo`: every cgo file gets a `libsignal_go` twin on top of libsignal-go
  (its `PUREGO.md`). Since `v0.2609.0-purego.11`, it also deliberately extends
  signalmeow with bounded invite previews, one-shot joining and signed-response
  binding, plus opt-in websocket request/response logging redaction for credentials.
  `v0.2609.0-purego.14` adds authenticated password-free join-request previews and
  exact signed own-ACI request cancellation in one PATCH, without generic mutation
  retries, full-state reads, profile credentials, member notification or device sync.
  `v0.2609.0-purego.15` preserves owned contact-sync timer metadata with field presence,
  stamps group timer seconds from the same retrieved state as the GroupV2 context, clears
  direct timer versions on group messages and propagates sent-transcript/contact-sync
  handler failures to acknowledgement. Incomplete contact attachment framing is rejected.
  `v0.2609.0-purego.16` additionally handles an absent optional contact-avatar MIME type
  with content detection, preserving avatar bytes and paired timer metadata.
  `v0.2609.0-purego.17` adds an opt-in provisioning API that renews only an idle scan
  wait. After an envelope arrives, acknowledgement, decryption and registration failures
  are terminal. The original API keeps its one-shot two-minute timeout.
  `v0.2609.0-purego.19` adds opt-in websocket story delivery and typed incoming/
  sent-transcript story events, retaining group/profile key storage and handler-failure
  acknowledgement behavior. Story handling sends no delivery/read/viewed receipts.
  `v0.2609.0-purego.20` adds explicit-timestamp group story sends with sealed pairwise
  encryption, same-state group context/audience and an own-device story transcript.
  Peer failures and transcript failures are separate outcomes; accepted peers are not
  resubmitted to repair a failed transcript.
  `v0.2609.0-purego.21` adds ACI private distribution-list story sends, including
  My Story, with explicit audiences and intended-recipient manifests in own-device sync.
  `v0.2609.0-purego.22` fixes the captured incoming-request-channel shutdown race
  in `SignalWebsocket.connectLoop`, allowing zkgroup integration with `-race`.
  `v0.2609.0-purego.23` stabilizes status-channel lifetime, joins active request handlers
  before completion, makes incoming enqueue and caller response waiting cancellation-aware,
  and joins connection workers before draining pending responses. Queued incoming requests
  are discarded without acknowledgment on final shutdown; handlers survive reconnect.
  `v0.2609.0-purego.24` separates receive transport shutdown from worker joining,
  allowing the tracked key checker to deliver logout after a rejected PNI upload.
  It also keeps the initial-connect channel immutable and makes repeated gRPC close succeed.
- [`cwbudde/libsignal-go`](https://github.com/cwbudde/libsignal-go) (tags `vX.Y.Z-cw.N`), the
  pure-Go libsignal that the default backend runs on. Its Rust compat harness is pinned to the
  libsignal tag libsignalgo was generated against (its `decisions/0007-cwbudde-fork-policy.md`).
- The `third_party/libsignal` submodule, which the cgo backend links. It must sit at exactly the
  tag libsignalgo was generated against (`pkg/libsignalgo/signalversion/version.go`);
  `just check-libsignal` and the `internal/signal` tests fail when the two differ.

Always move to an upstream release tag, never a pseudo-version of `main`. Commit to the forks'
release branches and push; don't open PRs.

The disappearing-message baseline is the immutable
[`v0.2609.0-purego.16`](https://github.com/cwbudde/mautrix-signal/tree/v0.2609.0-purego.16)
tag at [commit `622eab1`](https://github.com/cwbudde/mautrix-signal/commit/622eab11d06f82755445d371faf33d876c686e34).
The downloaded module's Origin and production source match that commit. Both pure-Go CI
runs passed ([37183497231](https://github.com/cwbudde/mautrix-signal/actions/runs/37183497231),
[37183497248](https://github.com/cwbudde/mautrix-signal/actions/runs/37183497248)).
The broad Go CI runs
([37183497235](https://github.com/cwbudde/mautrix-signal/actions/runs/37183497235),
[37183497226](https://github.com/cwbudde/mautrix-signal/actions/runs/37183497226))
failed pre-commit formatting on six libsignalgo files, all byte-identical to the `.14`
baseline `9cd9cbd`. Local whole-fork lint likewise retains 40 default-backend and 42
pure-Go inherited findings; the broad Matrix bridge pure-Go build still fails on
`sqlite3.Error`/`sqlite3.ErrCorrupt`. Its cgo build also needs the external `olm/olm.h`
header, unavailable in the local validation environment. These are dependency
limitations, not passing whole-fork checks.

Affected fork tests passed on both backends. Contact transaction rollback is exercised
with the real cgo database; the pure-Go counterpart uses a controlled transaction fixture
because the upstream dbutil SQLite dialect handling prevents that real-store fixture there.
Phone acceptance remains unrun; offline tests do not verify rendering or expiry.

The current QR-refresh release is the immutable
[`v0.2609.0-purego.17`](https://github.com/cwbudde/mautrix-signal/tree/v0.2609.0-purego.17)
tag at [commit `d984cb5`](https://github.com/cwbudde/mautrix-signal/commit/d984cb5c4749c20f5e32d61e1809b6f40e08b8d5).
The affected `pkg/libsignalgo/...` and `pkg/signalmeow/...` tests passed locally on
pure Go and cgo with the race detector; pure-Go vet and the backend API parity check
also passed. The downloaded module's Origin and production source match that commit.
Both pure-Go CI runs passed
([37219880276](https://github.com/CWBudde/mautrix-signal/actions/runs/37219880276),
[37219880145](https://github.com/CWBudde/mautrix-signal/actions/runs/37219880145)).
The broad Go CI runs failed pre-commit formatting
([37219880168](https://github.com/CWBudde/mautrix-signal/actions/runs/37219880168),
[37219880118](https://github.com/CWBudde/mautrix-signal/actions/runs/37219880118))
on the same six libsignalgo files, all byte-identical to the `.16` baseline.
QR-refresh phone acceptance remains unrun.

The story-reception release is the immutable
[`v0.2609.0-purego.19`](https://github.com/cwbudde/mautrix-signal/tree/v0.2609.0-purego.19)
tag at [commit `f2b5e49`](https://github.com/cwbudde/mautrix-signal/commit/f2b5e49cc89af2582df1d1dd42f34e3be446f9de).
The downloaded module's Origin and all changed fork files match that commit. The affected
`pkg/libsignalgo/...` and `pkg/signalmeow/...` suites passed locally on pure Go and cgo
with the race detector on cgo. Pure-Go vet and the backend API parity check passed.
Both pure-Go CI runs passed
([37238649485](https://github.com/CWBudde/mautrix-signal/actions/runs/37238649485),
[37238648974](https://github.com/CWBudde/mautrix-signal/actions/runs/37238648974)).
The broad Go CI runs failed pre-commit formatting
([37238649428](https://github.com/CWBudde/mautrix-signal/actions/runs/37238649428),
[37238648965](https://github.com/CWBudde/mautrix-signal/actions/runs/37238648965))
on six libsignalgo files, all byte-identical to the `.17` baseline. Changed story files pass
the fork's exact `goimports -local github.com/cwbudde/mautrix-signal` formatting check.
Story reception phone acceptance remains unrun; private audience management remains planned.

The group-story sending release is the immutable
[`v0.2609.0-purego.20`](https://github.com/cwbudde/mautrix-signal/tree/v0.2609.0-purego.20)
tag at [commit `4a5d29e`](https://github.com/cwbudde/mautrix-signal/commit/4a5d29e351262958939c6a6518ca6a115ba40d3a).
The downloaded module's Origin and all four changed fork files match that commit.
The affected `pkg/libsignalgo/...` and `pkg/signalmeow/...` suites passed locally on
pure Go and cgo with the race detector on cgo. Pure-Go vet and the backend API parity
check passed, and all changed files pass the fork's exact `goimports -local` check.
Both pure-Go CI runs passed
([37242026136](https://github.com/CWBudde/mautrix-signal/actions/runs/37242026136),
[37242026216](https://github.com/CWBudde/mautrix-signal/actions/runs/37242026216)).
The broad Go CI runs failed pre-commit formatting
([37242026097](https://github.com/CWBudde/mautrix-signal/actions/runs/37242026097),
[37242026183](https://github.com/CWBudde/mautrix-signal/actions/runs/37242026183))
on the same six libsignalgo files, all byte-identical to the `.19` baseline.
Offline tests cover the public API's own-profile-key lookup and member guard, production
story policy, content ownership, peer/sync failure combinations and self-only groups.
Full encrypted websocket submission and Android/iOS display still require the pending
[phone acceptance procedure](dev.md#group-story-sending-live-check). Private story sending is added by `.21` below; distribution-list management remains open.

The private-story sending release is the immutable
[`v0.2609.0-purego.21`](https://github.com/cwbudde/mautrix-signal/tree/v0.2609.0-purego.21)
tag at [commit `a9d9e43`](https://github.com/cwbudde/mautrix-signal/commit/a9d9e43e63aab1e2b85c02f63e441944a4b835bf).
The downloaded module's cached `.info` Origin and all three changed fork files match that
commit. The affected `pkg/libsignalgo/...` and `pkg/signalmeow/...` suites passed locally
on pure Go and cgo with the race detector on cgo. Pure-Go vet and the 516-entry backend
API parity check passed; all three changed files pass exact `goimports -local` formatting.
Both pure-Go CI runs passed
([37246001990](https://github.com/CWBudde/mautrix-signal/actions/runs/37246001990),
[37246001429](https://github.com/CWBudde/mautrix-signal/actions/runs/37246001429)).
The broad Go CI jobs failed pre-commit formatting
([37246001961](https://github.com/CWBudde/mautrix-signal/actions/runs/37246001961),
[37246001409](https://github.com/CWBudde/mautrix-signal/actions/runs/37246001409))
on the same six libsignalgo files, all byte-identical to the `.20` baseline; neither log
reports a private-story file failure.
Offline tests cover custom lists and My Story, peer/sync failure combinations, intended
recipient manifests, media/input ownership, audience guards and the public API's own
profile-key lookup. Parent tests cover complete storage projection, tombstones, binary IDs,
ACI-absent contacts, selected accounts, local blocks, persistent snapshots without stale
fallback, allowlist preflight, media and plain/JSON outcomes.
Full encrypted websocket submission and Android/iOS display still require the pending
[private story phone acceptance procedure](dev.md#private-story-sending-live-check).
The fork accepts a caller-expanded ACI audience; go-signal fetches it freshly from phone
storage rather than inventing it. PNI audience entries, list editing and custom card
presentation remain unsupported.

The websocket shutdown fix is the immutable
[`v0.2609.0-purego.22`](https://github.com/cwbudde/mautrix-signal/tree/v0.2609.0-purego.22)
tag at [commit `fda5a06`](https://github.com/cwbudde/mautrix-signal/commit/fda5a06d822321a8e4fdc93f5f74af657fab8569).
The downloaded module's Origin and all three changed files match that commit.
The cancel-during-dial regression first reproduced the captured-variable race;
affected signalmeow/libsignalgo suites then passed locally on pure Go and on cgo with
the race detector. Pure-Go vet, backend API parity and changed-file formatting
passed. The published pin passes zkgroup integration with `-race` on both backends,
and parent facade tests pass without the former log-hook shutdown workaround.
Both pure-Go fork CI runs passed
([37389203409](https://github.com/CWBudde/mautrix-signal/actions/runs/37389203409),
[37389203740](https://github.com/CWBudde/mautrix-signal/actions/runs/37389203740)).
The broad Go CI runs failed pre-commit formatting
([37389203426](https://github.com/CWBudde/mautrix-signal/actions/runs/37389203426),
[37389203670](https://github.com/CWBudde/mautrix-signal/actions/runs/37389203670))
on the same six libsignalgo files, all byte-identical to `.21`. Neither failed log
reports a changed websocket file.
This fixes the observed captured-channel race; it does not certify every websocket
lifecycle path. The [remaining lifecycle investigation](websocket-lifecycle.md)
reproduces a `Connect` status-channel race, early handler completion signaling,
blocked incoming-queue cancellation and ignored request cancellation. It also
identified a pending-response registration window by source analysis; a subsequent
barrier regression reproduces the orphan on `.22`. The `.23` release below repairs
these contracts and promotes the offline regressions into required dependency checks.
The [external security review decision](security-review.md) is a separate
deferral, not an audit result.

The websocket lifecycle repair release is the immutable
[`v0.2609.0-purego.23`](https://github.com/cwbudde/mautrix-signal/tree/v0.2609.0-purego.23)
tag at [commit `24dd760`](https://github.com/cwbudde/mautrix-signal/commit/24dd7608b6c39a5c64ce3260265329566f8a80c6).
The downloaded module's Origin and all four changed fork files match that commit.
Affected signalmeow/libsignalgo suites, the promoted websocket contracts and the
parent's deterministic late-registration probe pass with `-race` on both backends.
Pure-Go vet and changed-file `goimports -local` formatting passed. The parent
facade's real websocket-handler regression verifies the database remains open for
processing after the event callback, then closes when the handler completes.
`just test-fork` and `just test-diff` require the repaired lifecycle probes; the
script injects scheduling barriers only into a disposable source copy, never the
published fork. Queued-request shutdown semantics and handler restrictions are
recorded in the [lifecycle report](websocket-lifecycle.md). Local checks cover
these paths; phone acceptance remains open. The separate key-check repair follows below.
The inherited broad fork CI limitations above are separate from these local checks.

The receive key-check repair release is the immutable
[`v0.2609.0-purego.24`](https://github.com/cwbudde/mautrix-signal/tree/v0.2609.0-purego.24)
tag at [commit `65ae5e4`](https://github.com/cwbudde/mautrix-signal/commit/65ae5e412b09582deb356c9fc77d6136a7977e3b).
The downloaded module's Origin and all four changed files match that commit.
The real initial key-check regression reproduces the `.23` self-join after a PNI
prekey upload returns HTTP 422. On `.24`, it verifies key/password clearing attempts,
transport termination, logout delivery despite cleanup failures, and external shutdown
waiting for a gated logout callback before releasing references. The same startup path
exposed an initial-connect channel race, repaired by keeping the channel immutable.
Affected signalmeow/libsignalgo suites pass with `-race` on both backends; pure-Go vet,
the no-cgo regression and changed-file `goimports -local` formatting pass.
`just test-fork` runs the key-check regression without cgo; `just test-diff` runs it
with both backends and `-race`. The fixture uses controlled store interfaces and local
websocket peers; it does not verify live service behavior or facade restart policy.

The acknowledgement flush release is the immutable
[`v0.2609.0-purego.25`](https://github.com/cwbudde/mautrix-signal/tree/v0.2609.0-purego.25)
tag at [commit `f06b75b`](https://github.com/cwbudde/mautrix-signal/commit/f06b75b68eac54db97cc6e2d12182f2de675d566).
The downloaded module's Origin and all five changed fork files match that commit.
Delivery receipts run after the envelope response is queued; websocket progress waits
let the facade flush that response before its keepalive, then finish the receipt within
the same two-second deadline. The affected fork suites pass on pure Go and on cgo
with the race detector; pure-Go vet and changed-file formatting also pass. ACK ordering
regressions pass ten race runs on both backends, and the facade regression reproduces
the keepalive-before-ACK failure on `.24`. The pinned dependency checks require the
new websocket and receipt contracts. Fixture boundaries and the pending live interrupt
check are recorded in [docs/websocket-lifecycle.md](websocket-lifecycle.md).

The group identity-trust repair is the immutable
[`v0.2609.0-purego.26`](https://github.com/cwbudde/mautrix-signal/tree/v0.2609.0-purego.26)
tag at [commit `ac8f355`](https://github.com/cwbudde/mautrix-signal/commit/ac8f355609ac025c362f4797d73c1d6a0706d077).
The downloaded module's Origin and all three changed fork files match that commit.
Sender-key recipient selection checks ACI sending trust; missing, unreadable or
untrusted identities stay on the pairwise path. Envelope encryption checks the exact
identity it returns to crypto, covering replacement after selection. A late refusal
falls back before submitting ciphertext, with ordinary per-recipient trust enforcement.
Unlocking restores the caller's context so pairwise ratchets retain mutex protection.
Removing a previous key holder persists a fresh distribution ID with an empty sharing
list before retiring the old key, then rebuilds the audience from successful key
distributions. Metadata-write failure preserves the old key; generation or old-key
deletion failure leaves the new distribution unshared, preventing stale retry authorization.

Affected signalmeow/libsignalgo suites pass on pure Go and on cgo with the race detector;
pure-Go vet and changed-file formatting pass. Trust regressions pass ten race runs on
each backend and are required by `just test-fork` / `just test-diff`. They reproduce unsafe
selection and replacement-key envelope encryption on `.25`; review regressions reproduce
unlocked fallback session access and stale persisted sharing after failed redistribution.
The encryption fixture uses real local sessions, sender keys and a sender certificate.
The rotation fixture copies stored metadata and stops before distribution transport;
it also checks metadata/deletion failures. These checks do not establish live service
delivery, successful remote redistribution or phone rendering.

PNI identity management uses the existing typed service-ID storage without a migration.
Identity list/show/trust accept explicit `PNI:<uuid>` offline; number and username resolution
continue to select ACI identities. Safety numbers use our ACI identity key and the peer's typed
service ID (version 2, 5200 iterations), following signal-cli's `IdentityHelper` / `Utils`.
Both local identity-store wrappers enforce TOFU, changed-key sending refusal, pending change
events and stale-session removal for peer PNIs independently of ACIs with the same UUID.
`TestPNIIdentity` covers both wrappers, typed lookup, real prekey session replacement and
recovery, and numeric/QR verification. CLI golden tests and MCP tests cover typed PNI output.
These are offline checks; phone comparison and live PNI delivery have not been exercised.

The incoming verification release is the immutable
[`v0.2609.0-purego.27`](https://github.com/cwbudde/mautrix-signal/tree/v0.2609.0-purego.27)
tag at [commit `060ced3`](https://github.com/cwbudde/mautrix-signal/commit/060ced3ab4360d53c97975814eb3f1f70afd073a).
The downloaded module's cached Origin and all four changed fork files match the reviewed commit.
Authenticated own-ACI `SyncMessage.Verified` updates produce a validated internal event.
Malformed destinations, conflicting text/binary ACIs, PNI destinations, malformed keys and
missing/unknown states are ignored. The facade atomically checks the selected account's
protocol key and its current trust key; a legacy protocol-only key can acquire a trust record,
but an unknown or different key is never imported. DEFAULT means trusted-unverified, VERIFIED
means trusted-verified, and UNVERIFIED means untrusted. Matching updates clear the pending warning
while preserving key history and timestamps. ACI verification never changes PNI trust.

Trust application and old-key session removal share one transaction. A persistence or session
cleanup failure rolls back and refuses acknowledgement; redelivery can retry. Successful internal
updates participate in Close's ACK flush, including send-only mode, without public output or
outgoing sync. Fork authentication/buffer-retention tests and parent real-database tests cover
these contracts, duplicates, state mapping, account isolation, stale keys, history, legacy keys,
transaction rollback, real stale-session recovery and restart durability. Both backend fork
suites, pure-Go vet and API parity pass; the verification regressions pass ten fork race runs
per backend. Full parent `just check`, `just check-purego`, `just test-fork` and
`just test-diff`, the no-cgo suite and ten parent verification race runs per backend pass.
Both fork pure-Go CI runs passed
([37860515963](https://github.com/CWBudde/mautrix-signal/actions/runs/37860515963),
[37860515528](https://github.com/CWBudde/mautrix-signal/actions/runs/37860515528)).
The broader Go CI runs
([37860515950](https://github.com/CWBudde/mautrix-signal/actions/runs/37860515950),
[37860515552](https://github.com/CWBudde/mautrix-signal/actions/runs/37860515552))
failed pre-commit formatting on six files byte-identical to `.26`; none is a changed
verification file. These are offline fixtures, not live phone interoperability evidence.

Outgoing verification-state synchronization and `ContactRecord` identity reconciliation remain
open. Storage reconciliation needs freshness/conflict handling rather than blindly importing
keys or trust. The fork's sent-sync PNI key writes, PNI signature validation and provisioning
still use its underlying store directly and bypass facade trust callbacks. Do not interpret a
locally verified PNI as an ACI verification or phone-synchronized state.

### 1. Rebase the mautrix fork

In a `cwbudde/mautrix-signal` checkout:

The commit that renames the module path (`build!: module path github.com/cwbudde/mautrix-signal`)
is not rebased but redone: drop it, rebase the rest, then rename again on top, so that the rename
covers the new upstream files too. Fork commits made after the rename already use the new path;
where they touch upstream files, their conflicts are only in import lines.

```sh
git fetch upstream --tags
git switch purego
# the new upstream tag; the sequence editor drops the rename commit
GIT_SEQUENCE_EDITOR="sed -i '/build!: module path github.com.cwbudde.mautrix-signal/d'" \
  git rebase -i vX.YYMM.Z
git grep -l go.mau.fi/mautrix-signal | xargs sed -i 's#go\.mau\.fi/mautrix-signal#github.com/cwbudde/mautrix-signal#g'
gofmt -w $(git diff --name-only | grep '\.go$')
git commit -am 'build!: module path github.com/cwbudde/mautrix-signal'
go run ./pkg/libsignalgo/internal/stubgen -gen         # stubs for new cgo files
go run ./pkg/libsignalgo/internal/stubgen -check       # exported API parity of the two builds
```

Resolve shim conflicts in `pkg/libsignalgo`. Take upstream's side elsewhere except
for the deliberate membership extension (`groups_join*.go`, `groups_accept*.go`, `groups_cancel_request*.go`,
shared `groups_membership_http.go`, scoped websocket logging policy/tests and
pure-Go CI coverage): preserve or port those changes until upstream offers
equivalent behavior. Retain strict fresh invitation reads, own ACI/PNI promotion
and signed/fresh-state verification. Preserve cancellation's key/group/revision/own-ACI
binding, sole-action and epoch checks, bounded password-free GET/PATCH transport,
accepted-but-unverified outcomes and fresh-preview-only no-op evidence. Server
403/404 must remain errors. Run the offline tests on both backends after
rebasing and confirm no mutation retries or credential logging are introduced. Keep
the disappearing-message extensions as well: ACI-bound contact timer metadata must own its
pointer values, retain zero/presence and emit only after successful contact storage; malformed
or truncated contact framing, downloads, decoding and transactions must fail without partial
lists or acknowledgements. Preserve sent-message/edit handler failure propagation. Group
ordinary and edit content must use one retrieved revision for context and seconds, including
zero, and clear the direct timer version. Exercise all these boundaries on both backends;
retain the real cgo rollback test and record the controlled pure-Go fixture limitation.
Preserve story reception: go-signal opts into `X-Signal-Receive-Stories` before connecting;
private blocked stories are omitted, group stories retain typed group keys and revisions,
private sent transcripts use the own-account stream, and failed handlers/storage retain
buffered plaintext without acknowledgement. Story timestamps come from client envelopes or
sent transcripts, with no automatic receipts. Preserve group story sending's explicit timestamp,
same-state full-member audience/context, own profile key and sealed pairwise sessions. Story
peer requests use `?story=true`, non-urgent delivery and the implicit content hint; own-device
sync uses an ordinary authenticated sent-story transcript. Keep peer outcomes when sync fails
and never resubmit accepted peers to repair a transcript. No sender-key or group endorsement
changes are needed for this path. Private stories must retain a group-free payload and
an intended-recipient distribution-list manifest with the selected list ID and reply policy,
including failed peer submissions. Keep own-device sync separate from peer outcomes. The
parent must require fresh complete storage and never use cached audiences after fetch failure;
My Story exclusions expand only from eligible connections, and list reply settings can only
be restricted by the sender. Verify these paths on both backends.
Preserve sender-key identity trust at recipient selection and the exact-key encryption
lookup. Trust refusals fall back before submitting a multi-recipient message; pairwise
fallback must reacquire the encryption mutex. Rotating away a previous holder installs
a fresh unshared distribution ID durably before deleting the old key, so failed
redistribution cannot retain stale sharing authorization. Keep `TestSenderKeyTrust`
in both pinned-fork backend gates. Preserve the authenticated Verified dispatch, validated
ACI/key/state event, and handler-failure acknowledgement behavior; keep `TestVerifiedSync`
in both gates too.
Preserve scan-only QR renewal: fresh socket/address/key, caller cancellation, no retries
after a submitted envelope (including acknowledgement failures), and one-shot registration. Keep
the fork's fixes that upstream doesn't have yet (the cgo clock fix in `message.go`,
`prekeybundle.go` and `sessionrecord.go`, the combined endorsement result; see `PUREGO.md`).

Read `pkg/libsignalgo/signalversion/version.go`: that is the libsignal tag `vA.B.C` everything
else follows. Note whether it moved.

### 2. Port the drift

`-check` lists new or changed exported API. For each difference:

- a new cgo file got a generated stub (`x_purego.go`) returning `ErrNotImplemented`: implement it
  by hand on libsignal-go (the generator leaves hand-written files alone once its marker is gone);
- a changed signature in a hand-written `x_purego.go`: port the change;
- new libsignal behaviour behind an unchanged API (error codes, serialized forms): compare with
  the Rust bridge in the new libsignal tag.

If the port needs something libsignal-go doesn't have, add it there first (step 3).

Then run the shim's tests in both builds, including `TestCrossBackend` (regenerate its
fixtures with `LIBSIGNALGO_WRITE_FIXTURE=1` only when the serialized forms changed on purpose),
`TestZKGroupAPI`, `TestGroupSendEndorsementShim` and `TestUnacknowledgedSessionClock`:

```sh
CGO_ENABLED=0 go test -tags libsignal_go ./pkg/libsignalgo/...
go test ./pkg/libsignalgo/...          # cgo; needs libsignal_ffi.a for vA.B.C
```

### 3. Re-pin libsignal-go (only if the libsignal tag moved)

In a `cwbudde/libsignal-go` checkout:

```sh
scripts/update-upstream-pin.sh vA.B.C
```

It bumps the tag mentions, updates the lockfile, regenerates the vectors and runs the vector,
report and interop tests. By hand (ADR 0007): set `rust-toolchain.toml` to the libsignal tag's
`rust-toolchain`, and the direct `spqr` and `libcrux-ml-kem` pins in
`compat/rust-harness/Cargo.toml` to what the tag's workspace `Cargo.toml` pulls in. Port what
the regenerated vectors or the interop tests show changed, run `scripts/fuzz.sh` briefly, tag
the next `-cw.N` and push. Then pin it in the mautrix fork
(`go get github.com/cwbudde/libsignal-go@vX.Y.Z-cw.N && go mod tidy`) and rerun step 2's tests.

### 4. Tag the mautrix fork

Tag `vX.YYMM.Z-purego.1` on `purego` and push the branch and the tag. The fork's CI
(`.github/workflows/purego.yml`) runs the stub check and the purego build and tests.

### 5. Bump go-signal

```sh
go get github.com/cwbudde/mautrix-signal@vX.YYMM.Z-purego.1
go mod tidy
```

go.mod must not get a `replace` (it breaks `go install`).

Move the submodule to the libsignal tag (`go run -tags libsignal_go . version` prints it as
`libsignal:`):

```sh
git -C third_party/libsignal fetch --depth 1 origin tag vA.B.C
git -C third_party/libsignal checkout vA.B.C
git add third_party/libsignal
```

Stage the submodule before running any `just` recipe: `check-libsignal` runs
`git submodule update`, which resets an unstaged checkout to the recorded commit.

Fix whatever signalmeow's API changes broke in `internal/signal` (the facade keeps them there).

### 6. Test

```sh
just check-libsignal && just libsignal   # cgo library for the new tag (Rust build)
just check                               # fmt, lint, cgo tests, tidy
just check-purego                        # the default backend: vet, lint, tests, AES assembly
just test-fork                           # the pinned forks' own tests
just test-diff                           # cgo vs pure Go, backend switch, zkgroup integration
just test-integration                    # live, on the test account (docs/dev.md, "Integration tests")
```

The live suite is the last word on a bump: it is the only test against Signal's servers. Run it
before tagging a release that contains the bump.

Bumping the submodule changes the CI cache key, so the first `test-cgo` run afterwards does a
full Rust build. The release workflow doesn't build Rust at all.
