# stroke-rasterizer

C++17 header-only library that rasterizes a stroked (widened) polyline into a
pixel bitmap. Supports miter, round, and bevel joins; a miter whose length
exceeds `miterLimit * width` falls back to bevel (SVG miterlimit convention).

## Usage

```cpp
#include "stroke_rasterizer/stroke_rasterizer.hpp"

stroke::Bitmap bm(64, 64);
stroke::StrokeOptions opt;
opt.width = 3.0;
opt.join = stroke::JoinStyle::kMiter;   // kMiter | kRound | kBevel
opt.cap = stroke::CapStyle::kButt;      // kButt | kSquare | kRound
opt.miterLimit = 4.0;

stroke::rasterize(bm, {{2, 5}, {12, 5}, {12, 12}}, opt);
bool covered = bm.get(12, 8);
```

## Conventions

- Pixel `(x, y)` is covered iff its center `(x + 0.5, y + 0.5)` lies inside the
  stroke. Shape tests are inclusive on the boundary, so segment rectangles and
  join shapes share full edges and leave no holes at joints.
- Round joins/caps compare squared distances (`dx*dx + dy*dy <= r*r`); the
  miter-limit test is likewise done on squared lengths. No square root is taken
  inside the rasterization loops.
- Zero-length segments (consecutive coincident points) are dropped. A fully
  degenerate path renders as a cap-shaped dot (round/square) or nothing (butt).
- A 180-degree foldback renders as a semicircular cap for round joins and as a
  bevel cap for bevel/miter joins (the miter would be infinitely long).
- Coverage is accumulated into the bitmap with OR semantics, so overlapping
  segments and joins never double-count pixels; `Bitmap::pixels()` returns each
  covered pixel exactly once.

## Build and test

```sh
cmake -B build && cmake --build build && ctest --test-dir build
```

or directly:

```sh
c++ -std=c++17 -Wall -Wextra -Iinclude tests/test_stroke_rasterizer.cpp -o stroke_tests
./stroke_tests
```
