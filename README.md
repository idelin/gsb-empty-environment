# plansel

Go library for selecting a maximum-profit subset of up to 20 candidate
plans under a budget, with shared group preparation fees, inter-plan
dependencies, checkpoint revisions, deadline-bounded search with
resume, and an independent verifier.

## Model

- Each `Plan` has an `ID`, a `Group`, a `Cost` (budget consumed), a
  `Profit`, and `Requires` (IDs of plans that must be selected too).
- Selecting any plan of a group charges that group's preparation fee
  **once**, no matter how many plans of the group are selected.
- A selection is feasible when it is dependency-closed and its total cost
  (plan costs + fees of newly activated groups) fits the budget.
- Objective: maximize total profit. Ties break by lower total cost, then
  by the lexicographically smaller sorted ID set.

```go
res, err := plansel.Solve(inst, deadline) // zero time = no deadline
```

## Checkpoints (Session)

`Session` carries committed state across revisions:

- `Prepare(groups...)` executes preparations; fees are **sunk**
  (non-refundable) and never charged again.
- `Commit(ids...)` marks plans irrevocably selected. The dependency
  closure is added automatically and the involved groups become prepared.
  Committed plans stay in every later solution; their costs are sunk.
- `Revise(budget, profits)` installs a new budget and overrides profits at
  a checkpoint. Sunk costs are not charged against the revised budget;
  committed plans contribute their (revised) profits to the objective.

## Deadline, bounds, resume

`Session.Solve(deadline)` / `Session.Resume(token, deadline)`:

- On completion: `Status == Optimal`, `Lower == Upper == Profit`.
- On deadline: `Status == Feasible`, `Lower <= optimum <= Upper`, and
  `Result.ResumeToken()` continues the exact same search later.
- Any `Commit`/`Prepare`/`Revise` bumps the checkpoint epoch and
  invalidates outstanding tokens: stale bounds and optimality proofs are
  never reused after a revision.

## Independent verification

`Verify(inst, view, result)` shares no state with the solver. It
recomputes the optimum by exhaustive subset enumeration (2^n, n <= 20) and
checks: committed closure (requirements committed, groups prepared),
dependency closure of `Selected`, group fees charged at most once with
sunk costs excluded, budget compliance, reported profit/cost, and that
`Lower`/`Upper` bracket the freshly computed optimum. For `Optimal`
results it additionally requires the exact tie-break winner.

## Layout

- `plan.go` — types and instance validation (size, duplicates, DAG).
- `solve.go` — branch-and-bound solver, bounds, resume token.
- `session.go` — checkpoint state machine (commit/prepare/revise).
- `verify.go` — independent exhaustive-enumeration verifier.

Run tests: `go test ./...`
