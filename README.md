# policysolver

Exact solver for finite decision problems under partial observability.

- **Finite worlds** with rational probabilities (`math/big.Rat`, no floats).
- **Observable signals**: a policy maps each signal to one action, so worlds
  emitting the same signal must share an action (non-anticipativity).
- **Lexicographic objective**: minimize the worst-case loss first, then the
  expected (average) loss.
- **Shared action capacities**: the total probability mass of the worlds an
  action covers must not exceed its capacity.
- **Enumeration cross-check**: `Enumerate` exhaustively evaluates all
  `actions^signals` policies; `Verify` asserts the branch-and-bound solver
  agrees with it on feasibility and both objective values.

## Usage

```sh
go run . -f examples/shared_capacity.json -verify
```

Input is JSON (`-f -` reads stdin); all numbers are rationals like `"3/4"`:

```json
{
  "worlds":  [{"name": "rain", "prob": "1/4", "signal": "cloudy"}],
  "actions": ["umbrella", "picnic"],
  "losses":  {"rain": {"umbrella": "1", "picnic": "9"}},
  "capacity": {"umbrella": "3/5"}
}
```

`capacity` is optional per action; omitted actions are uncapacitated.
World probabilities must sum to exactly 1.

## Layout

- `problem.go` — types, JSON loading, exact policy evaluation.
- `solver.go` — branch-and-bound over signal assignments with lexicographic
  lower-bound pruning and incremental capacity checks.
- `enumerate.go` — exhaustive policy enumeration and `Verify` cross-check.
- `main.go` — CLI.
- `solver_test.go` — unit tests (capacity binding, non-anticipativity,
  lexicographic order, infeasibility) plus randomized solver-vs-enumeration
  cross-checks.

## Tests

```sh
go test ./...
```
