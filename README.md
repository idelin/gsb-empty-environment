# rational_resampler

Exact rational-ratio FIR resampler for integer sample streams (C++17).

## Model

Upsample by `P`, apply FIR kernel `h` (length `L`), downsample by `Q`:

```
y[n] = sum_k x[k] * h[n*Q - k*P],   0 <= n*Q - k*P < L
```

- Kernel coefficients are exact rationals (`Rational{num, den}`). All
  filtering runs in exact integer arithmetic over the coefficients' common
  denominator (128-bit accumulator); rounding to integer happens exactly once
  per output sample, at emission time (round-half-away-from-zero).
- Non-coprime `P`/`Q` are normalized by `gcd(P, Q)` and the kernel is
  decimated accordingly — mathematically identical, since only every
  gcd-th tap is reachable.

## Streaming guarantees

`process(chunk)` may be called with any chunking of the input — including
empty and single-sample chunks — and the concatenated output of
`process(...)` calls plus a final `flush()` is sample-identical to one-shot
`resample(...)`. `flush()` emits the filter tail; total output length for `N`
input samples is `floor(((N-1)*P + L-1) / Q) + 1` (0 for empty input), also
available via `output_size_for(N)`.

## Usage

```cpp
#include "rational_resampler.hpp"

rrs::RationalResampler r(3, 2, {{1, 2}, {1, 1}, {1, 2}});  // 3/2 ratio
auto out0 = r.process({10, -4, 7});   // determined prefix
auto out1 = r.process({});            // empty chunk: fine
auto tail = r.flush();                // end of stream: filter tail

// one-shot:
auto all = rrs::RationalResampler::resample(samples, 3, 2, kernel);
```

`process` after `flush`, a second `flush`, non-positive factors, empty
kernel, or non-positive coefficient denominators throw.

## Build & test

```sh
cmake -S . -B build && cmake --build build
ctest --test-dir build          # or: ./build/test_resampler
```
