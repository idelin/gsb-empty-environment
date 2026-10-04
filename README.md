# closestpair

Go library for closest-pair queries over 2D integer point sets with unique IDs.

## Features

- **Exact arithmetic**: the minimum squared Euclidean distance is computed with
  `math/big` integers. Coordinates anywhere in the `int64` range are safe —
  squared differences never overflow and no floating-point square roots are
  ever compared.
- **All ties**: returns every ID pair attaining the minimum distance, sorted by
  `(A, B)` with `A < B` inside each pair.
- **Pagination**: duplicate coordinates can make the number of tied pairs grow
  quadratically, so results are paged via `offset`/`limit`. Iterating
  `offset += limit` until an empty page enumerates all pairs without omissions
  or duplicates.

## Usage

```go
res, err := closestpair.ClosestPairs(points, offset, limit)
if err != nil { /* ErrTooFewPoints / ErrDuplicateID / ErrBadRange */ }
fmt.Println(res.Dist2) // exact minimum squared distance (*big.Int)
fmt.Println(res.Total) // total number of tied pairs across all pages
for _, p := range res.Pairs {
	fmt.Println(p.A, p.B) // sorted ID pairs
}
```

## How it works

1. **Minimum distance**: duplicate-coordinate scan (distance zero short-circuit),
   then a classic O(n log n) divide-and-conquer over x-sorted points with a
   y-sorted strip scan around the split line — all in `big.Int`.
2. **Tie enumeration**: for distance zero, points are grouped by coordinate and
   every within-group pair is emitted. For d > 0, a uniform grid with cell side
   `floor(sqrt(d))` is used: any pair at squared distance d has integer
   coordinate differences `<= floor(sqrt(d))`, so tied pairs always lie in the
   3x3 cell neighborhood and cannot be missed.

## Tests

`go test ./...` cross-checks against an all-pairs brute-force reference over
randomized inputs, plus targeted cases: closest pairs straddling the recursion
midline, 20 co-circular lattice points with 12 tied pairs, duplicate point
clusters (quadratic output), pagination integrity, and int64-extreme
coordinates that overflow 64-bit squared distances.
