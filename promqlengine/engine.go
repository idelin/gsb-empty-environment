package promqlengine

import (
	"errors"
	"fmt"
	"time"
)

// DefaultLookbackDelta is the Prometheus default: an instant selector looks
// back 5 minutes for the most recent sample.
const DefaultLookbackDelta = 5 * time.Minute

// MetricNameLabel is the implicit label carrying the metric name.
const MetricNameLabel = "__name__"

// Engine evaluates PromQL expressions against a Storage.
//
// Construct one with NewEngine and reuse it across queries. Evaluation holds
// no per-query state on the Engine; concurrent queries are safe as long as the
// backing Storage is not being mutated concurrently.
type Engine struct {
	store         Storage
	lookbackDelta time.Duration
}

// NewEngine builds an engine with the default 5m lookback delta.
func NewEngine(store Storage) *Engine {
	return &Engine{store: store, lookbackDelta: DefaultLookbackDelta}
}

// WithLookbackDelta overrides the lookback delta and returns the engine.
func (e *Engine) WithLookbackDelta(d time.Duration) *Engine {
	if d <= 0 {
		panic("promqlengine: lookback delta must be positive")
	}
	e.lookbackDelta = d
	return e
}

// LookbackDelta returns the configured lookback delta.
func (e *Engine) LookbackDelta() time.Duration { return e.lookbackDelta }

// Eval evaluates expr at query timestamp tMs (Unix milliseconds).
//
// The concrete result is []Sample for instant-vector expressions and
// []Series for matrix expressions.
func (e *Engine) Eval(expr Expr, tMs int64) (Value, error) {
	if expr == nil {
		return nil, errors.New("promqlengine: nil expression")
	}
	return e.evalExpr(expr, tMs)
}

func (e *Engine) evalExpr(expr Expr, tMs int64) (Value, error) {
	switch n := expr.(type) {
	case *VectorSelector:
		return e.evalInstantSelector(n, tMs), nil
	case *RangeSelector:
		return e.evalRangeSelector(n, tMs), nil
	case *SubqueryExpr:
		return e.evalSubquery(n, tMs)
	case *Call:
		return e.evalCall(n, tMs)
	default:
		return nil, fmt.Errorf("promqlengine: unsupported expression %T", expr)
	}
}

// selectorMatchers prepends the __name__ equality predicate when the selector
// names a metric.
func selectorMatchers(v *VectorSelector) []*Matcher {
	matchers := make([]*Matcher, 0, len(v.Matchers)+1)
	if v.Name != "" {
		matchers = append(matchers, MustMatcher(MetricNameLabel, MatchEqual, v.Name))
	}
	matchers = append(matchers, v.Matchers...)
	return matchers
}

// evalInstantSelector implements the lookback-delta rule.
//
// At effective evaluation time et the selector reads points in
// (et-lookback, et], takes the newest one, and:
//   - if that point is a StaleNaN, the series is considered gone and is NOT
//     emitted; older points behind the marker are never salvaged;
//   - otherwise the value is emitted stamped at et, including a regular NaN
//     produced by arithmetic.
func (e *Engine) evalInstantSelector(v *VectorSelector, tMs int64) []Sample {
	et := v.effectiveTime(tMs)
	start := et - e.lookbackDelta.Milliseconds()
	raw := e.store.Select(start, et, selectorMatchers(v))

	out := make([]Sample, 0, len(raw))
	for _, s := range raw {
		if len(s.Points) == 0 {
			continue
		}
		last := s.Points[len(s.Points)-1]
		if IsStaleNaN(last.V) {
			// Explicit stale marker: hard stop for this series at et.
			continue
		}
		out = append(out, Sample{Metric: s.Metric, Point: Point{T: et, V: last.V}})
	}
	sortVector(out)
	return out
}

// evalRangeSelector collects window points for a matrix selector.
//
// The window is (et-range, et]. Stale markers truncate the series: every
// point at or after the first StaleNaN is discarded, mirroring the storage
// iterator contract that Seek/At stop at a stale marker. Regular NaNs are
// retained so arithmetic NaN propagation stays observable.
func (e *Engine) evalRangeSelector(r *RangeSelector, tMs int64) Matrix {
	et := r.Vector.effectiveTime(tMs)
	start := et - r.Range.Milliseconds()
	raw := e.store.Select(start, et, selectorMatchers(r.Vector))

	out := make([]Series, 0, len(raw))
	for _, s := range raw {
		points := make([]Point, 0, len(s.Points))
		for _, p := range s.Points {
			if IsStaleNaN(p.V) {
				break
			}
			points = append(points, p)
		}
		if len(points) == 0 {
			continue
		}
		out = append(out, Series{Metric: s.Metric, Points: points})
	}
	sortMatrix(out)
	return Matrix{Series: out, RangeStartMs: start, RangeEndMs: et}
}

// evalSubquery builds the step-aligned grid and recursively evaluates Inner.
//
// The grid anchor is the greatest step multiple not exceeding the effective
// evaluation time et (after @/offset). It then walks backward by step while
// staying strictly inside the left boundary, so the window is left-open (a
// point exactly at et-range is excluded) and right-closed (et itself, when on
// the grid, is included). Inner carries its own independent @/offset, which
// take effect at every grid evaluation.
func (e *Engine) evalSubquery(sq *SubqueryExpr, tMs int64) (Matrix, error) {
	if sq.Step <= 0 {
		return Matrix{}, errors.New("promqlengine: subquery step must be positive")
	}
	if sq.Range <= 0 {
		return Matrix{}, errors.New("promqlengine: subquery range must be positive")
	}

	et := sq.effectiveTime(tMs)
	stepMs := sq.Step.Milliseconds()
	leftBoundary := et - sq.Range.Milliseconds()

	// Build the grid descending from the aligned anchor, then reverse.
	var grid []int64
	for g := floorStep(et, stepMs); g > leftBoundary; g -= stepMs {
		grid = append(grid, g)
	}
	reverseInt64(grid)

	merged := map[string]*Series{}
	for _, gridTime := range grid {
		val, err := e.evalExpr(sq.Inner, gridTime)
		if err != nil {
			return Matrix{}, err
		}
		vec, ok := val.([]Sample)
		if !ok {
			return Matrix{}, fmt.Errorf("promqlengine: subquery inner expression %s must return an instant vector", sq.Inner.nodeType())
		}
		for _, samp := range vec {
			fp := samp.Metric.Fingerprint()
			ser, ok := merged[fp]
			if !ok {
				ser = &Series{Metric: samp.Metric}
				merged[fp] = ser
			}
			ser.Points = append(ser.Points, Point{T: gridTime, V: samp.V})
		}
	}

	out := make([]Series, 0, len(merged))
	for _, ser := range merged {
		out = append(out, *ser)
	}
	sortMatrix(out)
	return Matrix{Series: out, RangeStartMs: leftBoundary, RangeEndMs: et}, nil
}

// floorStep returns the greatest multiple of step not exceeding t. A custom
// modulo is required because Go's % keeps the sign of the dividend, which
// would misalign negative (pre-epoch) timestamps.
func floorStep(t, step int64) int64 {
	rem := t % step
	if t < 0 && rem != 0 {
		rem += step
	}
	return t - rem
}

func reverseInt64(s []int64) {
	for i, j := 0, len(s)-1; i < j; i, j = i+1, j-1 {
		s[i], s[j] = s[j], s[i]
	}
}
