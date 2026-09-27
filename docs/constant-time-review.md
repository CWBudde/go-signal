# Pure-Go timing and secret-lifetime review

Reviewed 2026-09-27 for PLAN.md §10.2. **The review is complete; remediation is
not.** The current backend does not support an unconditional constant-time or
secure-erasure claim. CT-01 and CT-02 need fixing before the default switch.
CT-03 is defense in depth.

## Remediation status

| Finding | Status                                                                                                                                      |
| ------- | ------------------------------------------------------------------------------------------------------------------------------------------- |
| CT-01   | Fixed in libsignal-go `v0.7.1-cw.5` (`478422d0e`), pinned through mautrix-signal `v0.2609.0-purego.6`.                                      |
| CT-02   | Open. Only the GCM-SIV package comment is corrected (`e3aa3bf3e`, in `cw.5`); the backend tag, CPU policy and release builds are unchanged. |
| CT-03   | Fixed in `cw.5` (`d6f6f7409`, `8e218f057`), pinned through `purego.6`.                                                                      |

The findings below describe the reviewed `cw.4` baseline. The fork's
`docs/constant-time.md` records each fix, its tests and the disassembly check.

## Scope and method

The shipped baseline is go-signal `2e3b89e96a0a0455eb2d91e318057f37fbe267ba`,
libsignal-go `v0.7.1-cw.4` (`2a6c29369d5f46b6186d8c8a3c028cff0fa7d678`), and
mautrix-signal `v0.2609.0-purego.5`. The sibling libsignal-go checkout at
`b29cd11750cfb00cb52b9a7995531d596363660d` was also compared with that release:
its fuzzing additions and SPQR malformed-state guards do not fix these findings.
No dependency or production code was changed for this review.

Dependencies inspected at the call boundaries and relevant arithmetic helpers:
Go 1.26.0, x/crypto v0.57.0, CIRCL v1.6.4, edwards25519 v1.2.0, and ristretto255
v0.2.0. This covers the fork's secret-bearing crypto paths, the purego shim's
ownership/lifetime behavior, and go-signal's HPKE and device-name crypto. It is
not a fresh audit of all signalmeow application logic, the Rust backend, every
dependency assembly implementation, or the protocol's cryptographic design.

The review traced branches, loop bounds, indexing, comparisons, parsing,
authentication order, and secret copies. Existing zkgroup review notes were
checked against their implementation. Two suspect paths were additionally
inspected in optimized linux/amd64 machine code, with `CGO_ENABLED=0`,
`-tags purego` and `GOAMD64=v1`. No statistical timing experiment, practical remote exploit, or
cross-platform machine-code proof was performed. Passing vectors and fuzzers
does not establish constant-time execution.

Secrets include private scalars, signing nonces, KEM noise/messages and shared
secrets, ratchet keys, profile/group keys, credential witnesses, bearer tokens,
PINs and account entropy. Public inputs include lengths, protocol versions,
counters, key IDs, ciphertexts, signatures and attestation evidence. Lengths and
protocol state are not hidden by these APIs. A value is not public merely
because it is serialized into the local account database.

## Findings and required follow-up

### CT-01 — High priority: SPQR branches on secret encapsulation state

[`internal/mlkem768incr/incremental.go:429`](https://github.com/cwbudde/libsignal-go/blob/v0.7.1-cw.4/internal/mlkem768incr/incremental.go#L429),
`FixEncapsStateEndianness`, scans the secret `e₂` noise coefficients until the
first value other than 0 or -1. It then branches on that value and either returns
the original state or allocates and swaps a copy. For two correctly encoded
states of identical size, `e₂ = [1, …]` examines one coefficient, whereas
`e₂ = [0, -1, 1, …]` examines three. These are valid noise values, not just
malformed storage. The deciding position and coefficient class affect control
flow even when both outputs keep the same byte order.

`Encapsulate2` calls this helper on every completion, including states generated
by Go. The path is `spqr/v1.go:toCt2Sampled` → `Encapsulate2` →
`FixEncapsStateEndianness`. `session/cipher.go:decryptWithState` calls
`PQRatchetRecv` before verifying the outer message MAC. SPQR has its own header
authentication and public-key consistency checks, but the outer MAC does not
exclude this computation. Failed receive attempts operate on cloned state;
failure does not necessarily consume the secret being inspected.

Optimized Go 1.26.0 amd64 disassembly confirms a coefficient load followed by
conditional jumps for its sign/value, a data-dependent loop exit, and a separate
allocation path. This is a demonstrated control-flow leak, **not a demonstrated
key-recovery attack**. Remote observability and useful information accumulation
would need separate investigation.

The adjacent `toBalanced` and `fromBalanced` helpers also express conditional
operations on secret coefficients; the latter uses signed `% q`. These need
explicit constant-time treatment and target-specific code-generation checks,
even if a particular compiler lowers a conditional to a conditional move.

Required fix in libsignal-go: scan all 256 `e₂` coefficients, select the first
decisive classification with masks, and apply a masked swap over all 1024
coefficient pairs with the same allocation/copy shape. Preserve the upstream
first-decisive-value rule, ambiguous/unexpected-value fallback, and untouched
trailing 32-byte message. Replace balanced conversions with bounded arithmetic
whose generated code has no secret branches or variable-latency division.
Release the fix and update go-signal's pin.

Validation needed: correct/swapped states with every decisive position and value
class; all-ambiguous states; unexpected values before/after a decision; unchanged
input and trailing message; all 3329 canonical and all 65536 signed-int16
conversion inputs; existing incremental Rust oracle, ML-KEM ACVP and backend
switch tests. Inspect optimized amd64 and arm64 output after the fix. Output
equivalence tests alone cannot catch the early-exit regression.

### CT-02 — High priority: the purego tag disables constant-time hardware AES

The fork's CBC, CTR, GCM and GCM-SIV paths, profile access-key derivation,
go-signal's HPKE and device-name decryption delegate to `crypto/aes`.
Go 1.26.0's `crypto/internal/fips140/aes/aes_generic.go` indexes round and S-box
tables with secret-dependent values. More seriously, the backend's global
`-tags purego` also selects Go's `aes_noasm.go` and excludes `aes_asm.go`, whose
constraint includes `!purego`. This **forces generic AES even on a CPU with
AES-NI or ARM AES support**. Confirmed with `go list -tags purego` for the actual
build: `aes_noasm.go` is present and the assembly-file list is empty. GCM also
selects `gcm_noasm.go`.

Without that tag, `aes_asm.go` chooses the accelerated path using runtime CPU
capabilities. Even then an amd64/arm64 target alone does not prove that accelerated
AES is active; a VM or disabled CPU feature can select fallback code.
The Go [AES documentation](https://pkg.go.dev/crypto/aes) explicitly
qualifies its constant-time behavior by enabled hardware support.

The fork's `internal/crypto/gcmsiv/gcmsiv.go` package comment describes the
standard-library AES as constant-time/hardware AES without this condition.
POLYVAL's fixed loops and masked carry-less multiplication do not make the AES
part constant-time. The older zkgroup review does acknowledge hardware limits.

Before the default switch, rename the backend-selection tag in go-signal and
the mautrix fork to avoid the standard/dependency `purego` convention, retaining
`CGO_ENABLED=0` for the no-cgo build. Go assembly does not require cgo or Rust.
Then define and enforce supported CPU conditions or adopt a reviewed constant-time
AES fallback. Renaming alone does not protect CPUs lacking AES acceleration.
Update CI, release recipes, docs and the GCM-SIV comment together, and check the
selected standard-library files in release builds. Do not infer a timing
guarantee from successful functional tests with `GODEBUG=cpu.aes=off`; those tests
only exercise fallback correctness. Other dependencies also recognize `purego`,
so inspect their effective build files when changing the tag.

### CT-03 — Defense in depth: CBC padding check returns early

[`internal/crypto/aescbc.go:86`](https://github.com/cwbudde/libsignal-go/blob/v0.7.1-cw.4/internal/crypto/aescbc.go#L86),
`pkcs7Unpad`, returns immediately when the decrypted final byte is 0 or greater
than 16. Other invalid padding values go through a full-block scan. Its comment
promises constant-time padding validation, which the implementation does not
provide. Optimized amd64 output retains both early-return branches.

All three current callers of the fork's `DecryptCBC` authenticate first:
`session/cipher.go` checks the message MAC, `groups/cipher.go` verifies the sender
signature, and `usernames/usernames.go` checks the link HMAC. This review did
**not** establish an unauthenticated network padding oracle through those paths.
The helper is internal to the module. Authentication ordering must be preserved.

Required fix in libsignal-go: incorporate the 1..16 range check into a validity
mask, visit every byte of the final block, then branch once on overall validity.
Use nonnegative operands where required by `crypto/subtle`; blindly retaining
`blockSize-pad` for out-of-range padding can violate those preconditions. Test
every final-byte value and mismatches at each padding position, then inspect
generated code. Also clear the owned decrypted buffer on failure as best-effort
memory hygiene. Keep the existing CBC vectors and caller authentication tests.

## Other paths examined

| Surface                         | Assessment and boundary                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                          |
| ------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| X25519 / XEdDSA                 | Agreement delegates to x/crypto's standard-library X25519 wrapper. Signing uses fixed-width reduction, `ScalarBaseMult` and `MultiplyAdd`; no variable-time signing multiplication was found. `VerifySignature` uses `VarTimeDoubleScalarBaseMult` on signature/challenge/public-key inputs. This is appropriate for public verification inputs, not a promise to conceal a private message supplied to the verification API.                                                                                                                                                                                                                                                                    |
| PQXDH / Kyber1024               | CIRCL decapsulation decrypts, reencrypts and uses `ConstantTimeCompare`/`ConstantTimeCopy` for implicit rejection. The inspected coefficient compression uses multiply/shift arithmetic rather than division by a secret value; noise sampling has fixed loops. Uniform matrix rejection sampling depends on the public matrix seed. No separate secret-dependent rejection branch was found in this adapter.                                                                                                                                                                                                                                                                                    |
| Incremental ML-KEM-768          | NTT indices and bounds are fixed; field reduction/compression and CBD noise sampling use arithmetic masks. Decapsulation computes both candidate/fallback secrets and selects with constant-time primitives. Expanded-key parsing can reject invalid local encodings; valid reduced coefficients take the same checks. CT-01 concerns the surrounding state codec, not this implicit-rejection selection.                                                                                                                                                                                                                                                                                        |
| Ratchets / session / groups     | KDFs use HMAC/HKDF on fixed-size key material. Message MACs use constant-time comparisons. Session selection, skipped-key searches, counters, replay detection and state transitions are variable-time on public metadata and local history; the API does not conceal whether a session or cached message key exists. `session/state.go:bytesEqual` compares public ratchet keys, not root/chain keys.                                                                                                                                                                                                                                                                                           |
| SPQR transport                  | Authenticator tags use constant-time comparisons. GF16 erasure-code multiplication/reduction has branches, but its call sites encode public encapsulation keys, ciphertext parts and transmitted MACs, not decapsulation keys, noise state or shared secrets. Keep that restriction; these helpers are not general constant-time secret-sharing arithmetic.                                                                                                                                                                                                                                                                                                                                      |
| Sealed sender                   | v1 verifies CTR-HMAC before returning decrypted content. v2 checks the rederived ephemeral public key, GCM-SIV tag and sender authentication tag using constant-time comparisons. Branches reveal success/failure of these protocol checks. KDF and XOR loops depend on fixed sizes. AES retains CT-02.                                                                                                                                                                                                                                                                                                                                                                                          |
| POKSHO / zkcredential / zkgroup | Proving and verification retain constant-time multiscalar multiplication, including potentially secret points. The edwards25519 radix-16 table selection scans entries using conditional selection; canonical scalar range checking uses a fixed subtraction chain. Lizard visits eight inverse candidates; profile decryption visits all 8×8 candidates and conditionally copies matches. UID decoding checks both kinds before final selection; its returned kind and decryption success are not concealed. Canonical point parsing and proof validity can reject. Endorsement sorting is over ciphertext points visible to the group service; token comparisons use constant-time primitives. |
| Noise / CDSI / HSM              | X25519, standard-library ML-KEM-1024, HMAC-SHA256 and ChaCha20-Poly1305 supply the secret arithmetic. Pattern/role, message size, chunk count, state and nonce-limit branches are public. AEAD failures return no plaintext; later-chunk failure can leave earlier plaintext in abandoned internal buffers.                                                                                                                                                                                                                                                                                                                                                                                      |
| Attestation                     | Certificate, quote, CRL, measurement and timestamp checks handle public evidence, so early exits, `bytes.Equal` and `math/big` signature parsing here are not private-key timing defects. Attestation acceptance itself is observable.                                                                                                                                                                                                                                                                                                                                                                                                                                                           |
| HPKE / device names             | HPKE delegates to Go's X25519/HKDF/AES-GCM suite. Framing branches use public lengths/type bytes. Device-name synthetic-IV authentication uses `hmac.Equal`; failed plaintext is not returned. AES retains CT-02; temporary plaintext/key copies are not comprehensively wiped.                                                                                                                                                                                                                                                                                                                                                                                                                  |
| Account keys / PINs             | HKDF calls have public requested lengths. Local PIN verification uses Argon2i and constant-time hash comparison. The separate SVR PIN derivation uses Argon2id, which intentionally includes data-dependent memory accesses; do not claim that path has data-independent access. Preserve protocol parameters.                                                                                                                                                                                                                                                                                                                                                                                   |
| Usernames / account entropy     | Username parsing, character conversion, formatting and candidate deduplication are variable-time on potentially private names; they are outside a constant-time name-privacy guarantee. `math/big` is used for bounded random candidate sampling, not private scalar arithmetic. Account-entropy generation rejection-samples random bytes and indexes a 36-byte alphabet by the accepted value; the rejection count is independent of the accepted residue, but the table access is secret-dependent. Local generation/parsing is not certified constant-time. A stronger local side-channel requirement needs arithmetic alphabet mapping and a review of serialization too.                   |
| Device-transfer keys            | RSA key generation and private-key DER parsing/serialization are not constant-time operations. Certificate signing delegates to Go's RSA/ECDSA implementations; this review does not extend their guarantees to the entire generation/serialization lifecycle.                                                                                                                                                                                                                                                                                                                                                                                                                                   |

The earlier reviews remain useful for detail:
[POKSHO](https://github.com/cwbudde/libsignal-go/blob/v0.7.1-cw.4/poksho/CONSTANT_TIME.md),
[zkcredential](https://github.com/cwbudde/libsignal-go/blob/v0.7.1-cw.4/zkcredential/CONSTANT_TIME.md),
[zkcrypto](https://github.com/cwbudde/libsignal-go/blob/v0.7.1-cw.4/zkgroup/zkcrypto/CONSTANT_TIME.md),
and [zkgroup](https://github.com/cwbudde/libsignal-go/blob/v0.7.1-cw.4/zkgroup/CONSTANT_TIME.md).
Their source-level assessments do not certify the rest of the backend.

## Zeroization posture

**There is no comprehensive or guaranteed zeroization.** Garbage collection,
dropping references, closing the account database, removing a session, and
calling a shim `Destroy` method do not establish physical erasure.

| Owner / operation                                                                               | Actual behavior                                                                                                                                                                                                                                                                                                                                                  |
| ----------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Purego shim private keys, Kyber keys, prekey/session/sender-key records and AES-GCM-SIV objects | Many `Destroy` methods simply return nil. For example, `privatekey_purego.go:57` leaves the key field intact and `Serialize` remains usable afterward. `CancelFinalizer` is generally a no-op. These are API compatibility methods, not erasure operations.                                                                                                      |
| SGX/HSM shim                                                                                    | `Destroy` drops the client/state pointer. It prevents subsequent wrapper use but does not overwrite the underlying Noise state or key objects.                                                                                                                                                                                                                   |
| Noise handshake                                                                                 | `Transport` clears the inline symmetric-state struct and drops ephemeral/static/KEM pointers after splitting. AEAD objects, key objects and temporary KDF buffers can survive until collection; nil assignments do not wipe their allocations. Failed handshakes also have no comprehensive erasure path.                                                        |
| Ratchet state / serialization                                                                   | Key accessors, protobuf marshaling, state cloning and database writes create copies. `spqr/chain.go:keyHistory.clear` shortens the slice without wiping its backing array. Removing old keys establishes logical deletion, not memory erasure.                                                                                                                   |
| Existing explicit wipes                                                                         | POKSHO clears its temporary nonce-derivation byte buffer. GCM-SIV overwrites its owned plaintext buffer after tag failure. GCM-SIV's separate tag-input buffer still contains a plaintext copy; neither wipe covers all hash/scalar/AES objects or compiler copies. `mlkem768incr/field.go:clear(b)` initializes output bits and is not secret-lifetime cleanup. |
| Persistent account data                                                                         | Keys and sessions are intentionally serialized to SQLite. Application file permissions and account locks are not encryption or secure deletion. SQLite pages, journals/WAL, filesystem snapshots and backups are outside any heap-wipe claim.                                                                                                                    |
| Logging / formatting                                                                            | Some curve/ratchet types redact all formatting verbs. This is not universal: `AccountEntropyPool.String` returns the actual entropy, and secret `Bytes`/`Serialize` results are ordinary byte slices. Do not format entire secret-bearing objects or log serialized keys/state.                                                                                  |

Best-effort follow-up should define ownership first: wipe owned temporary key and
unauthenticated-plaintext buffers on all exits, avoid unnecessary clones and
strings, and give secret-owning objects explicit disposal/invalidation semantics.
Kyber objects and wrapper clones may share underlying storage; blindly clearing
one alias can corrupt another live owner. Add lifetime/alias tests when changing
these APIs. Even then, Go value copies, stack growth, compiler temporaries,
cryptographic dependency objects and durable storage prevent a blanket erasure
guarantee. `runtime.KeepAlive` alone is not a secure-erasure primitive.

## Evidence and repeatability

`just test-fork` exercises the pinned library vectors, purego shim, offline CDSI
handshake and mocked zkgroup integration. It passed for this review with local
test-server access. The initial sandboxed run reached the integration test but
could not bind its localhost listener; that was an environment failure.
The complete pinned libsignal-go suite also passed with the actual backend tag:
`CGO_ENABLED=0 go test -tags purego -count=1 github.com/cwbudde/libsignal-go/...`.

The following builds the exact pinned packages without editing either fork.
Inspecting optimized code, rather than `-N`/`-l` debug builds, matters:

```sh
CGO_ENABLED=0 GOAMD64=v1 go test -tags purego -c -o /tmp/ct-mlkem.test \
  github.com/cwbudde/libsignal-go/internal/mlkem768incr
CGO_ENABLED=0 GOAMD64=v1 go test -tags purego -c -o /tmp/ct-cbc.test \
  github.com/cwbudde/libsignal-go/internal/crypto
objdump -d --disassemble=github.com/cwbudde/libsignal-go/internal/mlkem768incr.FixEncapsStateEndianness /tmp/ct-mlkem.test
objdump -d --disassemble=github.com/cwbudde/libsignal-go/internal/crypto.pkcs7Unpad /tmp/ct-cbc.test
CGO_ENABLED=0 go list -tags purego -f '{{.ImportPath}}: {{.GoFiles}} {{.SFiles}}' \
  crypto/internal/fips140/aes crypto/internal/fips140/aes/gcm
```

In the first function, inspect the `movzwl` coefficient load and following
`jg`/`je`/`jge` jumps. In the second, inspect the final-byte `movzbl` followed by
the zero and `> 16` checks before the scan. Addresses vary with build settings.
These observations establish the two particular branch findings, not the timing
behavior of the entire program.

Revisit this review when the Go toolchain, CPU/build support, dependency pins,
secret-state codecs, authentication order, scalar operations or buffer ownership
change. Keep CT-01/02/03 remediation separate from the completed review checkbox.
