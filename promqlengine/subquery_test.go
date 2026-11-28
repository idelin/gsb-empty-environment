package promqlengine

import (
	"reflect"
	"testing"
	"time"
)

// evalMatrix runs an expression expected to yield a Matrix and returns it.
func evalMatrix(t *testing.T, eng *Engine, expr Expr, at int64) Matrix {
	t.Helper()
	val, err := eng.Eval(expr, at)
	if err != nil {
		t.Fatalf("eval error: %v", err)
	}
	mat, ok := val.(Matrix)
	if !ok {
		t.Fatalf("expected Matrix, got %T", val)
	}
	return mat
}

func pointTimes(s Series) []int64 {
	out := make([]int64, len(s.Points))
	for i, p := range s.Points {
		out[i] = p.T
	}
	return out
}

// TestSubqueryGridAlignment: the grid is anchored at the greatest step
// multiple <= t, then walks left while staying strictly greater than
// t-range, giving a left-open / right-closed set.
func TestSubqueryGridAlignment(t *testing.T) {
	store := NewMemStorage()
	// Dense points so every grid evaluation finds a live sample.
	metric := NewLabels("__name__", "m")
	for ts := int64(0); ts <= 100_000; ts += 1_000 {
		store.AddPoint(metric, ts, float64(ts))
	}
	eng := NewEngine(store)

	// [30s:10s] evaluated at t=55s. Anchor = floor(55,10) = 50s; window
	// (25s, 55s]; grid points strictly > 25: 30,40,50.
	sq := &SubqueryExpr{
		Inner: &VectorSelector{Name: "m"},
		Range: 30 * time.Second,
		Step:  10 * time.Second,
	}
	mat := evalMatrix(t, eng, sq, 55_000)
	if mat.RangeStartMs != 25_000 || mat.RangeEndMs != 55_000 {
		t.Fatalf("window bounds = (%d,%d], want (25000,55000]", mat.RangeStartMs, mat.RangeEndMs)
	}
	want := []int64{30_000, 40_000, 50_000}
	if len(mat.Series) != 1 {
		t.Fatalf("expected 1 series, got %d", len(mat.Series))
	}
	if got := pointTimes(mat.Series[0]); !reflect.DeepEqual(got, want) {
		t.Fatalf("grid = %v, want %v", got, want)
	}
}

// TestSubqueryLeftBoundaryExcluded: a grid point exactly at t-range must be
// excluded (left-open), while the right anchor is included.
func TestSubqueryLeftBoundaryExcluded(t *testing.T) {
	store := NewMemStorage()
	metric := NewLabels("__name__", "m")
	for ts := int64(0); ts <= 100_000; ts += 1_000 {
		store.AddPoint(metric, ts, 1)
	}
	eng := NewEngine(store)

	// [40s:10s] at t=60s: window (20s,60s]; aligned points >20 are
	// 30,40,50,60. The point at 20s (= t-range) is excluded.
	sq := &SubqueryExpr{
		Inner: &VectorSelector{Name: "m"},
		Range: 40 * time.Second,
		Step:  10 * time.Second,
	}
	mat := evalMatrix(t, eng, sq, 60_000)
	want := []int64{30_000, 40_000, 50_000, 60_000}
	if got := pointTimes(mat.Series[0]); !reflect.DeepEqual(got, want) {
		t.Fatalf("grid = %v, want %v", got, want)
	}
}

// TestSubqueryRightAnchorOnGrid: when t is itself a step multiple, the grid
// includes t as its rightmost point.
func TestSubqueryRightAnchorOnGrid(t *testing.T) {
	store := NewMemStorage()
	metric := NewLabels("__name__", "m")
	for ts := int64(0); ts <= 100_000; ts += 1_000 {
		store.AddPoint(metric, ts, 1)
	}
	eng := NewEngine(store)

	sq := &SubqueryExpr{
		Inner: &VectorSelector{Name: "m"},
		Range: 25 * time.Second,
		Step:  10 * time.Second,
	}
	// t=50s on grid; window (25s,50s]: points 30,40,50.
	mat := evalMatrix(t, eng, sq, 50_000)
	want := []int64{30_000, 40_000, 50_000}
	if got := pointTimes(mat.Series[0]); !reflect.DeepEqual(got, want) {
		t.Fatalf("grid = %v, want %v", got, want)
	}
}

// TestSubqueryStaleGaps: when the inner selector is stale at some grid
// points, those points simply do not appear, demonstrating per-point
// recursive evaluation.
func TestSubqueryStaleGaps(t *testing.T) {
	store := NewMemStorage()
	metric := NewLabels("__name__", "m")
	store.AddSeries(metric,
		Point{T: 30_000, V: 1},
		Point{T: 40_000, V: StaleNaN()},
		Point{T: 50_000, V: 3},
	)
	eng := NewEngine(store)
	sq := &SubqueryExpr{
		Inner: &VectorSelector{Name: "m"},
		Range: 30 * time.Second,
		Step:  10 * time.Second,
	}
	// Window (25s,55s]: grid 30,40,50. At 40 the series is stale -> omitted.
	mat := evalMatrix(t, eng, sq, 55_000)
	want := []Point{{T: 30_000, V: 1}, {T: 50_000, V: 3}}
	if len(mat.Series) != 1 {
		t.Fatalf("expected 1 series, got %d", len(mat.Series))
	}
	if got := mat.Series[0].Points; !reflect.DeepEqual(got, want) {
		t.Fatalf("points = %+v, want %+v", got, want)
	}
}

// TestSubqueryInvalidArgs guards the validation errors.
func TestSubqueryInvalidArgs(t *testing.T) {
	eng := NewEngine(NewMemStorage())
	if _, err := eng.Eval(&SubqueryExpr{
		Inner: &VectorSelector{Name: "m"},
		Range: time.Minute,
		Step:  0,
	}, 0); err == nil {
		t.Fatal("zero step must be rejected")
	}
	if _, err := eng.Eval(&SubqueryExpr{
		Inner: &VectorSelector{Name: "m"},
		Range: 0,
		Step:  time.Minute,
	}, 0); err == nil {
		t.Fatal("zero range must be rejected")
	}
}

// TestFloorStep covers alignment including negative (pre-epoch) timestamps.
func TestFloorStep(t *testing.T) {
	cases := []struct {
		t, step, want int64
	}{
		{55_000, 10_000, 50_000},
		{50_000, 10_000, 50_000},
		{0, 10_000, 0},
		{-5_000, 10_000, -10_000},
		{-15_000, 10_000, -20_000},
		{-10_000, 10_000, -10_000},
	}
	for _, c := range cases {
		if got := floorStep(c.t, c.step); got != c.want {
			t.Errorf("floorStep(%d,%d) = %d, want %d", c.t, c.step, got, c.want)
		}
	}
}
