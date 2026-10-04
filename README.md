# ratlsq

Exact least squares over the rationals. Every computation uses
`math/big.Rat`; no floating-point value ever enters the pipeline, so
coefficients are exact regardless of denominator size.

## Usage

```go
a := ratlsq.NewMatrix(3, 2)      // m samples x n unknowns (m < n, m = 0 allowed)
b := ratlsq.NewVector(3)
// ... fill via Set with *big.Rat values ...

sol, err := ratlsq.LeastSquares(a, b, ratlsq.Budget{MaxBits: 4096})
if err != nil { /* dimension mismatch or *ratlsq.BudgetError */ }

// Full affine description of all minimizers of ||A x - b||:
//   x = sol.Particular + t1*sol.NullBasis[0] + ... (ti in Q)
// sol.NullBasis is empty iff the solution is unique.
// sol.MinNorm is the unique minimum Euclidean norm solution.

if err := ratlsq.Verify(a, b, sol, ratlsq.Budget{}); err != nil { ... }
```

## Design

- `LeastSquares` solves the normal equations `A^T A x = A^T b` (always
  consistent) by exact Gauss-Jordan RREF. Rank deficiency yields a
  particular solution plus a basis of `null(A)`, giving the affine
  family of all minimizers.
- `MinNorm` projects the particular solution off the null space:
  `x* = x0 - N (N^T N)^{-1} N^T x0`, all in exact rational arithmetic.
- `Verify` independently recomputes everything from `A` and `b`: the
  normal-equation residuals, `A v = 0` for each null basis vector,
  basis independence and completeness (`rank + nullity = n`), and
  orthogonality of `MinNorm` to the null space.
- `Budget{MaxBits}` aborts with `*BudgetError` as soon as any
  intermediate numerator or denominator exceeds the bit limit,
  controlling coefficient blow-up on small-sample fits. Zero means
  unlimited.

## Tests

`go test ./...` covers duplicate columns, all-zero columns, zero
samples, zero unknowns, large coprime denominators, exactness of huge
intermediate coefficients, budget overflow, and affine-family
invariants (constant residual, minimality of the min-norm solution).
