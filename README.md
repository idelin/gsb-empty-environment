# algcmp — exact real algebraic number comparison

Compares real algebraic numbers **exactly**: no floating-point
approximation is ever used as a verdict. All arithmetic is rational
(`math/big`), root counting uses Sturm sequences, and common roots are
detected with polynomial GCDs.

## Representation

A real algebraic number is given as

- a non-zero **integer-coefficient** polynomial `p` of degree ≤ 8, and
- an **open isolating interval** `(lo, hi)` with rational endpoints:
  `lo < hi`, neither endpoint is a root of `p`, and the interval contains
  **exactly one** distinct real root of `p`.

Repeated roots are handled transparently: root counting is always done on
the square-free part `p / gcd(p, p')` (Yun's square-free factorization is
also exposed as `SquareFreeFactors`). Illegal intervals (zero/two or more
roots, endpoint is a root, empty/inverted interval, degree > 8) are
rejected with an explicit error — a root is never silently chosen.

## Comparison algorithm

1. Validate both inputs (Sturm count == 1 in each open interval).
2. **Equality**: let `g = gcd(p1, p2)` and `J` the intersection of the two
   intervals. If `g` is non-constant and has exactly one root in `J`
   (Sturm count), both numbers equal that root → `equal`. Overlapping
   isolating intervals alone never imply equality.
3. **Order**: otherwise the roots differ; both intervals are bisected
   (Sturm-guided, exact midpoint-root handling) until they are disjoint,
   which decides `<` or `>`.

## Certificates

Every result comes with an independently checkable certificate:

- `equal`: the gcd polynomial and a common interval `J ⊆ I1 ∩ I2`
  containing exactly one of its roots.
- `less` / `greater`: refined, disjoint, ordered isolating intervals,
  each contained in its original interval.

The checker (`Verify`, or the `algcheck` binary) re-validates both inputs
and every certificate statement from scratch: exact polynomial division,
interval containment, endpoint non-root tests, and Sturm root counts.

## Usage

```sh
# compare (JSON in, JSON out)
echo '{"lhs":{"coeffs":["-2","0","1"],"lo":"1","hi":"3/2"},
       "rhs":{"coeffs":["-3","0","1"],"lo":"1","hi":"2"}}' | go run ./cmd/algcmp

# independent certificate check
echo '{"request": ..., "certificate": ...}' | go run ./cmd/algcheck
# or: go run ./cmd/algcmp -check
```

JSON form: `coeffs` lists integer coefficients in ascending degree;
`lo`/`hi` are rationals (`"p"` or `"p/q"`).

## Tests

```sh
go test ./...
```

Covers: same root via different polynomials, repeated roots, initially
overlapping intervals with distinct close roots, negative roots, rational
roots, midpoint-is-root refinement, forged-certificate rejection, invalid
intervals, and cross-checking comparisons plus Sturm root counts against
known roots of integer polynomials built as products of linear factors.
