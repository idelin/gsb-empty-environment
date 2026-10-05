# closestpair

Go library for closest-pair queries on ID-tagged 2D integer point sets.

## Features

- `Query(points)` returns the minimum **squared** Euclidean distance and
  **all** tied ID pairs, sorted by `(First, Second)` with `First < Second`.
- Exact arithmetic: distances are computed with `math/big` integers, so
  full-range `int64` coordinates cannot overflow and no floating-point
  square roots are ever compared.
- Paginated results: duplicate coordinates can make the number of tied
  pairs quadratic, so pairs are consumed page by page via
  `Result.Pairs(offset, limit)` (negative `limit` = all remaining);
  `Result.NumPairs()` gives the total up front.
- Divide-and-conquer in O(n log n) plus time linear in the output size.

## Usage

```go
res, err := closestpair.Query([]closestpair.Point{
    {ID: 10, X: 0, Y: 0},
    {ID: 20, X: 3, Y: 4},
    {ID: 30, X: 0, Y: 1},
})
if err != nil {
    // fewer than 2 points, or duplicate IDs
}
fmt.Println(res.MinDist2())  // 1

for offset := 0; offset < res.NumPairs(); {
    page := res.Pairs(offset, 100)
    for _, p := range page {
        fmt.Println(p.First, p.Second)
    }
    offset += len(page)
}
```

## Testing

`go test ./...` cross-checks the divide-and-conquer solver against an
O(n^2) all-pairs reference, including randomized inputs, midline-crossing
pairs, co-circular lattice points, duplicate point groups, quadratic
outputs, and coordinates at the `int64` extremes.
