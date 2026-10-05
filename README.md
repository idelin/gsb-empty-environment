# exactclip

Exact convex polygon clipping in C++17 with rational vertices. No floating
point and no epsilon thresholds anywhere: degenerate contacts and tiny sliver
areas are represented exactly.

## Layout

- `include/exactclip/bigint.hpp` — self-contained arbitrary-precision integer
  (`+ - * / %`, binary GCD, comparison, decimal output).
- `include/exactclip/rational.hpp` — exact rational, kept normalized
  (`den > 0`, `gcd(|num|, den) == 1`).
- `include/exactclip/clip.hpp` — geometry predicates, normalization, clipping,
  and independent verification helpers.
- `tests/test_clip.cpp` — test suite.

## Semantics

- Input polygons may be CW or CCW and may contain duplicate adjacent vertices
  and collinear edges; `normalizePolygon` removes duplicates/collinear
  vertices, forces CCW order, and rotates the lexicographically smallest
  vertex to the front.
- `clipConvex(subject, clip)` (Sutherland-Hodgman with exact orientation and
  exact line-intersection arithmetic) returns a `ClipResult` whose `kind`
  keeps point and line contacts distinct:
  - `Empty` — no contact (disjoint);
  - `Point` — exactly one contact point, `vertices = {p}`;
  - `Segment` — contact along a line segment, `vertices = {p, q}`;
  - `Polygon` — area overlap, `vertices` normalized CCW, strictly convex.
- A degenerate input (fewer than 3 vertices or zero area after normalization)
  is treated as having empty interior and yields `Empty`.

## Verification helpers

- `onOrInside(ccwPoly, p)` — exact half-plane test against every edge.
- `strictlyConvexCCW(poly)` — strict convexity and orientation check.
- `separated(a, b)` — exact separating-axis test; used to confirm that
  `Empty` results are genuinely disjoint.

## Build and test

```sh
cmake -B build -DCMAKE_BUILD_TYPE=Release
cmake --build build
ctest --test-dir build
```
