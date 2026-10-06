# convexhull

Exact 2D convex hull in Go. All predicates are exact sign tests on
`math/big.Rat` — there is no floating-point tolerance anywhere, so results
never depend on an epsilon.

## Features

- **Rational coordinates**: `hull.Rat("1/3", "7/8")` stores exact
  fractions; `hull.Float` converts float64 to its exact binary value.
- **Duplicates tolerated**: repeated input points never become extra
  vertices; their ids still appear on the edges that contain them.
- **Extreme points only, CCW**: `Hull.Vertices` lists input ids of extreme
  points in counterclockwise order. Points in the interior of a hull edge
  are not vertices.
- **Fully collinear clouds** yield exactly the two endpoints (a single
  distinct point yields one vertex).
- **Edge collinear ids**: every `Edge` carries `Collinear`, the sorted ids
  of all input points on the closed segment, endpoints included.

## Usage

```go
pts := []hull.Point{
    hull.Pt(0, 0), hull.Pt(4, 0), hull.Pt(4, 4), hull.Pt(0, 4),
    hull.Pt(2, 0), // on the bottom edge
    hull.Pt(0, 0), // duplicate
}
h := hull.ConvexHull(pts)
// h.Vertices == [0 1 2 3]  (CCW)
// h.Edges[0] == {From:0, To:1, Collinear:[0 1 4 5]}

if err := hull.Verify(pts, h); err != nil { ... }
```

## Verification

`hull.Verify(pts, h)` cross-checks a hull independently of the
construction algorithm, using only exact predicates:

1. vertices are distinct input points in strict CCW order;
2. edges are the cyclic vertex pairs and each `Collinear` list equals the
   recomputed exact set of input ids on the segment;
3. every input point lies inside or on the hull (`orient >= 0` for all
   directed edges);
4. exhaustively, every supporting line through any pair of distinct input
   points maps to a hull edge, and the hull has no extra edges.

## Tests

```
go test ./hull/
```

Includes deterministic cases (square with edge points and duplicates,
all-collinear, rational coordinates, degenerate clouds) plus randomized
rational clouds cross-checked by `Verify`.
