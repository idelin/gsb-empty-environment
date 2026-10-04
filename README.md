# Segmented Regression Solver (exact arithmetic)

Fits at most `K` contiguous least-squares line segments to an integer
sequence `(x[i], y[i])`, minimizing the total squared error. Every segment
must contain at least `minSegmentLength` points (default 3). All candidate
comparisons use exact rational arithmetic (`Fraction`, BigInt-backed), so
rounding never influences the chosen partition.

## Usage

```bash
npm run build          # compile to dist/
npm test               # build + run the test suite
echo '{"x":[0,1,2,3,4,5,6,7,8,9],"y":[0,2,4,6,8,5,4,3,2,1],"maxSegments":3}' \
  | node dist/src/cli.js
```

Input JSON: `x`/`y` arrays (or `points` pairs), `maxSegments`,
`minSegmentLength` (default 3), `singular` (`"constant"` default,
`"reject"`, `"error"`). Integers may be JSON numbers or strings (for
values beyond 2^53). Output: breakpoints, per-segment exact slope /
intercept / SSE as `"p/q"` strings plus float approximations, and the
`singular` flag.

## Library

```ts
import { fitSegmentedRegression } from "./src/segmented-regression";
const r = fitSegmentedRegression(xs, ys, { maxSegments: 3 });
// r.breakpoints, r.segments[].slope/intercept/sse (Fraction), r.totalSse
```

## Tie-breaking

1. Minimize exact total SSE.
2. Equal SSE -> fewer segments.
3. Still tied -> deterministic breakpoint choice (smallest possible last
   breakpoint, recursively).

## Singular segments (repeated x-coordinates)

A segment whose x-values are all identical makes the normal-equation
denominator `n*Sxx - Sx^2` exactly zero. This is detected **before** any
division and handled explicitly via the `singular` option — the solver
never silently divides by zero:

- `"constant"` (default): fit `y = mean(y)`, the exact SSE minimizer for
  that degenerate design; the segment is flagged `singular: true`.
- `"reject"`: the segment is infeasible and the DP avoids it (throws
  `RangeError` if no feasible partition remains).
- `"error"`: throw `SingularSegmentError`.
