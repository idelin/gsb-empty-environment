# CSP Solver with Conflict-Directed Backjumping (CBJ)

A constraint satisfaction solver in pure Go (standard library only) that
performs **non-chronological backjumping**: on a dead end, conflict
analysis identifies the deepest variable actually responsible for the
contradiction (the *conflict set*) and jumps straight back to it,
skipping irrelevant intermediate variables.

## How it works

- Each variable accumulates a **conflict set**: the past variables whose
  assignments ruled out one of its values (`cbj.go`).
- When a variable's domain is exhausted, the search jumps back to the
  **deepest variable in its conflict set**, merging conflict sets along
  the way, instead of undoing just the previous variable.
- Jumps that skip at least one variable are recorded in
  `Stats.Backjumps`.

## Acceptance scenario

`NewBenchmarkProblem()` builds a 10-variable CSP (`csp.go`):

- steps 1..10 assign `X1..X10` in order;
- `X8` has a single candidate value `{1}` and is constrained only by
  `X2 != X8`, so the dead end at **step 8** is caused solely by the
  value chosen at **step 2**;
- steps 3..7 form a benign inequality chain that always succeeds.

The solver detects the conflict set `{X2}` at step 8 and jumps directly
back to step 2, skipping steps 3..7 — pruning the ~3^5 useless
combinations a chronological solver would explore.

## Layout

- `csp.go` — problem model (`Problem`, `Constraint`) and the benchmark scenario
- `cbj.go` — conflict-directed backjumping solver (`CBJSolver`)
- `naive.go` — chronological backtracking solver for comparison
- `cbj_test.go` — acceptance tests
- `cmd/demo/main.go` — runnable demo

## Run

```console
$ go run ./cmd/demo
=== 10-variable CSP: conflict at step 8 is caused only by step 2 ===
domains: X1..X7,X9,X10 in {1,2,3}; X8 = {1}
constraints: X2 != X8; benign chain X3!=X4, X4!=X5, X5!=X6, X6!=X7

chronological backtracking: 199 value attempts, solution [1 2 1 2 1 2 1 1 1 1]
CBJ backjump: step 8 -> step 2 (skipped steps 3..7)
CBJ: 21 value attempts, solution [1 2 1 2 1 2 1 1 1 1]
valid solution: true
pruning factor: 9.5x fewer value attempts
```

## Test

```console
$ go test ./... -v
=== RUN   TestBackjumpSkipsSteps3To7
    cbj_test.go:27: backjump: step 8 -> step 2 (skipped steps 3..7)
    cbj_test.go:28: solution: [1 2 1 2 1 2 1 1 1 1]
--- PASS: TestBackjumpSkipsSteps3To7 (0.00s)
=== RUN   TestConflictSetContainsOnlyStep2
--- PASS: TestConflictSetContainsOnlyStep2 (0.00s)
=== RUN   TestCBJPrunesSearch
    cbj_test.go:67: value attempts: CBJ=21, chronological=199
--- PASS: TestCBJPrunesSearch (0.00s)
=== RUN   TestUnsatisfiable
--- PASS: TestUnsatisfiable (0.00s)
=== RUN   TestValidate
--- PASS: TestValidate (0.00s)
PASS
ok  	csp
```
