# gridray

Exact rational grid traversal and asynchronous voxel picking in Go.

## Grid traversal (`gridray.go`)

`TraverseSegment(p0, p1 Point) []Visit` enumerates every **closed** unit
grid cell touched by the segment `P(t) = p0 + t*(p1-p0)`, `t ∈ [0,1]`,
with exact `math/big.Rat` arithmetic:

- Endpoints may be arbitrary rationals (negative coordinates included).
- Touching a boundary counts: at a grid line two adjacent cells are
  reported, at a grid point all four.
- Each `Visit` carries the first-contact parameter `T`; results are sorted
  by `(T, Cell.X, Cell.Y)` and contain no duplicates.
- A zero-length segment reports the cells containing the point.

`clip.go` provides the independent verification oracle `ClipSegmentCell`
(exact Liang–Barsky clipping against one closed cell) and
`TraverseByClipping`, which the tests cross-check against the traversal on
random rational segments, grid-line walks and zero-length rays.

## Voxel picking (`picker.go`)

`Picker` casts the traversal as a voxel pick over a scene whose 16x16
chunks arrive asynchronously:

- `BeginQuery` binds the query to the current scene version and walks
  cells in traversal order.
- Cells in loaded chunks are proven empty (appended to `Evidence`) or
  solid (`Hit`, query done). The first unknown cell is **not** skipped:
  the query becomes `Pending` with that cell as a retractible candidate.
- `DeliverChunk(coord, chunk, version)` resolves waiting queries bound to
  the same version; an empty candidate is retracted into evidence and the
  walk continues, a solid one becomes the hit. Deliveries tagged with a
  stale version never affect newer queries.
- `UpdateScene` bumps the scene version; edits apply only to the new
  version, leaving in-flight older queries untouched.
- `Query.Snapshot` returns the current status, the first hit (if any) and
  all empty-cell evidence before it.

## Tests

```
GO111MODULE=on go test ./...
```
