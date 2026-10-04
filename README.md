# Segmented Regression (Exact Arithmetic)

Fits a piecewise-linear function with a bounded number of segments to an
integer sequence `(x[i], y[i])`, minimizing the total residual sum of
squares (SSE). Every segment contains at least `minSegmentLength` points
(default 3). Requires Node.js >= 22.6 (runs TypeScript via type stripping,
no build step).

## Guarantees

- **Exact rational arithmetic.** All segment statistics are computed from
  BigInt prefix sums, and every candidate SSE is an exact `Fraction`
  (`src/fraction.ts`). Candidate comparisons and tie detection never
  involve floating point.
- **Tie-breaking.** Among segmentations with exactly equal total SSE, the
  one with the fewest segments wins; remaining ties prefer earlier
  breakpoints (deterministic).
- **Singular segments.** If every `x` in a segment is identical
  (`Sxx = 0`, e.g. from duplicate x-coordinates), the normal equations are
  singular and the slope is not identifiable. This is detected explicitly
  — never a silent division by zero — and the segment falls back to the
  minimum-norm least-squares fit: the horizontal line `y = mean(y)`
  (`slope = 0`, `intercept = mean(y)`), flagged with `degenerate: true`.

## Algorithm

Dynamic programming over `O(maxSegments * n^2)` candidate segments with
`O(1)` exact cost per segment from BigInt prefix sums:

```
Sxx = n*sum(x^2) - sum(x)^2        Sxy = n*sum(xy) - sum(x)*sum(y)
Syy = n*sum(y^2) - sum(y)^2
slope     = Sxy / Sxx                          (Sxx != 0)
intercept = (sum(y)*Sxx - Sxy*sum(x)) / (n*Sxx)
SSE       = (Syy*Sxx - Sxy^2) / (n*Sxx)        (>= 0, exact rational)
```

## Usage

Library:

```ts
import { fitSegmentedRegression } from "./src/segmentedRegression.ts";

const result = fitSegmentedRegression(xs, ys, /* maxSegments */ 3);
// result.segments[i]: { start, end, slope, intercept, sse, degenerate }
// result.breakpoints: start indices of segments after the first
// result.totalSSE:    exact Fraction ("p/q" via toString)
```

CLI (JSON in, JSON out; slopes/intercepts/SSE rendered as exact `"p/q"`):

```sh
echo '{"x":[0,1,2,3,4,5],"y":[1,3,5,8,7,6],"maxSegments":2}' | node src/cli.ts
node src/cli.ts input.json
```

Tests:

```sh
node --test
```
