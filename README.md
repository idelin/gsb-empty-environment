# realroots

Exact counting and isolation of distinct real roots of an integer-coefficient
polynomial inside a rational open interval `(a, b)`. All decisions are made
with exact rational arithmetic (`math/big.Rat`); no fixed-precision
approximation is ever used as a final arbiter.

## Usage

```
go run . <c_n> <c_{n-1}> ... <c_0> <a> <b>
```

- `c_i`: integer coefficients, highest degree first
- `a`, `b`: rational endpoints (integer or fraction like `-3/2`)

Example — isolate sqrt(2) from `x^2 - 2` on `(0, 2)`:

```
$ go run . 1 0 -2 0 2
polynomial:        p(x) = x^2 - 2
open interval:     (0, 2)
distinct real roots in (0, 2): 1
  root 1: x in (0, 2)
    sign evidence: q(0) = -2, q(2) = 2 (opposite signs)
```

## Method

1. **Zero polynomial rejected** up front.
2. **Multiplicities removed**: `p` is replaced by its square-free part
   `q = p / gcd(p, p')`, which has the same distinct roots, all simple.
3. **Endpoint roots**: if `q(a) == 0` or `q(b) == 0`, they are reported
   separately and excluded from the open interval.
4. **Exact counting**: Sturm's theorem — the number of distinct roots in
   `(a, b]` is `V(a) - V(b)`, where `V(x)` counts sign changes of the Sturm
   chain evaluated exactly at `x`.
5. **Isolation**: dyadic bisection driven by Sturm counts until each
   subinterval contains exactly one root. Bisection midpoints that are
   themselves roots are reported as exact rational roots. The resulting
   open intervals are pairwise disjoint.
6. **Sign-change evidence**: each isolating interval `(l, r)` is emitted
   only when `q(l)` and `q(r)` are nonzero with opposite signs; the exact
   rational values are printed as the witness.

## Files

- `poly.go` — exact rational polynomial arithmetic (add/mul/divmod/gcd/
  derivative/square-free/eval)
- `sturm.go` — Sturm chain, sign variations, exact root counting
- `isolate.go` — Sturm-guided bisection into disjoint isolating intervals
- `report.go` — `Analyze`: endpoint-root reporting, evidence assembly
- `main.go` — CLI
- `realroots_test.go` — verification (see below)

## Verification

`go test ./...` covers:

- product of known rational roots `(x-1)(2x-1)(x+3)(x-2)` — each known root
  isolated exactly once;
- irrational low-degree cases `x^2 - 2` (containment of sqrt(2) proved
  exactly via `l^2 < 2 < r^2` over Q) and `x^2 - x - 1` (golden ratio);
- multiple roots `(x-1)^2 (x-2)` deduplicated to 2 distinct roots;
- zero polynomial rejected;
- endpoint roots `x(x-1)(x+1)` on `(-1, 1)` reported separately;
- two roots `1/3` and `1/3 + 10^-25`, whose separation is far below float64
  epsilon — fixed-precision arithmetic cannot separate them, exact
  arithmetic counts and isolates both;
- interval disjointness, containment in `(a, b)`, and sign-change evidence
  checked as invariants on every report.
