# linsolve

Exact solver for sparse linear systems `A x = b` over the rationals.

- All arithmetic uses `math/big.Rat`; there are **no floating-point rank
  thresholds** — a pivot is nonzero iff it is exactly nonzero.
- `Solve` classifies the system and returns one of:
  - **Inconsistent** — with a certificate `c` (one coefficient per
    equation) such that `c·A = 0` and `c·b ≠ 0`: the corresponding linear
    combination of the equations yields a nonzero constant, proving
    infeasibility.
  - **Unique** — the single solution point.
  - **Parametric** — a particular solution plus one homogeneous direction
    per free variable. `FreeVars` lists the free columns in strictly
    increasing index order, so parameter `t_i` corresponds to free
    variable `FreeVars[i]`; the solution set is
    `Point + Σ t_i · Directions[i]`.
- Independent verification: `Check` (and the lower-level `VerifyPoint`,
  `VerifyDirection`, `VerifyCertificate`) recompute everything from the
  original `A` and `b` by substitution — they do not trust the solver's
  internal state.

```go
A := linsolve.NewSparseMatrix(2, 2)
A.Set(0, 0, linsolve.NewRat(2, 1))
A.Set(0, 1, linsolve.NewRat(1, 1))
A.Set(1, 0, linsolve.NewRat(1, 1))
A.Set(1, 1, linsolve.NewRat(3, 1))
b := []*big.Rat{linsolve.NewRat(5, 1), linsolve.NewRat(5, 1)}

sol, _ := linsolve.Solve(A, b)   // sol.Status == linsolve.Unique
ok := linsolve.Check(A, b, sol)  // independent substitution check
```

Run tests with:

```sh
GO111MODULE=on GOCACHE=/tmp/gocache go test ./...
```
