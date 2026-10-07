# rational-ticks

Linear/log scales from an exact rational domain to a pixel range, with
non-repeating tick labels. Pure TypeScript, zero dependencies.

Requires Node.js >= 22.18 (runs `.ts` directly via type stripping).

## Usage

```ts
import { createScale, rat } from './src/index.ts';

const scale = createScale({
  domain: [rat(1, 3), rat(1000, 7)], // exact rationals: num/den, decimal strings, bigints
  range: [100, 800],                 // pixels; inverted ranges like [800, 100] are legal
  mode: 'linear',                    // or 'log' (optional base, default 10)
});

scale.map(rat(2, 3));   // -> pixel (never clamps)
scale.invert(450);      // -> exact Rational (never clamps)
scale.clip(900);        // -> 800; clipping is a separate, explicit operation
scale.ticks(10);        // -> [{ value: Rational, label: string }, ...]
```

## Contract

- **Rational domain**: endpoints and tick values are exact bigints-based
  rationals (`{ n, d }`, normalized). `rat('0.001')` parses decimals exactly.
- **Log mode rejects non-positive values**: the constructor throws if the
  domain is not strictly positive, and `map` throws a `RangeError` for any
  value `<= 0`.
- **Inverse mapping and clipping are separate**: `map`/`invert` never clamp;
  use `clip` to clamp pixels into the (possibly inverted) range.
- **Inverted pixel ranges are legal**: `range: [500, -100]` simply flips the
  direction; `clip` still works. Inverted domains are accepted too.
- **Tick labels never repeat**: each `ticks()` call raises label precision
  until labels are unique (falling back to exact `n/d` notation), and every
  tick carries both the display `label` and the exact rational `value`.

## Tests

```sh
node --test test/*.test.ts
```

Covers round-trip (`invert(map(v)) ~= v`) and strict monotonicity for linear
and log modes, inverted ranges, 1e-9 spans, exact boundary endpoints,
non-positive rejection in log mode, and tick-label uniqueness.
