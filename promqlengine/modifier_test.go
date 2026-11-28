package promqlengine

import (
	"reflect"
	"testing"
	"time"
)

// TestEffectiveTime pins the @/offset composition: @ pins the base, offset is
// always added on top and may be negative.
func TestEffectiveTime(t *testing.T) {
	cases := []struct {
		name string
		mod  timeModifier
		t    int64
		want int64
	}{
		{"plain", timeModifier{}, 100_000, 100_000},
		{"positive offset", timeModifier{Offset: 2 * time.Minute}, 100_000, -20_000},
		{"negative offset", timeModifier{Offset: -5 * time.Minute}, 100_000, 400_000},
		{"at only", timeModifier{HasAt: true, AtMs: 1700000000000}, 999, 1700000000000},
		{"at plus offset", timeModifier{HasAt: true, AtMs: 1700000000000, Offset: 2 * time.Minute}, 999, 1700000000000 - 120_000},
		{"at plus negative offset", timeModifier{HasAt: true, AtMs: 1700000000000, Offset: -time.Minute}, 999, 1700000000000 + 60_000},
	}
	for _, c := range cases {
		if got := c.mod.effectiveTime(c.t); got != c.want {
			t.Errorf("%s: effectiveTime = %d, want %d", c.name, got, c.want)
		}
	}
}

// TestNegativeOffsetSelector: `metric offset -5m` reads 5 minutes in the
// future relative to the query time.
func TestNegativeOffsetSelector(t *testing.T) {
	store := NewMemStorage()
	metric := NewLabels("__name__", "m")
	store.AddSeries(metric,
		Point{T: 600_000, V: 1},
		Point{T: 900_000, V: 2},
	)
	eng := NewEngine(store)

	// Query at 600s with -5m offset -> effective time 900s, reads value 2.
	sel := &VectorSelector{Name: "m", timeModifier: timeModifier{Offset: -5 * time.Minute}}
	vec := mustEvalVector(t, eng, sel, 600_000)
	if len(vec) != 1 || vec[0].V != 2 {
		t.Fatalf("negative offset lookup = %+v, want value 2", vec)
	}

	// Positive offset moves into the past.
	selPast := &VectorSelector{Name: "m", timeModifier: timeModifier{Offset: 5 * time.Minute}}
	if vec := mustEvalVector(t, eng, selPast, 900_000); len(vec) != 1 || vec[0].V != 1 {
		t.Fatalf("positive offset lookup = %+v, want value 1", vec)
	}
}

// TestAtModifierPinsTime: @ locks the evaluation timestamp regardless of the
// query time, and offset still applies on top of the pinned base.
func TestAtModifierPinsTime(t *testing.T) {
	const at int64 = 1_700_000_000_000
	store := NewMemStorage()
	metric := NewLabels("__name__", "m")
	store.AddSeries(metric,
		Point{T: at - 120_000, V: 7}, // 2m before @
		Point{T: at, V: 9},           // at @
		Point{T: at + 60_000, V: 11}, // 1m after @
	)
	eng := NewEngine(store)

	// @ alone: reads the sample at the pinned time.
	atSel := &VectorSelector{Name: "m", timeModifier: timeModifier{HasAt: true, AtMs: at}}
	for _, qt := range []int64{0, 5_000_000, at} {
		vec := mustEvalVector(t, eng, atSel, qt)
		if len(vec) != 1 || vec[0].V != 9 {
			t.Fatalf("qt=%d: @ selector = %+v, want 9", qt, vec)
		}
	}

	// @ with +2m offset: effective = at - 2m -> value 7.
	atOffset := &VectorSelector{Name: "m", timeModifier: timeModifier{
		HasAt: true, AtMs: at, Offset: 2 * time.Minute,
	}}
	vec := mustEvalVector(t, eng, atOffset, at)
	if len(vec) != 1 || vec[0].V != 7 {
		t.Fatalf("@ + offset selector = %+v, want 7", vec)
	}

	// @ with -1m offset: effective = at + 1m -> value 11.
	atNeg := &VectorSelector{Name: "m", timeModifier: timeModifier{
		HasAt: true, AtMs: at, Offset: -time.Minute,
	}}
	vec = mustEvalVector(t, eng, atNeg, at)
	if len(vec) != 1 || vec[0].V != 11 {
		t.Fatalf("@ + negative offset selector = %+v, want 11", vec)
	}
}

// TestSubqueryAtAndOffset: the subquery's own @/offset shifts the whole grid,
// independent of the inner selector's modifiers.
func TestSubqueryAtAndOffset(t *testing.T) {
	store := NewMemStorage()
	metric := NewLabels("__name__", "m")
	for ts := int64(0); ts < 2_000_000; ts += 1_000 {
		store.AddPoint(metric, ts, 1)
	}
	eng := NewEngine(store)

	const at int64 = 1_000_000
	sq := &SubqueryExpr{
		Inner:        &VectorSelector{Name: "m"},
		Range:        30 * time.Second,
		Step:         10 * time.Second,
		timeModifier: timeModifier{HasAt: true, AtMs: at},
	}
	// @ pins et=1s; window (970ms,1s]; grid 980,990,1000 regardless of query.
	mat := evalMatrix(t, eng, sq, 42_000)
	want := []int64{980_000, 990_000, 1_000_000}
	if got := pointTimes(mat.Series[0]); !reflect.DeepEqual(got, want) {
		t.Fatalf("@ subquery grid = %v, want %v", got, want)
	}

	// Add a +20s offset: et = at - 20s = 980s; window (950,980]; grid 960,970,980.
	sq.Offset = 20 * time.Second
	mat = evalMatrix(t, eng, sq, 42_000)
	want = []int64{960_000, 970_000, 980_000}
	if got := pointTimes(mat.Series[0]); !reflect.DeepEqual(got, want) {
		t.Fatalf("@+offset subquery grid = %v, want %v", got, want)
	}
}

// TestNestedSubqueryIndependentModifiers: an outer subquery and an inner
// selector each carry their own @/offset, and both take effect at every grid
// evaluation.
func TestNestedSubqueryIndependentModifiers(t *testing.T) {
	store := NewMemStorage()
	metric := NewLabels("__name__", "m")
	// Distinct values so we can trace exactly which timestamp each sample came
	// from: value equals the timestamp in seconds.
	for ts := int64(0); ts < 2_000_000; ts += 1_000 {
		store.AddPoint(metric, ts, float64(ts/1000))
	}
	eng := NewEngine(store)

	const outerAt int64 = 1_000_000
	// Inner selector pins its own read time with a negative offset; the outer
	// subquery pins the grid. At every grid point the inner @ dominates the
	// grid timestamp, then the inner offset shifts the read.
	inner := &VectorSelector{
		Name: "m",
		timeModifier: timeModifier{
			HasAt:  true,
			AtMs:   500_000,
			Offset: -10 * time.Second, // effective read = 510s at every grid point
		},
	}
	outer := &SubqueryExpr{
		Inner:        inner,
		Range:        30 * time.Second,
		Step:         10 * time.Second,
		timeModifier: timeModifier{HasAt: true, AtMs: outerAt},
	}
	mat := evalMatrix(t, eng, outer, 999)

	// Outer grid: window (970s,1000s] -> 980,990,1000.
	wantTimes := []int64{980_000, 990_000, 1_000_000}
	if got := pointTimes(mat.Series[0]); !reflect.DeepEqual(got, wantTimes) {
		t.Fatalf("nested outer grid = %v, want %v", got, wantTimes)
	}
	// Every point carries the inner pinned read value of 510 (510s timestamp).
	for _, p := range mat.Series[0].Points {
		if p.V != 510 {
			t.Fatalf("inner modifier not honored at %d: value %v, want 510", p.T, p.V)
		}
	}
}

// TestRateOverSubquery: rate() accepts a subquery matrix and extrapolates
// against the subquery window bounds.
func TestRateOverSubquery(t *testing.T) {
	store := NewMemStorage()
	metric := NewLabels("__name__", "c")
	// Counter rising by 10 every 10s over the grid range.
	for ts, v := int64(0), 0.0; ts <= 100_000; ts, v = ts+10_000, v+10 {
		store.AddPoint(metric, ts, v)
	}
	eng := NewEngine(store)
	expr := &Call{Func: Rate, Args: []Expr{
		&SubqueryExpr{
			Inner: &VectorSelector{Name: "c"},
			Range: 50 * time.Second,
			Step:  10 * time.Second,
		},
	}}
	val, err := eng.Eval(expr, 60_000)
	if err != nil {
		t.Fatal(err)
	}
	vec := val.([]Sample)
	if len(vec) != 1 {
		t.Fatalf("expected 1 rate series, got %d", len(vec))
	}
	// Grid (10s,60s] step 10 -> 20..60: values 20..60, slope 1/s. The rate
	// must be finite and positive regardless of extrapolation details.
	if vec[0].V <= 0 {
		t.Fatalf("rate over subquery = %v, expected positive", vec[0].V)
	}
}
