# sparselu

Sparse LU factorization of small rational matrices in Go, with exact
`math/big.Rat` arithmetic.

Given an m x n matrix A, it computes permutations P, Q and factors L, U
such that **PAQ = LU**, where L is unit lower triangular (m x m) and U is
upper trapezoidal (m x n).

## Features

- **Exact rational arithmetic** — no floating-point error; entries that
  cancel to zero during elimination are removed, never stored.
- **Full pivoting** — a zero pivot is resolved by a row swap, a column
  swap, or both; if the remaining submatrix is entirely zero, the rank is
  reported instead.
- **Fill-in accounting** — `Factorization.FillIn` counts nonzero entries
  created in positions that were zero in the (permuted) input.
- **Resumable budgets** — `Factorizer.Run(budget)` stops with
  `ErrBudgetExhausted` before exceeding the fill-in budget; call `Run`
  again with a larger budget to resume exactly where it stopped.
- **Independent verification** — `Verify(a, f)` applies the permutations
  and multiplies L*U with a standalone sparse `Mul` to check PAQ = LU.

## Usage

```go
a := sparselu.New(2, 2)
a.Set(0, 1, big.NewRat(1, 1))
a.Set(1, 1, big.NewRat(2, 1))

f, err := sparselu.Factorize(a, -1) // negative budget = unlimited
if err != nil { /* ErrBudgetExhausted */ }
ok := sparselu.Verify(a, f) // PAQ == LU
```

Resumable run:

```go
fz := sparselu.NewFactorizer(a)
for !fz.Done() {
    if err := fz.Run(budget); err == sparselu.ErrBudgetExhausted {
        budget += 10 // grant more fill-in and resume
    }
}
f := fz.Result()
```

## Test

```sh
go test ./...
```
