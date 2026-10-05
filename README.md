# ray_intersect

C++17 library computing the full intersection of a **rational ray** with the
interior of an **integer-vertex polygon with holes**, under the **even-odd
fill rule**. All arithmetic is exact — no floating point anywhere.

## Features

- **Exact arithmetic**: origins/directions are rationals (`Rational`,
  128-bit numerator/denominator); orientation tests are integer cross-product
  sign evaluations. Interval endpoints come out as exact rationals.
- **Even-odd fill**: every ring (outer + holes) toggles parity; ring
  orientation (CW/CCW) is irrelevant — reversing vertex order yields
  identical results.
- **Vertex hits counted once**: maximal runs of on-line vertices are collapsed
  into a single chain event. A chain toggles parity iff the two off-line
  vertices bracketing it lie on opposite sides of the ray (tangent chains and
  tangent vertices never toggle).
- **Boundary classified separately**: collinear overlaps are reported as
  `BoundarySegment`s, isolated touches/crossings as `BoundaryPoint`s; the
  interior is reported as open `InteriorInterval`s in the ray parameter `t`.
- **Source-edge evidence**: every interval endpoint, boundary point and
  boundary segment carries the `EdgeRef`s (ring index + edge index) that
  produced it.

## API

```cpp
#include "ray_intersect.hpp"
using namespace raypoly;

Polygon p;
p.outer = {{0,0},{20,0},{20,10},{0,10}};
p.holes = {{{5,3},{15,3},{15,6},{5,6}}};

Ray r = Ray::throughIntegers(-1, 3, 1, 0);   // or set Rational fields directly
Intersection res = intersectRayPolygon(r, p);
// res.interior          : open t-intervals inside the polygon
// res.boundary_points   : isolated t values on the boundary (with evidence)
// res.boundary_segments : t-intervals where the ray runs along edges
```

`InteriorInterval::lo_closed` is true only when `lo == 0` and the ray origin
is strictly inside; `hi_unbounded` marks a ray that never leaves the polygon.

## Build & test

```sh
cmake -S . -B build && cmake --build build
./build/ray_tests        # or: ctest --test-dir build
```

Tests cover: transverse crossings, vertex crossings (counted once), horizontal
edges collinear with the ray, collinear overlaps (incl. origin on the edge),
hole tangency along an edge and at a vertex, reversed vertex order, rational
ray parameters, nested even-odd rings, and toggling collinear chains.

## Precision limits

Intermediate products must fit in `__int128`. This is safe for coordinates
and rational denominators up to roughly 1e9 / 1e6 respectively (and far
beyond typical use); inputs near `int64` extremes with large denominators can
overflow.
