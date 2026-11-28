# promqlengine

An importable, zero-dependency Go package implementing the precise timing and
extrapolation semantics of a Prometheus PromQL evaluation engine. It is built
for monitoring/observability engines that need Prometheus-compatible behavior
without pulling in the full Prometheus server.

## Semantics implemented

### 1. Lookback delta and explicit `StaleNaN` truncation

An instant vector selector at time `t` reads the newest sample in
`(t-lookbackDelta, t]` (default `5m`, configurable via
`WithLookbackDelta`).

- If the newest sample is the canonical stale marker (`StaleNaN`, IEEE-754
  payload `0x7ff8000000000002`), the series is considered gone and is **not**
  emitted. The engine never crosses the marker to recover an older value.
- An ordinary arithmetic NaN (`math.NaN()`, payload `...0001`) is a regular
  value and is emitted. `IsStaleNaN` performs an exact bitwise distinction.
- A range selector truncates a series at its first stale marker, matching the
  storage iterator contract that seeks stop at a stale marker.

### 2. `rate` / `increase` counter resets and extrapolation

`extrapolatedRate` is a faithful port of `promql/functions.go`:

- Windows with fewer than two non-stale samples contribute no series.
- A sample below its predecessor is a counter reset: the new value is added
  instead of the negative difference. A negative first sample is folded in.
- The average sample interval comes from the first/last timestamps.
- Each edge gap beyond `1.1 * avgSampleInterval` is clamped to
  `0.5 * avgSampleInterval`.
- For counters the left extrapolation is zero-clamped: linear
  back-extrapolation never crosses the zero point.
- Both `rate` and `increase` apply counter/reset handling; only `rate`
  additionally divides by the window length in seconds.

### 3. Subquery step grid alignment

For `subexpr[range:step]` evaluated at `t` (after applying the subquery's own
`@`/`offset`):

- The anchor is the greatest multiple of `step` not exceeding the effective
  time (`floorStep`, correct for negative pre-epoch timestamps).
- The grid walks backward by `step` while staying strictly greater than
  `t-range`, so the window is **left-open** (a point exactly at `t-range` is
  excluded) and **right-closed** (the anchor, when on the grid, is included).
- The inner expression is recursively evaluated at every grid point and the
  per-point vectors are merged into a `Matrix`.

### 4. `@` and `offset` (including negative offset)

- `@ T` pins the base timestamp; the query time is ignored.
- `offset D` is always added on top of the base; negative offsets move the
  read into the future.
- Outer subqueries and inner selectors carry independent modifiers, each
  honored at every recursive evaluation.

## Layout

- `labels.go`, `value.go` — label sets, samples, series, `StaleNaN`.
- `storage.go` — `Storage` interface, matchers, in-memory implementation.
- `ast.go`, `engine.go` — expression nodes and the evaluator.
- `functions.go` — `rate`/`increase` and the extrapolation algorithm.
- `duration.go`, `lexer.go`, `parser.go` — PromQL text parsing for the
  supported subset.
- `*_test.go` — acceptance tests for every semantic boundary above.

## Quick start

```go
store := promqlengine.NewMemStorage()
store.AddSeries(
    promqlengine.NewLabels("__name__", "requests"),
    promqlengine.Point{T: 0, V: 0},
    promqlengine.Point{T: 10000, V: 10},
    promqlengine.Point{T: 20000, V: 5}, // counter reset
)
eng := promqlengine.NewEngine(store)

v, err := eng.Eval(
    promqlengine.MustParseExpr(`rate(requests[20s]) offset -1m @ 1000`),
    40000,
)
```

Run the tests with `go test ./...`.
