# linsolve

Exact solver for sparse linear systems over the rational numbers, in Go.

- **Exact arithmetic only.** All computation uses `math/big.Rat`. Rank and
  consistency are decided by exact zero tests — there is no floating point
  and no rank threshold anywhere.
- **Sparse input.** Each equation is a `map[int]*big.Rat` of non-zero
  coefficients plus a rational right-hand side.
- **Three outcomes, each independently verifiable:**
  - `Infeasible` — returns a certificate `λ` (one rational per equation)
    with `λ·A = 0` and `λ·b ≠ 0`: the linear combination of equations
    `Σ λ_e · eq_e` is the contradiction `0 = (non-zero constant)`.
  - `Unique` — returns the unique solution vector.
  - `Parametric` — returns a particular solution and one direction vector
    per free variable. Free variables are the non-pivot columns, listed by
    **increasing variable index**; parameter `t_i` corresponds to
    `FreeVars[i]`, and `x = Particular + Σ t_i·Directions[i]`.
- **Independent checking.** `Verify(sys, res)` re-substitutes every outcome
  into the original system without trusting solver internals: substitution
  of the (unique or particular) solution into `A x = b`, substitution of
  each direction into `A d = 0`, canonical-form checks of the
  parameterization, and the two certificate identities for infeasibility.

## Usage

```go
sys := &linsolve.System{
    NumVars: 2,
    Equations: []linsolve.Equation{
        {Coeffs: map[int]*big.Rat{0: big.NewRat(2, 1), 1: big.NewRat(1, 1)}, RHS: big.NewRat(5, 1)},
        {Coeffs: map[int]*big.Rat{0: big.NewRat(1, 1), 1: big.NewRat(-1, 1)}, RHS: big.NewRat(1, 1)},
    },
}
res, err := linsolve.Solve(sys) // Unique: x = (2, 1)
if err != nil { log.Fatal(err) }
if err := linsolve.Verify(sys, res); err != nil { log.Fatal(err) }
```

## Method

`Solve` performs exact Gauss-Jordan elimination on sparse rows
(`map[int]*big.Rat`), choosing the sparsest available pivot row per column
to limit fill-in. Every row carries a combination vector recording it as a
linear combination of the original equations; when elimination produces the
row `0 = c` with `c ≠ 0`, that combination vector is exactly the
infeasibility certificate. Otherwise the reduced row echelon form directly
yields the particular solution and the null-space directions.

## Tests

```
go test ./...
```

Covers unique/parametric/infeasible systems, certificate identities,
free-variable ordering, tiny coefficients (`1/10^30`) that would defeat
floating-point rank thresholds, randomized consistent and inconsistent
sparse systems, and rejection of garbage results by `Verify`.
