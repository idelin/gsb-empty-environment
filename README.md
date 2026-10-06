# segconst

Optimal piecewise-constant approximation of integer sequences with exact
rational arithmetic.

Given a sequence of integers, a rational per-segment penalty `lambda`, and a
minimum segment length `m`, `segconst` finds the partition minimizing

    total squared error (SSE) + lambda * number of segments

Ties are broken deterministically: fewer segments first, then the
lexicographically smallest boundary list. All costs, losses, and segment
means are exact (`math/big.Rat`); no floating point is involved.

## Usage

```go
c := segconst.NewCosts([]int64{0, 0, 1, 1, 2, 2})

// Single penalty.
res := c.Solve(big.NewRat(1, 1), 1)
fmt.Println(res.Loss, res.Cuts, res.Means) // 3/1 [2 6] [0/1 3/2]

// Many penalties, reusing the precomputed interval costs.
results := c.SolveAll([]*big.Rat{big.NewRat(1, 2), big.NewRat(5, 1)}, 2)
```

`NewCosts` builds prefix sums once in O(n); each `Solve` then costs O(n^2)
exact interval-cost queries served from the shared tables (with caching), so
sweeping a family of penalty parameters over the same sequence is cheap.

`Result` fields: `Loss` (penalized objective), `SSE` (unpenalized error),
`Cuts` (exclusive segment ends, last is `n`), `Means` (exact per-segment
means), and `Feasible` (false when `0 < n < m`).

## Verification

`BruteForceSolve` enumerates every legal cut pattern with the same
tie-breaking rules; the test suite cross-checks the dynamic program against
it on hundreds of short random sequences and penalties, including
large-magnitude values that would lose precision in floating point.

## Test

```sh
go test ./...
```
