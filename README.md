# gcrt

Generalized Chinese remainder library for Go: solves systems of
congruences `x ≡ rᵢ (mod mᵢ)` where the moduli need **not** be coprime.
Residues may be negative and arbitrarily large (`math/big` throughout).

## Solving

`Merge` combines two congruences: they conflict iff `r₁ ≢ r₂ (mod gcd(m₁, m₂))`,
otherwise their intersection is a single class modulo `lcm(m₁, m₂)`.
`Store.Solve` returns the least non-negative representative and the period
(lcm of all moduli). The empty system solves to `x ≡ 0 (mod 1)` — all
integers. An inconsistent system reports the **earliest** contradictory
pair of original constraint IDs (a system is consistent iff every pair is).

## Versioned store

Constraints carry stable IDs and are added/removed inside transactions
(`Begin`/`BeginAt` → `Add`/`Remove` → `Commit`/`Rollback`). Every commit
produces an immutable, queryable snapshot. The store keeps all original
constraints — never just a merged residue — so deleting any constraint
re-derives the true solution set of the remainder.

## Checkpoints

`Store.Save`/`Load` persist each version's original constraint identities
plus the incremental solver state as JSON. `Load` re-derives every
version's solution with a full generalized merge and rejects checkpoints
whose persisted state disagrees.

## Tests

`go test ./...` — merging and whole-system solving are checked against
finite enumeration over one full period; snapshot/removal, rollback, and
checkpoint recovery are checked against from-scratch full merges.
