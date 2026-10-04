# circlecover

Maximum-weight circle covering with exact arithmetic.

Given integer points with non-negative weights (points with equal
coordinates count separately) and a non-negative rational radius, find
the maximum total weight coverable by a **closed** disk, plus a center
attaining it.

## Method

Candidate centers are enumerated exhaustively for small point sets:

- every input point as a center;
- for every pair of points at distance `d <= 2r`, the two centers that
  put both points on the boundary (they coincide at the midpoint when
  `d == 2r`).

Each candidate is scored by exactly summing the weights of covered
points, and the best is kept. Ties resolve to the first candidate in
enumeration order.

## Exactness

No floating-point tolerance is used anywhere:

- radii and all arithmetic are `math/big.Rat`;
- pair-boundary centers have coordinates of the form `a + b*sqrt(s)`
  with rational `a`, `b`, `s` (type `Alg`), so squared distances are
  algebraic numbers `A + B*sqrt(S)`;
- `A + B*sqrt(S) <= r^2` is decided by sign analysis and exact squaring
  of rationals.

`CoveredWeight` is exported so any reported center can be independently
recomputed and verified.

## Usage

Library:

```go
res, err := circlecover.Solve(pts, weights, radius) // radius *big.Rat
// res.Weight *big.Int, res.Center with Alg coordinates
```

CLI (stdin: radius, count, then `x y w` lines):

```sh
printf '5/2\n3\n0 0 1\n3 0 1\n6 0 5\n' | go run ./cmd/circlecover
```

## Tests

```sh
go test ./...
```

Covers radius 0 with duplicate coordinates, points exactly `2r` apart,
multiple tied optima, boundary-inclusive 3-4-5 configurations, and
comparisons decided by gaps of ~1e-13 that a fixed float epsilon would
get wrong.
