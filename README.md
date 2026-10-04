# versioned-vote-ledger

Offline-mergeable voting ledger with per-voter version merging and frozen
eligibility weights. Written in TypeScript (erasable syntax only), runs
directly on Node >= 22.6 via type stripping — no build step, no
dependencies.

## Model

- **Vote**: `{ issueId, voterId, version, replicaId, payload }` where the
  payload is a candidate choice, an abstention, or a withdrawal.
- **Version order**: votes are ordered by `(version, replicaId)`. The
  effective vote of a voter is the maximum under this order; higher
  versions replace older ones, and same-version cross-replica ties break
  by replica id.
- **Withdrawal is a version**: a withdrawal tombstone competes in the same
  order, so a stale older ballot can never resurrect a withdrawn vote.
- **Conflicts**: two votes with the same full version identity
  `(issue, voter, version, replica)` but different contents raise
  `VoteConflictError` — the ledger never silently picks one.
- **Idempotency**: re-importing identical votes or re-merging replicas is
  a no-op; merges converge regardless of order and repetition.

## Eligibility

Weights live in an independent, append-only `EligibilityTable`. Each issue
pins an `eligibilityVersion`, so later weight adjustments never rewrite
historical results. Tallying against an unpublished version throws
`EligibilityVersionNotFoundError`. Re-publishing a version with different
content throws `EligibilityConflictError`.

## Tally rules

- Abstentions count toward the participated weight but not any candidate.
- Votes from ineligible voters (or for unknown candidates) are kept as
  rejected records with a reason, and count toward nothing.
- Output: exact integer (`bigint`) weight per candidate, total
  participated weight, and the winner. Candidate ties break by candidate
  id. If the participated weight is below the issue's participation
  threshold, there is no resolution.

## Recomputer

`recomputeTally` (src/recompute.ts) independently reduces a raw vote
collection from scratch — max `(version, replicaId)` per voter, then
counting — and `sameTally` compares results, letting you cross-check the
incremental ledger against a from-scratch recomputation.

## Usage

```sh
npm test                              # node --test (requires Node >= 22.6)
node src/cli.ts tally examples/tally.json
node src/cli.ts verify examples/tally.json   # ledger vs recomputer
cat examples/tally.json | node src/cli.ts tally -
```

CLI input is a single JSON document: `{ issue, eligibility, votes }`.
Weights and thresholds are decimal strings (arbitrary precision); the
tally output prints them as strings to preserve exactness.

## Layout

- `src/types.ts` — vote/issue/tally types and the version order
- `src/eligibility.ts` — versioned eligibility table
- `src/ledger.ts` — mergeable ledger, conflict detection, tally
- `src/recompute.ts` — independent recomputer and tally comparison
- `src/cli.ts` — `tally` / `verify` commands
- `test/ledger.test.ts` — node:test suite (late withdrawal, missing
  eligibility version, cross-replica ties, all-abstain, big-integer
  weights, idempotent merge, conflicts, thresholds)
