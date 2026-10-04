# matrixgame — exact zero-sum matrix game solver (Go)

Solves two-player zero-sum games given by a rational payoff matrix, returning
both players' optimal mixed strategies and the **exact** game value as a
`big.Rat`. No floating point, no iterative approximation: all arithmetic is
exact rational arithmetic via `math/big`.

## Method

Support enumeration over Shapley–Snow kernels: for every square submatrix
`M` of `A` (sizes `k = 1..min(m,n)`), solve the two exact linear systems

- `M q = v·1`, `Σq = 1` (column player makes support rows indifferent)
- `pᵀ M = v·1ᵀ`, `Σp = 1` (row player makes support columns indifferent)

by exact Gaussian elimination, then check the candidate globally: `p, q ≥ 0`,
`A q ≤ v·1`, `pᵀ A ≥ v·1ᵀ`. Every matrix game has at least one such kernel,
so enumeration is complete and always terminates with an exact optimum. Pure
saddle points are the `k = 1` case and need no special handling; duplicate
rows/columns, multiple optima, and all-negative payoffs fall out naturally.

## Verifiable certificates

`Solution` carries `ColPayoffs = pᵀA` (every entry `≥ value`, so the row
player guarantees the value) and `RowPayoffs = Aq` (every entry `≤ value`,
so the column player enforces it). `Verify(A, sol)` recomputes everything
independently — probability simplex checks plus both bounds — so optimality
is provable from the output alone.

## Usage

    go test ./...        # unit + randomized verification tests
    go run ./cmd/mgsolve <<EOF
    2 2
    1 -1
    -1 1
    EOF

Input format: `m n` on the first line, then `m` rows of `n` rationals
(integers or fractions like `-3/2`). Output: exact value, both strategies,
and the certificates, re-verified before printing.
