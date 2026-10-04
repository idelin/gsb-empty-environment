# diskcover

Exact maximum-weight disk cover for integer lattice points.

- Input: integer points, non-negative `int64` weights, rational radius (`*big.Rat`).
- Output: maximum total weight coverable by a closed disk, plus a center.
- Points sharing coordinates are counted individually.
- Centers are algebraic: `a + b*sqrt(D)` with rational `a`, `b` and integer
  `D >= 0` (`AlgPoint`). All coverage comparisons use `big.Rat`/`big.Int`
  only — no floating-point tolerance.

## Method

An optimal disk can always be moved (without losing covered weight) until
either a covered point sits at its center or two covered points lie on its
boundary. The solver enumerates all such candidates — one per point, plus up
to two per pair of points within `2r` — and recomputes each candidate's
covered weight exactly. For a pair at squared distance `d2` with radius
`a/b`, the boundary centers are

    m ± sqrt(N*d2)/(2*b*d2) * (-dy, dx),   N = 4a² - d2·b² ≥ 0

which keeps the radical an integer (`D = N·d2`). Checking
`|p - c|² ≤ r²` reduces to comparing `P + Q·sqrt(D)` against `r²` with
rational `P`, `Q`, done by sign case analysis and exact squaring.

## Usage

```go
w, center, err := diskcover.Solve(pts, weights, radius)
```

CLI:

```sh
printf '0 0 3\n6 8 7\n' | go run ./cmd/diskcover 5
# weight: 10
# center: (3, 4)
```

## Tests

    GO111MODULE=on go test ./...

Covers radius 0, points exactly one diameter apart, tied optima,
irrational centers, and exact on-boundary inclusion (`r = 5` vs
`r = 4999/1000` for a point at distance 5).
