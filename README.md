# knapbb

Deterministic, checkpointable branch-and-bound solver for the bounded
integer knapsack problem (Go 1.11+).

## Design

- **Node priority**: max-heap ordered by upper bound (fractional
  knapsack relaxation, floored), ties broken by lexicographically
  smaller path encoding (`Node.Path` = chosen copy counts per item).
- **Checkpoint**: `Solver.Save` serializes the *entire frontier* plus
  the incumbent (`BestValue`/`BestPath`) and the explored-node count as
  JSON; `Load` restores the heap verbatim. Because the search is fully
  deterministic, a resumed run pops exactly the same nodes in the same
  order as an uninterrupted one — no node is repeated or skipped.
- **Budget**: `Solver.Run(budget)` pops at most `budget` nodes. If the
  frontier is not empty it returns `Done=false` with `LowerBound`
  (incumbent) and `UpperBound` (max frontier UB) bracketing the optimum.

## Verification

`go test ./...` runs:

- `TestResumeMatchesUninterrupted` — 30 random instances; per-instance
  pop-order traces of the uninterrupted run and a run checkpointed and
  resumed every 5 nodes must be identical, as must explored counts and
  incumbents.
- `TestOptimalityVsExhaustive` — 40 instances cross-checked against an
  independent exhaustive enumeration of all count vectors.
- `TestBudgetExhaustionBounds` — budget-limited runs must bracket the
  exhaustive optimum, and resuming from the exhaustion checkpoint must
  reach it.

`go run ./cmd/knapdemo` prints a side-by-side comparison of the three
approaches on one instance.
