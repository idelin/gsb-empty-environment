# polysimp

Polyline simplification with a hard guarantee on the **discrete Fréchet
distance** between the original and simplified polylines.

## Guarantees

- First and last points are always preserved; the output is a subsequence
  of the input.
- The discrete Fréchet distance from the original polyline to the
  simplified one never exceeds `eps`. This is the explicit, exact
  criterion — perpendicular distance to a shortcut segment is **never**
  used as a proxy (a shortcut with tiny perpendicular deviation can have
  arbitrarily large discrete Fréchet error; see
  `TestNoPerpendicularDistanceProxy`).
- Objective: fewest retained points first, then lexicographically
  smallest index sequence.
- Every `Result` carries `Matching`: a monotone matching between input
  and output whose maximum matched-pair distance equals `Result.Error`.
  Validate it independently with `CheckMatching` or re-check a candidate
  with `Verify`.
- Budget exhaustion (`Options.Budget` caps shortcut-error evaluations)
  still returns a **feasible** simplification — a valid upper bound on
  the optimal point count — flagged with `Optimal == false` and
  `BudgetExhausted == true`.

## API

```go
res, err := polysimp.Simplify(p, eps, polysimp.Options{Budget: 0})
// res.Indices, res.Points, res.Error, res.Matching, res.Optimal

d, matching := polysimp.DiscreteFrechet(p, q)      // exact distance + evidence
res, err := polysimp.Verify(p, indices, eps)       // re-check a candidate
maxd, err := polysimp.CheckMatching(p, q, m, eps)  // validate evidence
res, err := polysimp.BruteForce(p, eps)            // enumeration, n <= 24
```

## Method

A shortcut `(a, b)` is accepted iff the exact discrete Fréchet distance
between the subchain `p[a..b]` and the two-point polyline `(p[a], p[b])`
is at most `eps`; for endpoint-sharing subsequences the total error
decomposes as the max over shortcuts. Min-point and lexicographic
tie-break are solved with two DP passes plus a greedy reconstruction.
`BruteForce` enumerates all `2^(n-2)` endpoint-preserving subsequences
and judges each by the true discrete Fréchet distance; randomized tests
cross-check `Simplify` against it.

## Test

```sh
go test ./...
```
