# gridray

Exact 2D grid traversal for segments with rational endpoints, plus
versioned voxel picking over asynchronously arriving grid chunks.
All arithmetic uses `math/big.Rat`; there is no floating-point drift.

## Segment traversal

`SegmentCells(p0, p1 Vec) []CellHit` lists every **closed** unit cell
touched by the closed segment `p0 -> p1`:

- passing exactly through a grid vertex touches all four adjacent cells;
- running exactly along a grid line touches the cells on both sides;
- each cell carries its first-contact parameter `T` in `[0, 1]`;
- results are sorted by `(T, X, Y)` and contain no duplicates;
- degenerate (zero-length) segments return the 1/2/4 cells containing
  the point.

The implementation enumerates exact crossing events and classifies event
points and interval midpoints. It is verified in tests against an
independent per-cell Liang–Barsky clipping oracle, covering negative
coordinates, grid-line walks, vertex hits, and zero-length rays.

## Voxel picking

```go
s := gridray.NewScene()
q := s.NewQuery(p0, p1)          // bound to the current scene version
res := q.Result()                // first hit + all preceding evidence
for r := range q.Updates() {     // snapshots as chunks arrive
    ...
}
```

- `Scene.DeliverChunk(version, chunk)` applies an asynchronous chunk
  response; responses tagged with a stale version are dropped and never
  affect the scene or any query. `Scene.SetCell` bumps the version.
- Cells whose chunks have not arrived are reported as `PickPending`
  inside the evidence, never skipped. While pending cells precede the
  first solid cell, `PickResult.Pending` is true and `Hit` is only a
  candidate.
- When later chunks resolve the pending cells, a superseded candidate is
  retracted: the next snapshot names it in `PickResult.Retracted`.
- `PickResult.Cells` is exactly the traversal prefix up to and including
  the first hit — the full empty-cell evidence (interleaved with any
  still-pending cells) in first-contact order.

## Tests

```
go test ./...
```
