// Package promqlengine is a self-contained, importable PromQL evaluation
// engine focused on the exact timing semantics that distinguish a correct
// monitor from one that produces missed alerts or spurious spikes:
//
//   - Instant-vector lookback with a default 5m lookback delta and explicit
//     StaleNaN truncation (a stale marker drops the series; older samples are
//     never salvaged, while an ordinary arithmetic NaN is still emitted).
//   - rate/increase counter-reset detection and the Prometheus
//     half-interval (1.1x threshold / 0.5x clamp) extrapolation, including
//     the monotonic-counter zero clamp and the skip of windows with fewer
//     than two non-stale samples.
//   - Subquery [range:step] evaluation on a step-aligned grid that is
//     left-open and right-closed, anchored at the greatest step multiple not
//     exceeding the (offset/@ adjusted) evaluation time.
//   - @ and offset modifiers (offset may be negative), composable
//     independently on outer subqueries and inner selectors.
//
// Timestamps throughout the API are Unix milliseconds and durations are
// standard time.Duration values. Build queries either through the AST types
// directly or via ParseExpr/MustParseExpr.
//
//	store := promqlengine.NewMemStorage()
//	store.AddPoint(promqlengine.NewLabels("__name__","up"), 1000, 1)
//	eng := promqlengine.NewEngine(store)
//	v, err := eng.Eval(promqlengine.MustParseExpr(`up offset -1m @ 1000`), 5000)
//
// The package depends only on the Go standard library.
package promqlengine
