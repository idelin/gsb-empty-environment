# algcmp — exact comparison of real algebraic numbers

Compares two real algebraic numbers **exactly** and emits a checkable
certificate.  Each number is given as a nonzero integer-coefficient
polynomial (degree ≤ 8) plus an open isolating interval with rational
endpoints that contains exactly one real root (endpoints must not be
roots).  Repeated roots are handled by square-free decomposition.

Only exact rational arithmetic (`math/big`), polynomial GCD/division
and Sturm root counts are used — never a fixed-precision approximation.

## How comparison works

- **Validation.**  The polynomial is replaced by its square-free part
  `p / gcd(p, p')`.  The interval is rejected with an explicit error
  unless `lower < upper`, neither endpoint is a root, and a Sturm count
  shows exactly one distinct real root inside.
- **Equality.**  `α = β` iff `g = gcd(Pa, Pb)` is nonconstant and has a
  root in the *intersection* of the two isolating intervals.  (A root of
  `g` in the intersection is a root of both polynomials inside their
  isolating intervals, hence equals both numbers.)  Overlapping
  intervals alone never imply equality.
- **Order.**  Otherwise both intervals are refined by exact bisection
  (Sturm count picks the half containing the root; a midpoint that is
  itself a root is handled exactly) until they are disjoint, which
  decides `<` / `>`.

## Certificates

- `less` / `greater`: refined, disjoint isolating intervals for both
  numbers.  The checker re-validates each interval (sub-interval of the
  input, endpoints not roots, Sturm count 1) and the separation.
- `equal`: a nonconstant common divisor `g` of both polynomials and a
  "meet" interval, contained in both certified intervals, in which `g`
  has a root.  The checker verifies divisibility, containment and the
  Sturm count independently.

## CLI

```sh
go build ./cmd/algcmp

# compare; prints {"result": "less|equal|greater", "certificate": {...}}
./algcmp compare input.json

# independently verify a certificate (input.json also has "certificate")
./algcmp verify input.json    # prints {"valid": true} or exits 1
```

Input format (coefficients ascending, rationals as `n` or `n/d`):

```json
{"left":  {"poly": ["-2", "0", "1"], "lower": "1",   "upper": "2"},
 "right": {"poly": ["-3", "0", "1"], "lower": "3/2", "upper": "2"}}
```

## Library

```go
a, err := algcmp.NewAlgNum(p1, l1, r1)   // validates the isolating interval
b, err := algcmp.NewAlgNum(p2, l2, r2)
res, cert := algcmp.Compare(a, b)        // -1 / 0 / +1 + certificate
err = algcmp.Verify(doc)                 // independent certificate check
```

## Tests

```sh
go test ./...
```

Covers: same root via different polynomials, repeated roots (up to
degree 8), initially overlapping intervals with distinct nearby roots,
negative roots, rational roots, products of rational linear factors
(checked against the known roots), exact midpoint hits during
bisection, illegal isolating intervals, and certificate tampering.
