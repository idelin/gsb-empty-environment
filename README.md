# groupopt

Budget-constrained selection with shared setup fees and dependencies, in Go.

## Model

- Up to `MaxOptions` (20) candidate options; option `i` consumes `Cost` from
  the budget and yields `Profit`.
- Options belong to groups. Selecting **any** option of a group charges that
  group's one-time **setup fee** — charged exactly once, no matter how many
  options of the group are selected.
- `Option.Requires` lists ids that must also be selected; feasible selections
  are **closed** under this relation.
- Objective: maximum total profit within budget. Ties break by **lower total
  cost**, then by the **lexicographically smaller sorted id set**.

## Solving

```go
s, _ := groupopt.NewSession(inst, budget)
res := s.Solve(time.Second)   // limit <= 0 means no time limit
```

`Solve` runs a resumable branch-and-bound (fractional-knapsack relaxation
for upper bounds). If the time budget runs out it returns
`Status == Truncated` with certified `Lower`/`Upper` bounds and a witness
selection for the lower bound; calling `Solve` again resumes exactly where
the search stopped. `Status == Exact` means `Lower == Upper` is optimal.

## Checkpoints

Setup actions are non-refundable and selections can be irrevocable:

- `s.ExecuteSetup(group)` — the group's fee is sunk: it counts against every
  later budget even if no option of the group is ever selected.
- `s.CommitOption(id)` — the option (and its transitive requirements) must
  appear in every later solution.
- `s.Revise(budget, profits)` — revise the budget and option profits.

Every mutation bumps the epoch, preserves the committed closure, rebuilds
the search tree, and **invalidates all earlier certificates** — stale
optimality proofs are never reused.

## Independent verification

Package `verify` re-implements evaluation, relaxation bounds and exhaustive
subset enumeration from scratch. `verify.CheckResult(inst, state, result)`
confirms for every reported bound, against the committed closure of the
result's epoch:

- the witness is dependency-closed, contains the committed closure, respects
  the budget, and its recomputed cost (shared fee charged once, sunk fees
  counted) and profit match the report;
- every frontier node in a truncated certificate is consistent with the
  committed closure and its bound recomputes exactly under the verifier's
  own relaxation, so `Upper` is genuine;
- for `Exact` results, full enumeration confirms optimality and the
  tie-break winner;
- certificates from older epochs are rejected.

`verify.BruteForce` is the ground-truth enumerator used to cross-check the
solver (see `TestSolverMatchesBruteForce`).

## Tests

```sh
go test ./...
```
