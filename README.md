# convexhull

2D convex hull with exact rational arithmetic (`math/big.Rat`). No
floating-point tolerance is used anywhere; every predicate is exact.

## Semantics

- Input points may have rational coordinates and may be duplicated.
- `ConvexHull` returns the extreme points (vertices) only, in
  counterclockwise order, starting from the lexicographically smallest
  vertex. Points lying in the relative interior of an edge are not
  vertices.
- Fully collinear input yields exactly two vertices (the endpoints) and
  a single edge; identical points yield one vertex and no edges.
- Each `Edge` carries `Points`: the ids of **every** input point lying
  on the edge segment, duplicates included, in ascending id order. A
  point coinciding with a vertex appears on both adjacent edges.
- When duplicates exist at a vertex, the smallest input id represents
  the vertex.

## Verification

`Verify(pts, hull)` independently checks a hull without re-running the
construction algorithm:

1. vertices are valid, coordinate-distinct, and strictly
   counterclockwise (no 180° turns);
2. every input point lies inside or on the hull (left of or on every
   directed edge; on the segment in degenerate cases);
3. every edge is a supporting line, and **exhaustively** every
   supporting line through any pair of input points is realized by a
   hull edge covering all input points on that line;
4. every edge's `Points` list equals exactly the set of input ids on
   its segment.

## Example

```go
pts := []convexhull.Point{
    convexhull.Pt(0, 0),
    convexhull.PtFrac(1, 2, 0, 1), // (1/2, 0), on the bottom edge
    convexhull.Pt(1, 0),
    convexhull.Pt(0, 1),
    convexhull.Pt(1, 0),           // duplicate of vertex 2
}
h := convexhull.ConvexHull(pts)
// h.Vertices == [0 2 3] (counterclockwise extremes)
// h.Edges[0].Points == [0 1 2] (all ids on segment 0->2)
if err := convexhull.Verify(pts, h); err != nil {
    panic(err)
}
```

## Tests

```
go test ./...
```

Includes exact-predicate cases where the decisive quantity is 1e-40
(far below float64 resolution), degenerate inputs, negative tests for
the verifier, and a randomized fuzz cross-checked against an
independent brute-force supporting-line construction.
