package promqlengine

import (
	"math"
	"testing"
	"time"
)

func TestParseDuration(t *testing.T) {
	cases := []struct {
		in   string
		want time.Duration
	}{
		{"5m", 5 * time.Minute},
		{"1h30m", 90 * time.Minute},
		{"250ms", 250 * time.Millisecond},
		{"1d", 24 * time.Hour},
		{"-5m", -5 * time.Minute},
		{"1w2d", 9 * 24 * time.Hour},
	}
	for _, c := range cases {
		got, err := ParseDuration(c.in)
		if err != nil {
			t.Fatalf("ParseDuration(%q) error: %v", c.in, err)
		}
		if got != c.want {
			t.Errorf("ParseDuration(%q) = %v, want %v", c.in, got, c.want)
		}
	}
	if _, err := ParseDuration(""); err == nil {
		t.Fatal("empty duration must error")
	}
	if _, err := ParseDuration("5q"); err == nil {
		t.Fatal("unknown unit must error")
	}
}

func TestParseBasicSelectors(t *testing.T) {
	sel := MustParseExpr(`up{job="api",code!="200"} offset 5m`).(*VectorSelector)
	if sel.Name != "up" || len(sel.Matchers) != 2 {
		t.Fatalf("bad selector: %+v", sel)
	}
	if sel.Offset != 5*time.Minute {
		t.Fatalf("offset = %v, want 5m", sel.Offset)
	}
	if sel.Matchers[0].Op != MatchEqual || sel.Matchers[1].Op != MatchNotEqual {
		t.Fatalf("matcher ops wrong: %+v", sel.Matchers)
	}
}

func TestParseNegativeOffsetAndAt(t *testing.T) {
	expr := MustParseExpr(`metric offset -5m`).(*VectorSelector)
	if expr.Offset != -5*time.Minute {
		t.Fatalf("negative offset = %v", expr.Offset)
	}

	at := MustParseExpr(`metric @ 1700000000 offset 2m`).(*VectorSelector)
	if !at.HasAt || at.AtMs != 1_700_000_000_000 {
		t.Fatalf("@ not parsed: %+v", at)
	}
	if at.Offset != 2*time.Minute {
		t.Fatalf("offset alongside @ = %v", at.Offset)
	}
}

func TestParseRangeAndSubquery(t *testing.T) {
	rs := MustParseExpr(`metric offset 1m[5m]`).(*RangeSelector)
	if rs.Range != 5*time.Minute {
		t.Fatalf("range = %v", rs.Range)
	}
	// Modifiers live on the inner vector selector.
	if rs.Vector.Offset != time.Minute {
		t.Fatalf("inner offset = %v", rs.Vector.Offset)
	}

	sq := MustParseExpr(`rate(metric[1m])[5m:30s] @ 1700000000 offset 2m`).(*SubqueryExpr)
	if sq.Range != 5*time.Minute || sq.Step != 30*time.Second {
		t.Fatalf("subquery range/step = %v/%v", sq.Range, sq.Step)
	}
	if !sq.HasAt || sq.Offset != 2*time.Minute {
		t.Fatalf("subquery modifiers missing: %+v", sq.timeModifier)
	}
	call := sq.Inner.(*Call)
	if call.Func.Name != "rate" {
		t.Fatalf("inner call = %v", call.Func.Name)
	}
}

func TestParseErrors(t *testing.T) {
	bad := []string{
		``,
		`metric{`,
		`metric[`,
		`metric[5m:]`,
		`unknownfn(metric[1m])`,
		`metric @ notanumber`,
		`metric{job=}`,
	}
	for _, q := range bad {
		if _, err := ParseExpr(q); err == nil {
			t.Errorf("expected parse error for %q", q)
		}
	}
}

// TestEndToEndParsedQuery drives a full text query through the engine and
// checks the semantic result, exercising parser + selector + rate together.
func TestEndToEndParsedQuery(t *testing.T) {
	store := NewMemStorage()
	metric := NewLabels("__name__", "http_requests_total")
	store.AddSeries(metric,
		Point{T: 10_000, V: 0},
		Point{T: 20_000, V: 10},
		Point{T: 30_000, V: 20},
		Point{T: 40_000, V: 5}, // counter reset
		Point{T: 50_000, V: 15},
		Point{T: 60_000, V: StaleNaN()},
	)
	eng := NewEngine(store)

	// rate over a window ending just before the stale marker.
	expr := MustParseExpr(`rate(http_requests_total[50s])`)
	val, err := eng.Eval(expr, 50_000)
	if err != nil {
		t.Fatal(err)
	}
	vec := val.([]Sample)
	if len(vec) != 1 || math.IsNaN(vec[0].V) || vec[0].V <= 0 {
		t.Fatalf("parsed rate query gave %+v", vec)
	}

	// After the stale marker the instant selector drops the series.
	inst := MustParseExpr(`http_requests_total`)
	if v := mustEvalVector(t, eng, inst, 60_000); len(v) != 0 {
		t.Fatalf("stale series must be absent, got %+v", v)
	}
}

// TestParsedSubqueryGrid confirms a parsed subquery lands on the same aligned
// grid as the hand-built AST.
func TestParsedSubqueryGrid(t *testing.T) {
	store := NewMemStorage()
	metric := NewLabels("__name__", "m")
	for ts := int64(0); ts <= 100_000; ts += 1_000 {
		store.AddPoint(metric, ts, 1)
	}
	eng := NewEngine(store)
	expr := MustParseExpr(`m[30s:10s]`)
	mat := evalMatrix(t, eng, expr, 55_000)
	want := []int64{30_000, 40_000, 50_000}
	if got := pointTimes(mat.Series[0]); !equalInt64(got, want) {
		t.Fatalf("parsed subquery grid = %v, want %v", got, want)
	}
}

func equalInt64(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
