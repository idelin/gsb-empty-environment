# raypoly

C++17 header-only library: intersect a rational ray with the interior of an
even-odd filled polygon (holes supported), with exact arithmetic and
boundary-evidence output.

## Layout

- `include/raypoly/bigint.hpp` — arbitrary-precision signed integer
- `include/raypoly/rational.hpp` — exact rational (normalized num/den)
- `include/raypoly/ray_intersect.hpp` — geometry, predicates, intersection
- `tests/test_ray_intersect.cpp` — self-contained test suite (no deps)

## API

```cpp
#include <raypoly/ray_intersect.hpp>
using namespace raypoly;

Polygon poly;
poly.outer = {{0,0},{4,0},{4,4},{0,4}};        // integer literals convert
poly.holes.push_back({{1,1},{2,1},{2,2},{1,2}});

Ray ray{Point{-1, 2}, Point{1, 0}};             // p(t) = origin + t*dir, t >= 0
RayIntersection ri = intersect(ray, poly);
```

`RayIntersection` contains:

- `interior`: maximal parameter intervals where the ray is inside the polygon.
  Each `Interval` has `lo`/`hi` (`Rational`), open/closed flags (`lo_closed`
  only when the origin itself is interior), `hi_infinite`, and
  `lo_edges`/`hi_edges` — the `EdgeRef` evidence (ring, edge index, edge
  parameter `s`) for the boundary that bounds the interval.
- `boundary`: `BoundaryPiece`s where the ray touches the boundary. `lo == hi`
  is an isolated point (e.g. a vertex touch); `lo < hi` is a collinear
  overlap with one or more edges.

`locate(point, poly)` classifies a point as `Inside` / `Outside` / `Boundary`.

## Guarantees

- **Exact arithmetic**: all predicates (orientation, crossing tests) and
  outputs are computed with arbitrary-precision rationals — no floating point.
- **Even-odd fill**: interior is decided by parity over all rings (outer +
  holes); winding order is irrelevant.
- **Boundary classified separately**: points/segments on edges never appear
  in `interior`; they are reported in `boundary`.
- **Vertices counted once**: events are keyed by ray parameter `t`, so a
  vertex shared by two edges produces a single critical parameter (both edges
  are attached as evidence).
- **Tangencies**: a tangent touch (e.g. grazing a hole vertex) does not
  toggle parity; the interior intervals on both sides are split only by the
  boundary point itself.

## Algorithm

1. For every edge, solve the ray/edge intersection exactly: a transversal
   crossing yields a point event at parameter `t`; a collinear edge yields an
   overlap segment clipped to `t >= 0`.
2. Collect all critical parameters (point events + overlap endpoints) into a
   sorted, deduplicated set.
3. Sample one rational point per open cell between consecutive critical
   parameters and classify it with an exact even-odd point-in-polygon test.
   Cells inside the polygon become interior intervals; critical parameters
   and overlap spans become boundary pieces.

## Build & test

```sh
cmake -S . -B build && cmake --build build && ctest --test-dir build
# or directly:
c++ -std=c++17 -Iinclude tests/test_ray_intersect.cpp -o raypoly_tests && ./raypoly_tests
```

Tests cover: horizontal edges (transversal and collinear overlap), vertices
counted once, hole vertex crossing, hole tangency, hole edge overlap, ray
starting inside, exact rational parameters (slope 1/2), misses, and reversed
ring vertex order producing identical results.
