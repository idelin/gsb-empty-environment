package promlite

import (
	"math"
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)

func at(min int) time.Time { return t0.Add(time.Duration(min) * time.Minute) }

// counterStore builds metric "c" with one series per job, sampled once per
// minute starting at t0: job a = 100+10*m, job b = 200+20*m.
func counterStore() *Store {
	s := NewStore()
	for m := 0; m <= 10; m++ {
		s.Add(Sample{Metric: "c", Labels: Labels{"job": "a"}, T: at(m), V: float64(100 + 10*m)})
		s.Add(Sample{Metric: "c", Labels: Labels{"job": "b"}, T: at(m), V: float64(200 + 20*m)})
	}
	return s
}

func evalQuery(t *testing.T, s *Store, q string, ts time.Time) (Value, *Trace) {
	t.Helper()
	e, err := Parse(q)
	if err != nil {
		t.Fatalf("parse %q: %v", q, err)
	}
	v, tr, err := NewEvaluator(s).Eval(e, ts)
	if err != nil {
		t.Fatalf("eval %q: %v", q, err)
	}
	return v, tr
}

func vecMap(v Value) map[string]float64 {
	out := map[string]float64{}
	for _, el := range v.Vector {
		out[el.Labels.Key()] = el.V
	}
	return out
}

func TestSelectorInstant(t *testing.T) {
	s := counterStore()
	v, tr := evalQuery(t, s, `c{job="a"}`, at(7))
	got := vecMap(v)
	if len(got) != 1 || got[`{job="a"}`] != 170 {
		t.Fatalf("got %v", got)
	}
	if !tr.Range.Set || !tr.Range.Start.Equal(at(7)) || !tr.Range.End.Equal(at(7)) {
		t.Fatalf("trace range = %v, want exactly [t7, t7]", tr.Range)
	}
}

func TestRateWindowAndRange(t *testing.T) {
	s := counterStore()
	v, tr := evalQuery(t, s, `rate(c[5m])`, at(10))
	got := vecMap(v)
	// (t-5m, t] = minutes 6..10 -> delta 50 over 300s for job a.
	if math.Abs(got[`{job="a"}`]-50.0/300) > 1e-9 {
		t.Fatalf("job a rate = %v, want %v", got[`{job="a"}`], 50.0/300)
	}
	if math.Abs(got[`{job="b"}`]-100.0/300) > 1e-9 {
		t.Fatalf("job b rate = %v", got[`{job="b"}`])
	}
	if !tr.Range.Start.Equal(at(6)) || !tr.Range.End.Equal(at(10)) {
		t.Fatalf("rate consumed range = %v, want [t6, t10]", tr.Range)
	}
}

func TestRateCounterReset(t *testing.T) {
	s := NewStore()
	s.Add(
		Sample{Metric: "c", Labels: Labels{"i": "x"}, T: at(0), V: 100},
		Sample{Metric: "c", Labels: Labels{"i": "x"}, T: at(1), V: 130},
		Sample{Metric: "c", Labels: Labels{"i": "x"}, T: at(2), V: 5},
		Sample{Metric: "c", Labels: Labels{"i": "x"}, T: at(3), V: 25},
	)
	v, _ := evalQuery(t, s, `rate(c[10m])`, at(3))
	// increase = (130-100) + 5 (reset to 0, then 0->5) + (25-5) = 55 over 180s
	got := vecMap(v)[`{i="x"}`]
	if math.Abs(got-55.0/180) > 1e-9 {
		t.Fatalf("rate with reset = %v, want %v", got, 55.0/180)
	}
}

func TestRateNeedsTwoSamples(t *testing.T) {
	s := NewStore()
	s.Add(Sample{Metric: "c", Labels: Labels{"i": "x"}, T: at(0), V: 5})
	v, _ := evalQuery(t, s, `rate(c[10m])`, at(0))
	if len(v.Vector) != 0 {
		t.Fatalf("single sample must not fabricate a rate, got %v", v.Vector)
	}
}

func TestOffsetReadsHistoricalWindow(t *testing.T) {
	s := counterStore()
	// Same query, different offsets -> different historical windows.
	now, trNow := evalQuery(t, s, `c{job="a"}`, at(10))
	back, trBack := evalQuery(t, s, `c{job="a"} offset 4m`, at(10))
	if vecMap(now)[`{job="a"}`] != 200 {
		t.Fatalf("no-offset value = %v", vecMap(now))
	}
	if vecMap(back)[`{job="a"}`] != 160 {
		t.Fatalf("offset 4m value = %v, want 160 (sample at t6)", vecMap(back))
	}
	if !trNow.Range.End.Equal(at(10)) {
		t.Fatalf("no-offset range end = %v", trNow.Range.End)
	}
	if !trBack.Range.End.Equal(at(6)) {
		t.Fatalf("offset range end = %v, want t6", trBack.Range.End)
	}

	// Offset shifts the whole rate window into the past.
	v, tr := evalQuery(t, s, `rate(c{job="a"}[5m]) offset 3m`, at(10))
	// window = (t10-3m-5m, t10-3m] = (t2, t7] -> minutes 3..7, delta 40/240s
	got := vecMap(v)[`{job="a"}`]
	if math.Abs(got-40.0/240) > 1e-9 {
		t.Fatalf("offset rate = %v, want %v", got, 40.0/240)
	}
	if !tr.Range.Start.Equal(at(3)) || !tr.Range.End.Equal(at(7)) {
		t.Fatalf("offset rate range = %v, want [t3, t7]", tr.Range)
	}
}

func TestSumBy(t *testing.T) {
	s := counterStore()
	v, tr := evalQuery(t, s, `sum by (job) (c)`, at(5))
	got := vecMap(v)
	if len(got) != 2 || got[`{job="a"}`] != 150 || got[`{job="b"}`] != 300 {
		t.Fatalf("sum by = %v", got)
	}
	if !tr.Range.Start.Equal(at(5)) || !tr.Range.End.Equal(at(5)) {
		t.Fatalf("sum by range = %v, want [t5, t5]", tr.Range)
	}
}

func TestBinaryMissingSeriesNeverZeroDivisor(t *testing.T) {
	s := NewStore()
	s.Add(Sample{Metric: "a", Labels: Labels{"x": "1"}, T: at(0), V: 10})
	s.Add(Sample{Metric: "b", Labels: Labels{"x": "2"}, T: at(0), V: 5})
	v, _ := evalQuery(t, s, `a / b`, at(0))
	if len(v.Vector) != 0 {
		t.Fatalf("unmatched series must be dropped, not divided by 0: %v", v.Vector)
	}
}

func TestBinaryRealZeroDivisorIsInf(t *testing.T) {
	s := NewStore()
	s.Add(Sample{Metric: "a", Labels: Labels{"x": "1"}, T: at(0), V: 10})
	s.Add(Sample{Metric: "b", Labels: Labels{"x": "1"}, T: at(0), V: 0})
	v, _ := evalQuery(t, s, `a / b`, at(0))
	got := vecMap(v)[`{x="1"}`]
	if !math.IsInf(got, 1) {
		t.Fatalf("real zero divisor should yield +Inf, got %v", got)
	}
}

func TestBinaryMatchingOn(t *testing.T) {
	s := NewStore()
	s.Add(Sample{Metric: "a", Labels: Labels{"job": "x", "i": "1"}, T: at(0), V: 10})
	s.Add(Sample{Metric: "a", Labels: Labels{"job": "x", "i": "2"}, T: at(0), V: 20})
	s.Add(Sample{Metric: "b", Labels: Labels{"job": "x"}, T: at(0), V: 5})
	// on(job) with duplicate {job="x"} on the left -> many-to-many, rejected.
	e, _ := Parse(`a / on(job) b`)
	if _, _, err := NewEvaluator(s).Eval(e, at(0)); err == nil ||
		!strings.Contains(err.Error(), "many-to-many") {
		t.Fatalf("expected many-to-many error, got %v", err)
	}
	// After aggregation the match is one-to-one and succeeds.
	v, _ := evalQuery(t, s, `sum by (job) (a) / on(job) b`, at(0))
	if got := vecMap(v)[`{job="x"}`]; got != 6 {
		t.Fatalf("(10+20)/5 = %v, want 6", got)
	}
}

func TestManyToManyRejectedNotCartesian(t *testing.T) {
	s := NewStore()
	s.Add(Sample{Metric: "a", Labels: Labels{"g": "1", "i": "p"}, T: at(0), V: 1})
	s.Add(Sample{Metric: "a", Labels: Labels{"g": "1", "i": "q"}, T: at(0), V: 2})
	s.Add(Sample{Metric: "b", Labels: Labels{"g": "1", "j": "r"}, T: at(0), V: 3})
	s.Add(Sample{Metric: "b", Labels: Labels{"g": "1", "j": "s"}, T: at(0), V: 4})
	e, err := Parse(`a / on(g) b`)
	if err != nil {
		t.Fatal(err)
	}
	_, _, evalErr := NewEvaluator(s).Eval(e, at(0))
	if evalErr == nil {
		t.Fatal("2x2 match must be rejected, not expanded into 4 results")
	}
	if !strings.Contains(evalErr.Error(), "many-to-many") {
		t.Fatalf("error should name many-to-many: %v", evalErr)
	}
}

func TestBinaryDefaultMatchesAllLabels(t *testing.T) {
	s := NewStore()
	s.Add(Sample{Metric: "a", Labels: Labels{"x": "1"}, T: at(0), V: 10})
	s.Add(Sample{Metric: "b", Labels: Labels{"x": "1", "y": "2"}, T: at(0), V: 2})
	v, _ := evalQuery(t, s, `a * b`, at(0))
	if len(v.Vector) != 0 {
		t.Fatalf("default matching is on all labels; got %v", v.Vector)
	}
	v, _ = evalQuery(t, s, `a * ignoring(y) b`, at(0))
	if got := vecMap(v)[`{x="1"}`]; got != 20 {
		t.Fatalf("ignoring(y) match = %v, want 20", got)
	}
}

func TestScalarOps(t *testing.T) {
	s := counterStore()
	v, _ := evalQuery(t, s, `c{job="a"} * 2 + 1`, at(1))
	if got := vecMap(v)[`{job="a"}`]; got != 221 {
		t.Fatalf("scalar mix = %v, want 221", got)
	}
	v, _ = evalQuery(t, s, `10 / 4`, at(0))
	if !v.IsScalar || v.Scalar != 2.5 {
		t.Fatalf("scalar/scalar = %v", v)
	}
}

func TestTraceRangesPerStep(t *testing.T) {
	s := counterStore()
	s.Add(Sample{Metric: "cores", Labels: Labels{"job": "a"}, T: at(10), V: 4})
	s.Add(Sample{Metric: "cores", Labels: Labels{"job": "b"}, T: at(10), V: 2})
	_, tr := evalQuery(t, s, `sum by (job) (rate(c[4m])) / on(job) cores`, at(10))
	// Root must span the rate window (t7..t10) and the cores sample (t10).
	if !tr.Range.Start.Equal(at(7)) || !tr.Range.End.Equal(at(10)) {
		t.Fatalf("root range = %v, want [t7, t10]", tr.Range)
	}
	if len(tr.Kids) != 2 {
		t.Fatalf("binary trace must have 2 children, got %d", len(tr.Kids))
	}
	sumTr, coresTr := tr.Kids[0], tr.Kids[1]
	if !sumTr.Range.Start.Equal(at(7)) || !sumTr.Range.End.Equal(at(10)) {
		t.Fatalf("sum-by step range = %v, want [t7, t10]", sumTr.Range)
	}
	if !coresTr.Range.Start.Equal(at(10)) || !coresTr.Range.End.Equal(at(10)) {
		t.Fatalf("cores step range = %v, want [t10, t10]", coresTr.Range)
	}
	if sumTr.Count != 2 || coresTr.Count != 2 || tr.Count != 2 {
		t.Fatalf("counts: sum=%d cores=%d root=%d", sumTr.Count, coresTr.Count, tr.Count)
	}
}

func TestFixtureRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/f.json"
	src := Synth(4, time.Minute)
	if err := SaveFixture(path, src); err != nil {
		t.Fatal(err)
	}
	back, err := LoadFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(back.Samples()) != len(src.Samples()) {
		t.Fatalf("replayed %d events, want %d", len(back.Samples()), len(src.Samples()))
	}
	// Replay yields identical evaluation results.
	q := `sum by (job) (rate(http_requests_total[2m]))`
	v1, _ := evalQuery(t, src, q, SynthBase.Add(4*time.Minute))
	v2, _ := evalQuery(t, back, q, SynthBase.Add(4*time.Minute))
	got1, got2 := vecMap(v1), vecMap(v2)
	if len(got1) != len(got2) {
		t.Fatalf("replay mismatch: %v vs %v", got1, got2)
	}
	for k, val := range got1 {
		if got2[k] != val {
			t.Fatalf("replay mismatch at %s: %v vs %v", k, val, got2[k])
		}
	}
}
