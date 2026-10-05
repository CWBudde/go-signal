# External security review scope

On 2026-10-06, the project owner decided to defer commissioning an external review
of the zkgroup and attestation ports. No reviewer or budget has been selected.
Revisit this decision when a reviewer and budget can be chosen; the scope below is
prepared for that discussion. The internal source review and compatibility tests
are not an independent security audit.

The proposed scope covers the pure-Go zkgroup and attestation ports, their shim
adapters and their use by signalmeow. It should be pinned to immutable source commits
when a review is commissioned. The current cryptographic baseline is
`github.com/cwbudde/libsignal-go v0.7.1-cw.5`, with Rust reference
`third_party/libsignal` at `v0.102.2` (`8fc2113bda042fc972a166a24b974b0d34155c6c`).
The signalmeow adapter version is recorded in `go.mod`; record its exact commit too.

- **zkgroup:** libsignal-go `poksho/`, `zkcredential/`, `zkgroup/` (including
  `zkcrypto/`) and `internal/ristrettolizard/`. Examine proof transcripts, scalar
  and point handling, verification versus structural parsing, versioned encodings,
  randomness, expiry, ACI/PNI and profile-key encryption, endorsement proofs and
  recipient-set binding.
- **Attestation:** libsignal-go `attest/dcap/`, `attest/enclave/`,
  `attest/hsmenclave/` and `noise/`. Examine hostile evidence/collateral parsing,
  certificate chains, pinned roots, signatures and CRLs, signed JSON, measurement,
  advisory/TCB/time policy, claims-to-handshake binding, transport authentication,
  nonces and failed-state handling.
- **Adapters and callers:** mautrix-signal `pkg/libsignalgo/` pure-Go adapters and
  their cgo counterparts, plus group, profile, CDSI and endorsement consumers in
  `pkg/signalmeow/`. Check time units, caller randomness and authentication
  assumptions. Include release build settings and arithmetic dependencies.

The evidence packet should include [the internal review](constant-time-review.md),
the fork's package constant-time notes, committed compatibility vectors and Rust
harness, attestation unit/vector/fuzz tests, shim and differential tests, and
`scripts/test-zkgroup-integration.sh` / `scripts/test-cdsi-integration.sh`.
[Development](dev.md#pure-go-backend) and [maintenance](maintenance.md) document
the verification commands and dependency pins. Record fresh results at the review
baseline; historical passing results do not cover later changes.

The internal review did not establish statistical timing behavior, cross-platform
machine-code guarantees or comprehensive secure erasure. The offline CDSI fixture
uses a narrowly scoped test-only TCB evaluation-number-12 exception; production's
minimum remains 21. Offline fixtures do not establish live acceptance.

SVR2 raft/config/minimum-limit validation remains outside the implemented CDSI
port; `attest_svr2_bad_config` is still open if SVR2 becomes needed. A focused port
review does not cover Signal servers or enclave implementations, the complete
protocol design, unrelated application logic or ratchets, or a Rust-backend audit.
Any additional scope should be agreed with the reviewer before commissioning.
