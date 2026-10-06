# polysimp

Exact minimum-vertex polyline simplification in Go.

A simplification keeps a subsequence of the input vertices — always the
first and last vertex plus every mandatory index. An edge of the simplified
polyline is legal only when the **exact squared distance** from every skipped
intermediate vertex to that edge segment does not exceed a rational
threshold. `Simplify` returns the simplification with the fewest retained
vertices; ties are broken by choosing the lexicographically smallest index
sequence. Duplicate coordinates are handled naturally (degenerate segments
reduce to point-to-point distance).

## Algorithm

Not a greedy farthest-point (RDP) heuristic:

1. `SegDistSq` computes exact point-to-segment squared distances with
   `math/big.Rat` (integer coordinates, rational projection parameter).
2. Every candidate edge `(i, j)` is validated exactly — O(n³) rational
   operations in total.
3. A shortest-path DP minimizes the number of retained vertices; edges may
   not skip mandatory indices.
4. The lexicographically smallest optimal index sequence is reconstructed
   greedily from the DP table.

## Usage

```go
pts := []polysimp.Point{{0, 0}, {1, 1}, {2, 1}, {3, 0}}
idx := polysimp.Simplify(pts, []int{2}, big.NewRat(1, 2))
// idx == [0 2 3]: threshold is the max squared distance 1/2, vertex 2 mandatory.
```

## Testing

`go test ./...` cross-checks `Simplify` against exhaustive subset
enumeration (`bruteForce` in `simplify_test.go`) over thousands of random
polylines with duplicate coordinates, mandatory sets, and zero/negative/
fractional thresholds, plus targeted cases including one where greedy
farthest-point splitting is strictly suboptimal.
