# motion

Computes the exact set of time intervals during which the squared distance
between two piecewise constant-velocity trajectories stays within a given
threshold.

```go
spans, err := motion.Coverage(trajA, trajB, maxDistSq)
```

## Model

- A `Trajectory` is a sequence of `Segment`s. A segment covers the
  **half-open** interval `[T0, T1)`; inside it the position is
  `P0 + V*(t-T0)`. Positions may jump at breakpoints, and gaps (undefined
  times) are allowed. The instant of a breakpoint belongs to the segment
  that starts there.
- `Coverage` returns the sorted, merged list of maximal `Span`s on which
  `|a(t)-b(t)|^2 <= maxDistSq`. Each endpoint carries an open/closed flag;
  endpoints that are roots of the pairwise squared-distance quadratic (not
  trajectory breakpoints) also carry a `QuadRoot` — the quadratic
  coefficients plus an isolating bracket that can be refined by bisection
  (`QuadRoot.Float64`) for exact endpoint ordering.
- A zero-width closed span `[t, t]` represents a grazing (tangency) instant.

## Method

For every pair of overlapping segments the squared distance is a single
quadratic `f(u) = au*u^2 + bu*u + c0` in local time, solved exactly
(upward parabola, linear, and constant cases; stable root formulas). The
per-pair solutions are intersected with the half-open overlap and merged
across breakpoints: two pieces join exactly when the instant at their
shared breakpoint is covered by at least one side. This correctly handles
grazing contacts, stationary pairs, and breakpoint open/closedness, and it
never relies on sampling segment endpoints — the vertex of the parabola
may lie strictly inside a pair.

## Tests

`go test ./...` runs unit tests for the edge cases above plus a randomized
cross-check against an independent reference that solves the same
inequalities with exact rational arithmetic (`math/big`), and direct
point-sample verification at breakpoint instants and dyadic grid times.
