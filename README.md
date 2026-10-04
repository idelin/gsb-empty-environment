# sparselu

Sparse LU factorization of small rational matrices in pure Go, using exact
`math/big.Rat` arithmetic.

Given a sparse matrix `A`, it computes row/column permutations `P`, `Q` and
triangular factors `L` (unit lower) and `U` (upper) such that

```
P*A*Q == L*U
```

## Features

- **Exact rational arithmetic** — no floating-point error; entries that
  cancel become exact zeros and are removed from the sparse structure, so
  no residual zeros are ever stored or counted.
- **Full pivoting** — zero pivots are avoided by row and/or column swaps;
  when the trailing submatrix is entirely zero the rank is reported instead.
- **Rectangular and rank-deficient matrices** — works for any `m x n`.
- **Fill-in accounting** — `Factorization.FillIn` counts nonzeros of the
  `L`/`U` pattern at positions where `P*A*Q` is zero.
- **Resumable budget** — `Factorizer.Run(budget)` spends a bounded number of
  row-elimination operations; on `ErrBudgetExhausted` the full state is
  preserved and the next `Run` resumes mid-step.
- **Independent verification** — `Multiply` and `Permute` are separate from
  the factorization code, so tests check `P*A*Q == L*U` by genuine sparse
  multiplication.

## Usage

```go
a := sparselu.NewMatrix(3, 3)
a.Set(0, 0, big.NewRat(1, 2))
// ...

res := sparselu.Factorize(a)         // one-shot
fmt.Println(res.Rank, res.FillIn)

// Or with a resumable budget:
f := sparselu.NewFactorizer(a)
for !f.Done() {
    if err := f.Run(100); err != sparselu.ErrBudgetExhausted {
        // err == nil means finished; ErrBudgetExhausted means resume later
    }
}
res = f.Result()

// Verify:
paq := sparselu.Permute(a, res.P, res.Q)
lu := sparselu.Multiply(res.L, res.U)
_ = sparselu.Equal(paq, lu) // true
```

## Tests

```
go test ./...
```

Covers zero-pivot row swaps, column-swap-only continuation, rectangular and
rank-deficient matrices, exact cancellation (no residual zeros), fill-in
counting, budget exhaustion/resumption equivalence, and randomized
cross-checks.
