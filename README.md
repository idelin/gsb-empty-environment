# binom

Binomial-coefficient queries in Go: exact big-integer values and residues
modulo an arbitrary positive integer, including composite moduli where
factorials are not invertible.

## Features

- `Exact(n, k)` — exact `C(n, k)` as a `*big.Int`.
- `Mod(n, k, m)` — `C(n, k) mod m` for any positive integer `m`.
  The modulus is factored into prime powers, `C(n, k) mod p^a` is computed
  via the prime-power generalization of Lucas' theorem (p-adic valuation
  plus p-free factorial residues — no factorial inverses needed), and the
  residues are recombined with CRT.
- `Calculator` caches modulus factorizations; `ModBatch` evaluates many
  queries against one modulus while factoring it once.
- `Calculator.Budget` caps trial-division work per factorization
  (negative = unlimited). Primality proofs are free, so prime moduli never
  consume budget. Returns `ErrFactorBudgetExceeded` when the budget is
  insufficient; a cached factorization is reused regardless of budget.

## Conventions

- `C(n, k) = 0` for `k < 0` or `k > n`; `C(0, 0) = 1`.
- Result is `0` when `m == 1`.
- `n < 0` → `ErrNegativeN`; `m <= 0` → `ErrNonPositiveModulus`.

## Example

```go
c := binom.NewCalculator()
c.Budget = 1 << 20 // cap factorization work

exact, _ := c.Exact(100, 50)
m := new(big.Int).Exp(big.NewInt(7), big.NewInt(13), nil) // 7^13
r, _ := c.Mod(100, 50, m)

qs := []binom.Query{{N: 40, K: 17}, {N: 1000, K: 500}}
rs, _ := c.ModBatch(qs, m) // shares the cached factorization of m
```

## Tests

```sh
go test ./...
```

Tests cross-check modular results against exact big-integer values and
cover out-of-range `k`, `n = 0`, modulus `1`, high prime-power moduli
(e.g. `2^40`, `7^13 * 11^9`), semiprime moduli, and budget/cache behavior.
