# featureneg

Two-party capability negotiation in pure Go (standard library only).

## Model

Each party declares an `Offer`:

- `Protocol`: supported protocol version range (inclusive).
- `Required` / `Optional`: feature names that must / may be selected.
- `Preference`: optional features ordered by preference.
- `Features`: per-feature supported version range, dependencies
  (`Requires`, with a minimum-version condition on the target) and
  mutual-exclusion edges (`Conflicts`).

## Negotiation

`Session.Negotiate(budget)`:

1. Fails if the protocol version ranges have an empty intersection.
2. Fails if any required feature is not supported by both parties at a
   common version.
3. Otherwise searches the optional-feature space **globally** for the
   highest mutually satisfiable combination — maximum preference weight,
   then maximum selected versions. Dependencies, conflicts and version
   conditions couple the choices, so a per-feature local intersection is
   not sufficient. Dependency targets are pulled in implicitly.
4. Returns one of three statuses:
   - `OK` with a complete selection `Certificate`;
   - `Conflict` with rejection `Reasons` and a minimal conflicting
     condition subset (`ConflictCore`, deletion-minimized);
   - `Undecided` when the search budget is exhausted — reported
     separately from real conflicts, with the best-so-far selection.

## Transcript binding

The session transcript hashes the session ID, retry counter, downgrade
flag and the canonical encoding of both offers. Certificates carry the
transcript; `Session.BindDecoder` rejects certificates from older
parameters (`ErrStaleCertificate`), so old results cannot decode data
negotiated under new parameters. `Session.RequestDowngrade` narrows the
local offer (required features must be preserved exactly, optional
features may only be removed) and bumps the retry counter, producing a
fresh transcript.

## Serialization

Self-defined limits (`serialize.go`): strings ≤ 4 KiB, lists ≤ 4096
entries, frames ≤ 1 MiB, format version 1. Decoding is pure data
handling — it never executes host code — and every declared length is
validated against the remaining input and the configured limit before
any allocation.

## Testing

`go test ./...` includes a small-domain exhaustive reference: for
hundreds of seeded random instances the solver result is checked against
brute-force enumeration of all optional-feature subsets, and reported
conflict cores are verified to be infeasible and minimal.

## Demo

`go run ./cmd/negdemo` runs a full round: negotiation, certificate-bound
decoding, a downgrade retry that invalidates the old certificate, and a
refused required-stripping downgrade.
