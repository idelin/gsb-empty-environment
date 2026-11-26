# remoteattest

Raw, byte-level TPM 2.0 `TPMS_ATTEST` quote decoding and Linux IMA binary
measurement-log replay for a Go remote-attestation verifier. The package uses
**only the Go standard library** — all TPM wire structures are decoded by hand
and ECDSA-P256 verification uses `crypto/ecdsa`. No external TPM SDK is
imported.

## Public API

- `DecodeAttestation(raw []byte) (*Attestation, error)` — big-endian
  `TPMS_ATTEST` decoder. Validates magic `0xFF544347`, type
  `TPM_ST_ATTEST_QUOTE` (`0x8018`), the `TPMS_PCR_SELECTION` bitmap and the
  32-byte SHA-256 `pcrDigest`. Truncation and trailing bytes are rejected.
- `DecodeECDSASignature(raw []byte) (*Signature, error)` — fixed
  ECDSA-P256 `TPMT_SIGNATURE` body (`sigAlg=0x0010`, `hashAlg=0x000B`,
  `r[32] || s[32]`).
- `VerifyAKChain(...)` — X.509 chain validation of the AK certificate against
  the configured trusted roots; requires an ECDSA-P256 AK key.
- `VerifyQuote(...)` — full quote verification: nonce binding
  (`extraData == expectedNonce`, constant time), AK chain trust,
  `qualifiedSigner` binding to the compressed AK point, and ECDSA verification
  over `SHA-256(attestBlob)`.
- `ParseIMALog(log []byte)` / `ReplayIMALog(log []byte)` — little-endian IMA
  event stream parsing for `ima-ng` and `ima-sig`, template-hash
  recomputation, ToMToU detection and PCR replay.
- `PCRDigest(bank, selection)` — concatenates selected PCRs in ascending index
  order and hashes them; a missing selected PCR is a hard error.
- `Verify(cfg *Config) (*Report, error)` — end-to-end orchestration returning a
  structured `Report`; policy failures are `*PolicyError` carrying `Finding`s.

## IMA binary entry layout

```
pcr            4 bytes, little-endian (IMA uses 10)
templateHash   20 bytes SHA-1
templateName   NUL-terminated ASCII: "ima-ng" | "ima-sig"
d-ng field     u16 BE length, "sha256\0", 32-byte file digest
sig field      (ima-sig only) u16 BE length, raw signature
path           NUL-terminated
```

Template hash recomputation follows the kernel rule that the leading `d-ng`
digest field is skipped: `ima-ng` hashes `path + "\0"`, `ima-sig` hashes
`signature || path + "\0"` with SHA-1. A claimed template hash that differs
from the recomputed value fails the entry as a forged record
(`ErrTemplateHashMismatch`).

## Replay and ToMToU handling

PCRs start all-zero and are extended in log order:

```
PCR_new = SHA256(PCR_old || entryDigest)
```

`entryDigest` is `SHA-256` over the same template bytes whose SHA-1 is the
template hash. An all-zero template hash **or** all-zero file digest is a
ToMToU/open-writers violation: the entry is extended with `0xFF..FF` and the
platform is marked insecure via a `TOMTOU_ZERO_HASH` finding regardless of
signature validity.

## Defeated attacks

- **Quote/nonce replay** — `extraData` must equal the fresh verifier nonce.
- **Tampered event log** — recomputed template hash and the whitelist baseline
  reject altered claimed hashes and hidden file digests.
- **ToMToU cover-up** — zero hashes are flagged and extended as `0xFF..FF`.
- **PCR bitmap shifting** — `RequiredPCRs` (default PCR 10) must be covered by
  `pcrSelect`, and selected PCRs missing from the reconstructed bank hard-fail
  instead of being treated as zero.
- **Untrusted/forged AK** — X.509 chain trust, `qualifiedSigner` binding and
  ECDSA signature verification are all enforced.
- **Forged ima-sig payload** — the file signature must verify under a trusted
  ECDSA-P256 key.
