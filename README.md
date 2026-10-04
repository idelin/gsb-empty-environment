# area_scaler

C++17 header-only area-weighted image scaler for integer pixel maps.

Each output pixel is the exact area-weighted average of the source pixels it
covers. All weights are rational numbers (integer numerators over a common
denominator), and rounding happens **exactly once**, at the very end —
the horizontal pass never rounds before the vertical pass.

## How it works

- **Rational weights** (`computeAxisWeights`): coordinates are rescaled so
  source pixel `i` spans `[i*dstN, (i+1)*dstN)` and output pixel `j` spans
  `[j*srcN, (j+1)*srcN)`. Overlap lengths are integers; per output they sum
  to `srcN`, the common denominator. No floating point anywhere.
- **Streaming row cache** (`Scaler::pushRow`): source rows are pushed one at
  a time. Each row is horizontally resampled once into exact rational sums
  (denominator `srcW`), then accumulated into every output row whose
  coverage window includes it. An output row is emitted — and rounded — as
  soon as its last contributing source row arrives. Memory is
  `O(active output rows * dstW)`, independent of `srcH`.
- **Single final rounding**: `out = (acc + denom/2) / denom` with
  `denom = srcW * srcH` (round half up), in 64-bit arithmetic.

## Usage

```cpp
#include "area_scaler.h"

// Whole image in one shot:
std::vector<uint8_t> dst = area_scale::Scaler<uint8_t>::scale(
    src, srcW, srcH, dstW, dstH);

// Or stream row by row:
area_scale::Scaler<uint8_t> s(srcW, srcH, dstW, dstH);
for (int y = 0; y < srcH; ++y)
    for (auto& [outY, row] : s.pushRow(&src[y * srcW]))
        consume(outY, row);
```

`Pixel` may be any non-negative integer pixel type (`uint8_t`, `uint16_t`,
...). Accumulators are `long long`; safe while
`maxPixel * srcW * srcH < 2^63`.

## Tests

```
cmake -S . -B build && cmake --build build && ctest --test-dir build
```

`tests/test_scaler.cpp` covers: constant images (bit-exact for all sizes,
including primes), single-pixel impulses (energy preservation, exact
coverage locality, edge/corner handling), prime dimensions up and down,
sharp no-double-rounding cases, streaming order/flush, identity, and random
images cross-checked against an independent `__int128` reference.
