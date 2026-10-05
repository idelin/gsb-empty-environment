# exact_clip

Exact convex polygon clipping in C++17 (header-only, no dependencies).

- **Exact arithmetic**: all coordinates are arbitrary-precision rationals
  (`exact::Rational` over a built-in `exact::BigInt`). Orientation tests and
  line intersections are computed exactly — there are no floating-point
  thresholds anywhere, so arbitrarily small intersection areas are preserved.
- **Forgiving inputs**: subject and clip polygons may be CW or CCW and may
  contain duplicate adjacent vertices and collinear edges; they are normalized
  internally. Degenerate inputs (points, segments) are handled too.
- **Typed result**: the intersection kind distinguishes area, line, point and
  empty outcomes. Area results are normalized CCW vertex sequences (no
  duplicate adjacent vertices, no collinear vertices).

## API

```cpp
#include <exact/clip.hpp>

exact::ClipResult r = exact::clipConvex(subject, clip);
// r.kind: ClipKind::Polygon | Segment | Point | Empty
// r.verts: CCW vertices (>=3) | segment endpoints (2) | point (1) | none
// r.area(): exact rational area (0 unless Polygon)
```

Supporting tools: `exact::orient` (exact orientation sign),
`exact::lineIntersect` (exact line-line intersection),
`exact::area2` (exact doubled signed area), and
`exact::satisfiesHalfPlanes` (independent half-plane verification of a result
against a convex polygon).

## Build & test

```sh
cmake -S . -B build && cmake --build build
cd build && ctest --output-on-failure
```

Tests cover: plain overlaps, shared/partially-shared edges (Segment results),
containment, reversed (CW) inputs, duplicate/collinear input vertices, point
touches, disjoint polygons, nearly parallel long edges (1e-12 tilt over 1e6
length), a 1e-18-area intersection that float thresholds would swallow,
exact rational intersection points, and degenerate point/segment inputs.
Every result is independently verified to satisfy the half-planes of both
input polygons and to be properly normalized.
