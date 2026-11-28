package promqlengine

import (
	"math"
	"testing"
	"time"
)

// TestStaleNaNBitPattern pins the exact IEEE-754 payload and proves it is
// distinct from an ordinary arithmetic NaN.
func TestStaleNaNBitPattern(t *testing.T) {
	if got := math.Float64bits(StaleNaN()); got != staleNaNBits {
		t.Fatalf("StaleNaN bits = %#x, want %#x", got, staleNaNBits)
	}
	if !IsStaleNaN(StaleNaN()) {
		t.Fatal("IsStaleNaN must recognize the canonical stale marker")
	}
	if IsStaleNaN(math.NaN()) {
		t.Fatal("an ordinary math.NaN must not be classified as a stale marker")
	}
	if IsStaleNaN(math.Float64frombits(0x7ff8000000000001)) {
		t.Fatal("a differently-payloaded quiet NaN must not be a stale marker")
	}
	if IsStaleNaN(1.5) || IsStaleNaN(0) {
		t.Fatal("finite values are never stale")
	}
}

// TestInstantSelectorStaleTruncation: when the newest sample inside the
// lookback window is a StaleNaN, the series must be dropped and older samples
// must never be salvaged.
func TestInstantSelectorStaleTruncation(t *testing.T) {
	const base int64 = 600_000
	metric := NewLabels("__name__", "up", "job", "api")

	store := NewMemStorage()
	// Fresh value, then an explicit stale marker 30s later.
	store.AddSeries(metric,
		Point{T: base - 60_000, V: 1},
		Point{T: base - 30_000, V: StaleNaN()},
	)

	eng := NewEngine(store)
	sel := &VectorSelector{Name: "up"}

	// Query at the stale marker: gone.
	if vec := mustEvalVector(t, eng, sel, base-30_000); len(vec) != 0 {
		t.Fatalf("at stale marker: expected no series, got %d", len(vec))
	}
	// Query after the marker but still inside lookback: still gone, no
	// crossing back to the value at base-60s.
	if vec := mustEvalVector(t, eng, sel, base); len(vec) != 0 {
		t.Fatalf("after stale marker: expected no series, got %d", len(vec))
	}
	// Query just before the marker: the older live value is visible.
	vec := mustEvalVector(t, eng, sel, base-30_001)
	if len(vec) != 1 || vec[0].V != 1 {
		t.Fatalf("before stale marker: expected single value 1, got %+v", vec)
	}
}

// TestInstantSelectorRegularNaNKept: an arithmetic NaN is emitted normally;
// only the specific stale payload truncates.
func TestInstantSelectorRegularNaNKept(t *testing.T) {
	const base int64 = 600_000
	metric := NewLabels("__name__", "x")

	store := NewMemStorage()
	store.AddSeries(metric, Point{T: base - 10_000, V: math.NaN()})

	eng := NewEngine(store)
	vec := mustEvalVector(t, eng, &VectorSelector{Name: "x"}, base)
	if len(vec) != 1 {
		t.Fatalf("expected 1 series, got %d", len(vec))
	}
	if !math.IsNaN(vec[0].V) || IsStaleNaN(vec[0].V) {
		t.Fatalf("expected a regular NaN sample, got %v", vec[0].V)
	}
	if vec[0].T != base {
		t.Fatalf("instant sample must be stamped at evaluation time, got %d", vec[0].T)
	}
}

// TestLookbackWindowBoundary verifies the half-open window (t-lookback, t]:
// a point exactly at the left boundary is excluded while one at t is used.
func TestLookbackWindowBoundary(t *testing.T) {
	const base int64 = 1_000_000
	store := NewMemStorage()
	metric := NewLabels("__name__", "m")
	store.AddSeries(metric,
		Point{T: base - int64(5*time.Minute/time.Millisecond), V: 7}, // at left edge: excluded
		Point{T: base, V: 9},
	)
	eng := NewEngine(store)
	vec := mustEvalVector(t, eng, &VectorSelector{Name: "m"}, base)
	if len(vec) != 1 || vec[0].V != 9 {
		t.Fatalf("expected right-edge value 9, got %+v", vec)
	}

	// With only the boundary point present, nothing is selected.
	store2 := NewMemStorage()
	store2.AddPoint(metric, base-int64(5*time.Minute/time.Millisecond), 7)
	eng2 := NewEngine(store2)
	if vec := mustEvalVector(t, eng2, &VectorSelector{Name: "m"}, base); len(vec) != 0 {
		t.Fatalf("point at excluded left boundary must not be selected, got %+v", vec)
	}
}

// TestRangeSelectorStaleTruncation verifies that a stale marker inside a
// range window truncates subsequent points and that a stale-only window
// contributes no series.
func TestRangeSelectorStaleTruncation(t *testing.T) {
	const base int64 = 600_000
	metric := NewLabels("__name__", "c")
	store := NewMemStorage()
	store.AddSeries(metric,
		Point{T: base - 40_000, V: 1},
		Point{T: base - 30_000, V: StaleNaN()},
		Point{T: base - 20_000, V: 2}, // must be hidden behind the marker
		Point{T: base - 10_000, V: 3},
	)
	eng := NewEngine(store)
	rs := &RangeSelector{
		Vector: &VectorSelector{Name: "c"},
		Range:  time.Minute,
	}
	mat, err := eng.Eval(rs, base)
	if err != nil {
		t.Fatal(err)
	}
	matrix := mat.(Matrix)
	if len(matrix.Series) != 1 {
		t.Fatalf("expected 1 truncated series, got %d", len(matrix.Series))
	}
	pts := matrix.Series[0].Points
	if len(pts) != 1 || pts[0].V != 1 {
		t.Fatalf("expected only the pre-stale point, got %+v", pts)
	}

	// A window whose only point is stale contributes nothing.
	store2 := NewMemStorage()
	store2.AddPoint(metric, base-5_000, StaleNaN())
	eng2 := NewEngine(store2)
	mat2, err := eng2.Eval(&RangeSelector{Vector: &VectorSelector{Name: "c"}, Range: time.Minute}, base)
	if err != nil {
		t.Fatal(err)
	}
	if len(mat2.(Matrix).Series) != 0 {
		t.Fatalf("stale-only window must yield no series, got %+v", mat2)
	}
}

func mustEvalVector(t *testing.T, eng *Engine, expr Expr, at int64) []Sample {
	t.Helper()
	val, err := eng.Eval(expr, at)
	if err != nil {
		t.Fatalf("eval error: %v", err)
	}
	vec, ok := val.([]Sample)
	if !ok {
		t.Fatalf("expected instant vector, got %T", val)
	}
	return vec
}
