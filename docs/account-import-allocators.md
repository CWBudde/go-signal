# Account-import Stage 2a verification

Date: 2026-10-14. Status: Stage 2a complete.

This records Phase 16 Stage 2a, durable prekey allocation in the mautrix-signal fork. It
is an offline prerequisite: it does not add the converter, does not import an account and
establishes no phone/server acceptance. Stage 2b (storage-key lifecycle) is next. Design:
[importer spec](superpowers/specs/2026-10-10-account-import-design.md), "Durable key
allocation"; implementation and review rulings:
[plan](superpowers/plans/2026-10-11-account-import-allocators.md).

## What changed

- Fork schema v28 (compatibility v28) adds `signalmeow_prekey_allocators`: next EC, signed
  and Kyber positions plus active signed/last-resort references per account and local
  typed service ID. Existing ACI/PNI scopes are seeded once from retained maxima.
- Refill counts ordinary keys only and reserves each batch durably before generating it.
  EC/Kyber and ACI/PNI refills fail independently; failures are reported with the upload
  result.
- `SeedPreKeyAllocator` joins the importer's conversion transaction and validates scope,
  retained maxima and active references. `StoreProvisionedDevice` replaces a relinked
  account's credentials, keys, sessions and allocators in one local transaction;
  go-signal's `LinkStore` delegates to it.
- Owner rulings (2026-10-14): older releases refuse upgraded databases (no downgrade
  without starting the account over, [dev.md](dev.md#prekey-allocators-and-downgrades));
  missing allocator rows fail closed instead of falling back to `MAX+1`.

## Published version

| Module         | Version               | Commit                                     | Module checksum                                   |
| -------------- | --------------------- | ------------------------------------------ | ------------------------------------------------- |
| mautrix-signal | `v0.2609.0-purego.31` | `74fc1aa019c99cc0c1ae8efc5a73d0903e8398c1` | `h1:J7afHhceA82da/zg/+w5D+gfRvdE+jeQqDMzXr9Tpag=` |

Built on `v0.2609.0-purego.30` (`a3a2de4`) with three commits. The remote branch and
tag were verified against the reviewed commit, the fetched module's `pkg/signalmeow` tree
matches the reviewed checkout, and `go mod verify` passes. libsignal-go stays
`v0.7.1-cw.6`, native libsignal `v0.102.2`; no submodule changed. Go 1.26.0, Linux amd64.

## Verification

One independent read-only review checked the written plan before implementation, a second
one the whole change before publication; their findings are resolved in the plan and
code. Before publication, the fork's own gates passed (goimports `-local`, whitespace,
stubgen parity, pure-Go and native vet) with both full suites
(`./pkg/libsignalgo/... ./pkg/signalmeow/...`, pure-Go and native `-race`). Mutation checks
confirmed that the tests catch a missing exhaustion guard, a no-op record advance, a
non-persisting reservation, reservation-free refill and a dropped refill error.

Final commands use `GOWORK=off` against the published pin; every one exited 0:

| Command (`GOWORK=off`)                                                                      | Evidence                                                                              |
| ------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------- |
| `just fmt`                                                                                  | No formatter changes                                                                  |
| `just lint`                                                                                 | 0 issues                                                                              |
| `just check`                                                                                | Native race suite, lint, native pin and tidy checks (run on the committed tree)       |
| `just check-purego`                                                                         | Pure-Go vet, lint and suite                                                           |
| `just test-fork`                                                                            | Includes pure-Go `TestPreKeyRefill`, `TestPreKeyAllocator*`, `TestProvisionedPreKey*` |
| `just test-diff`                                                                            | Native and pure-Go `-race` allocator/provisioning tests and the backend runner        |
| `CGO_ENABLED=0 go test -count=1 ./...`                                                      | Untagged no-cgo suite                                                                 |
| `scripts/test-prekey-allocators.sh`                                                         | Both starting orders, 12 actions, exact ledger                                        |
| `CGO_LDFLAGS="-L $PWD/third_party/lib" scripts/test-account-import-compatibility.sh --java` | All 264 actions, 48 Java legs                                                         |

The allocator runner alternates native and pure-Go binaries on one database per order:
init, reserve, abort (the process exits right after a committed reservation), reserve,
reserve, check. The check replays the ledger and requires every reservation to start where
the previous one of its scope and kind ended, abandoned ones included; active references
and last-resort keys stay unchanged and all ordinary keys were consumed.

The Java run used the retained private JDK 25.0.2 / Maven 3.9.11 toolchain and cache. Its
ledger has 108 native, 108 pure-Go and 48 Java actions and is byte-identical to the
[Stage 1 ledger](account-import-compatibility-ledger.tsv) (SHA256
`8d058a94556bbd97a48b5205c08c2af1f207fdf0590dc55523056065bd4c34bb`), so the Java
continuation is unaffected.

## Limitations

- Upgrading is one-way (compatibility v28). Legacy migration uses retained maxima; IDs
  consumed and deleted before the upgrade cannot be reconstructed, and several retained
  signed/last-resort candidates leave the active reference unknown.
- Provisioning atomicity is local: the server registers the device first, so a failed
  local transaction requires linking again.
- Transaction detection sees only the same root database; the converter must seed through
  the account store's `Devices` container.
- A device with a nil stored PNI has no PNI scope and retries its PNI key check every
  5–30 minutes ([maintenance.md](maintenance.md#durable-prekey-allocators)).
- SQL stays PostgreSQL-compatible, but only SQLite (mattn and modernc) is exercised.
- Import headroom against the `0xffffff` limit is a converter decision. No live account,
  phone or server was used.
