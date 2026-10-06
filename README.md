# bnb — branch-and-bound knapsack with checkpoints

A small Go library solving the **bounded integer knapsack** problem

	maximize  sum values[i]·x[i]
	subject to sum weights[i]·x[i] <= capacity,  0 <= x[i] <= bounds[i], x integer

with a resumable, fully deterministic branch-and-bound search.

## Design

- **Node priority** — the frontier is a priority queue ordered by relaxation
  upper bound (descending); ties are broken by the node's *path encoding*
  (lexicographic on the fixed-variable prefix). The order is total, so the
  search order is fully deterministic.
- **Bounds** — each node's upper bound is the floored fractional-knapsack
  relaxation over its free variables (items taken in exact value/weight
  ratio order). Nodes whose bound cannot beat the incumbent are pruned.
- **Checkpoints** — `Solver.SaveCheckpoint` serializes the *entire frontier*
  (every live node's path) plus the incumbent and the node counter.
  `LoadCheckpoint` rebuilds the solver, recomputing weights/values/bounds
  from the stored paths. Because the priority order is total, a restored
  solver pops nodes in exactly the same order as the original: no
  duplicated and no missed nodes.
- **Budgets** — `Solve(Options{Budget: n})` expands at most `n` nodes. On
  exhaustion it returns `StatusBudgetExhausted` with a valid
  `LowerBound` (incumbent) and `UpperBound` (max of incumbent and all
  frontier bounds) bracketing the optimum. Call `Solve` again — or reload
  from a checkpoint — to continue exactly where it stopped.

## Verification

`go test ./...` checks, over many random instances:

- **uninterrupted vs. resumed** — a run chopped into budget chunks with a
  full checkpoint round-trip (or a simulated crash restoring the last
  mid-run checkpoint) expands exactly the same number of nodes and finds
  exactly the same result as the uninterrupted run;
- **optimality claims** — the solver's proven optimum equals an
  independent brute-force `Exhaustive` enumeration that shares no code
  with the solver;
- **budget bounds** — budget-exhausted runs return bounds that genuinely
  bracket the exhaustive optimum.

## Demo

	go run ./cmd/bnbdemo -seed 7 -n 12 -chunk 5 -crash-every 3

runs one instance three ways — uninterrupted, checkpoint/resume between
budget chunks, and crash-simulation with mid-run checkpoints — then
verifies the claimed optimum by exhaustive enumeration, exiting non-zero
on any disagreement.
