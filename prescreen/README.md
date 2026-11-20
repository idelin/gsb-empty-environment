# prescreen

Importable read-admission component for the Go cold-archive host
(module `prescreen.local/ddd-10497-v4`, package path
`prescreen.local/ddd-10497-v4/prescreen`).

## Entry points

- `Evaluate(policy, request, capabilities) (*ReadTicket, error)` admits a
  signed batch read and returns a block-number-free read ticket.
- `VerifyTicket(policy, request, capabilities, ticket)` is the independent
  consumer-side check: it re-resolves the signed view chain, re-verifies every
  Ed25519 signature, recomputes rank/inclusion-exclusion algebra and compares
  affine sets semantically rather than comparing block counts.
- `SignView`, `SignCapability`, `SignRequest` produce the real Ed25519
  signatures over deterministic canonical encodings.

## Algebraic building blocks

- `Vec`, `Eq`, `Affine`: 48-bit GF(2) vectors, affine equations and canonical
  affine subspaces (`SolveAffine`, `Affine.Equations`, `Intersect`, `Equal`).
- `AffineMap`: affine block maps with explicit `Rank`, `Apply` (re-ranks the
  image so aliases count once) and `Compose` for view chains.
- `UnionInclusionExclusion`, `CoverUnion`: exact union cardinality and
  coverage over at most `MaxSlices` (=12) subspaces, bounded to 2^12-1 terms;
  block numbers are never enumerated.

## Security semantics

- Every view manifest and capability signature must verify under a
  `Policy.Roots` key; otherwise admission fails with `ErrUntrustedRoot`.
- View edges bind the exact parent object version. A version mismatch returns
  `ErrVersionMismatch`; a repeated object in a chain returns `ErrViewCycle`.
- A physical image must be covered by the same subject's valid grant union
  (`ErrNotCovered`) and stay disjoint from every valid revocation slice
  (`ErrRevoked`). Rank-deficient maps and disjoint logical slices aliasing the
  same physical block are counted by actual distinct blocks.

## Tests

- `gf2_test.go`, `alias_test.go`: 6-bit brute-force reference oracle
  (`enumerate`) cross-checked against the algebraic implementation.
- `admission_test.go`: multi-layer rank drop, physical aliasing, equal-size
  different-set rejection, same-set different-equation admission, single-point
  revocation and overlapping grant unions.
- `reject_test.go`: missing root, bad/forged signatures, cycles, version
  mismatch, foreign subject, slice limits and certificate tampering.
- `bigspace_test.go`: 48-bit spaces up to 2^48 blocks completing in
  milliseconds, demonstrating there is no per-block enumeration.
