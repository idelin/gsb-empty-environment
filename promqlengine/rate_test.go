package promqlengine

import (
	"math"
	"testing"
	"time"
)

const ms = int64(time.Millisecond)

func approxEqual(a, b float64) bool {
	if math.IsNaN(b) {
		return math.IsNaN(a)
	}
	return math.Abs(a-b) < 1e-9
}

func rateOf(t *testing.T, eng *Engine, fn *Function, rng time.Duration, at int64) float64 {
	t.Helper()
	expr := &Call{Func: fn, Args: []Expr{
		&RangeSelector{Vector: &VectorSelector{Name: "c"}, Range: rng},
	}}
	val, err := eng.Eval(expr, at)
	if err != nil {
		t.Fatalf("eval error: %v", err)
	}
	vec := val.([]Sample)
	if len(vec) != 1 {
		t.Fatalf("expected exactly one result series, got %d", len(vec))
	}
	return vec[0].V
}

// TestExtrapolationNoClamp: samples fill the window edge-to-edge so both edge
// gaps are below half the average interval. No 1.1x clamp triggers and the
// endpoint contributions use the raw endpoint values.
func TestExtrapolationNoClamp(t *testing.T) {
	store := NewMemStorage()
	store.AddSeries(NewLabels("__name__", "c"),
		Point{T: 1_000, V: 0},
		Point{T: 11_000, V: 10},
		Point{T: 21_000, V: 20},
		Point{T: 31_000, V: 40},
	)
	eng := NewEngine(store)
	// Window (0, 31s]: avg interval 10s; left gap 1s (< 5s) adds first.V=0,
	// right gap 0s (< 5s) adds last.V=40; delta is 40. First value 0 keeps the
	// counter zero clamp out of the picture.
	//   result = 40 + 0 + 40 = 80
	//   observed = 30s span + 1s + 0s = 31s; window = 31s -> factor 1.
	gotInc := rateOf(t, eng, Increase, 31*time.Second, 31_000)
	if !approxEqual(gotInc, 80) {
		t.Fatalf("no-clamp increase = %v, want 80", gotInc)
	}
	gotRate := rateOf(t, eng, Rate, 31*time.Second, 31_000)
	if !approxEqual(gotRate, 80.0/31.0) {
		t.Fatalf("no-clamp rate = %v, want %v", gotRate, 80.0/31.0)
	}
}

// TestExtrapolationBothEdgesClamped: the first and last samples sit far from
// both window edges (> 1.1*avg interval), so both edge durations are clamped
// to half an average interval.
func TestExtrapolationBothEdgesClamped(t *testing.T) {
	const end int64 = 120_000
	metric := NewLabels("__name__", "c")
	store := NewMemStorage()
	store.AddSeries(metric,
		Point{T: end - 40_000, V: 0},
		Point{T: end - 30_000, V: 10},
		Point{T: end - 20_000, V: 20},
	)
	// Window (40s, 120s]: edge gaps are 40s each, avg interval is 10s.
	eng := NewEngine(store)

	// Hand calculation:
	//   delta = 20; endpoint contributions = 10 + 10 = 20 -> 40
	//   observed = 20s + 5s + 5s = 30s; window = 80s
	//   increase = 40 * 80/30 = 320/3
	wantIncrease := 40.0 * 80.0 / 30.0
	if got := rateOf(t, eng, Increase, 80*time.Second, end); !approxEqual(got, wantIncrease) {
		t.Fatalf("clamped increase = %v, want %v", got, wantIncrease)
	}
	if got := rateOf(t, eng, Rate, 80*time.Second, end); !approxEqual(got, wantIncrease/80) {
		t.Fatalf("clamped rate = %v, want %v", got, wantIncrease/80)
	}
}

// TestMultipleCounterResets: two resets inside one window. Each reset adds
// the post-reset value rather than the negative difference.
func TestMultipleCounterResets(t *testing.T) {
	store2 := NewMemStorage()
	metric2 := NewLabels("__name__", "c")
	store2.AddSeries(metric2,
		Point{T: 1_000, V: 0},
		Point{T: 10_000, V: 20},
		Point{T: 20_000, V: 5},
		Point{T: 30_000, V: 15},
		Point{T: 40_000, V: 2},
		Point{T: 49_000, V: 8},
	)
	eng2 := NewEngine(store2)
	// Window (0, 49s]: left gap 1s, right gap 0s, avg interval 9.6s. Both
	// gaps stay below half the average, so no 1.1x clamp and the scale factor
	// is exactly 1. First value 0 disables the counter zero clamp so the
	// assertion isolates reset detection.
	got2 := rateOf(t, eng2, Increase, 49*time.Second, 49_000)
	// Corrected delta = 20 + 5 + (15-5) + 2 + (8-2) = 43.
	// Endpoint terms add first.V (0) and last.V (8): total 51.
	if !approxEqual(got2, 51) {
		t.Fatalf("reset-aware increase = %v, want 51", got2)
	}
	gotRate := rateOf(t, eng2, Rate, 49*time.Second, 49_000)
	if !approxEqual(gotRate, 51.0/49.0) {
		t.Fatalf("reset-aware rate = %v, want %v", gotRate, 51.0/49.0)
	}
}

// TestZeroClamp: a monotonic counter whose linear back-extrapolation crosses
// zero before the window start must have its left duration cut to the
// zero-crossing instant. This contrasts with the unclamped result.
func TestZeroClamp(t *testing.T) {
	const end int64 = 120_000
	metric := NewLabels("__name__", "c")
	// Slope 10/s, first sample 10 at t=110s; zero crossing is at t=109s.
	store := NewMemStorage()
	store.AddSeries(metric,
		Point{T: end - 10_000, V: 10},
		Point{T: end, V: 20},
	)
	// Window (60s, 120s]: rangeStart=60s, avg interval 10s.
	rangeStart := end - 60_000
	samples := []Point{{T: end - 10_000, V: 10}, {T: end, V: 20}}

	got := rateOf(t, NewEngine(store), Increase, time.Minute, end)

	// Counter path (zero clamp active), step by step:
	//   delta = 10; durationToStart = 50s;
	//   durationToZero = 50 - (10*10/10) = 40s -> clamp left to 40s;
	//   left gap 40s >= 5s adds avg(10,20)=15; right gap 0s < 5s adds
	//   last.V=20, so resultFloat = 10+15+20 = 45;
	//   1.1*10 = 11s threshold: 40s > 11 -> clamp left to 5s.
	//   observed = 10s span + 5s + 0s = 15s; increase = 45 * 60/15 = 180.
	wantCounter, ok := extrapolatedRate(samples, rangeStart, end, true)
	if !ok {
		t.Fatal("counter extrapolation should produce a value")
	}
	if !approxEqual(wantCounter, 180) {
		t.Fatalf("zero-clamped increase = %v, want 180", wantCounter)
	}
	if !approxEqual(got, wantCounter) {
		t.Fatalf("engine increase = %v, want %v", got, wantCounter)
	}

	// Non-counter comparison over the same window: no zero clamp, so the left
	// gap stays 50s before the identical 1.1x threshold clamps it to 5s. The
	// two converge here; the next case separates them.
	nonCounter, ok := extrapolatedRate(samples, rangeStart, end, false)
	if !ok {
		t.Fatal("non-counter extrapolation should produce a value")
	}
	if !approxEqual(nonCounter, 180) {
		t.Fatalf("non-counter increase = %v, want 180 in this symmetric case", nonCounter)
	}

	// Distinguishing case: edge gap below the 1.1 threshold so no threshold
	// clamp, but the counter zero clamp still moves the left duration.
	// Window (105s,120s]: start gap 5s (< 11s).
	rs2 := end - 15_000
	counter2, _ := extrapolatedRate(samples, rs2, end, true)
	plain2, _ := extrapolatedRate(samples, rs2, end, false)
	if counter2 == plain2 {
		t.Fatalf("zero clamp must change the result: counter=%v plain=%v", counter2, plain2)
	}
	// Counter: zero clamp drives the left gap to 5-(10*10/10) = -5s.
	//   the clamped -5s is below half the interval so the left term adds
	//   first.V=10; right adds last.V=20; resultFloat = 10+10+20 = 40;
	//   observed = 10 - 5 = 5; increase = 40 * 15/5 = 120.
	if !approxEqual(counter2, 120) {
		t.Fatalf("counter2 = %v, want 120", counter2)
	}
	// Plain: left gap stays +5s, observed = 15, increase = 45.
	if !approxEqual(plain2, 45) {
		t.Fatalf("plain2 = %v, want 45", plain2)
	}
}

func TestFewerThanTwoSamplesSkipsSeries(t *testing.T) {
	const end int64 = 60_000
	metric := NewLabels("__name__", "c")

	oneSample := NewMemStorage()
	oneSample.AddPoint(metric, end-1_000, 42)
	eng := NewEngine(oneSample)
	for _, fn := range []*Function{Rate, Increase} {
		val, err := eng.Eval(&Call{Func: fn, Args: []Expr{
			&RangeSelector{Vector: &VectorSelector{Name: "c"}, Range: time.Minute},
		}}, end)
		if err != nil {
			t.Fatal(err)
		}
		if len(val.([]Sample)) != 0 {
			t.Fatalf("%s on a single sample must skip the series, got %+v", fn.Name, val)
		}
	}

	// One live sample plus a stale marker: the marker truncates, leaving one.
	staleThenLive := NewMemStorage()
	staleThenLive.AddSeries(metric,
		Point{T: end - 20_000, V: 1},
		Point{T: end - 10_000, V: StaleNaN()},
	)
	eng2 := NewEngine(staleThenLive)
	val, err := eng2.Eval(&Call{Func: Rate, Args: []Expr{
		&RangeSelector{Vector: &VectorSelector{Name: "c"}, Range: time.Minute},
	}}, end)
	if err != nil {
		t.Fatal(err)
	}
	if len(val.([]Sample)) != 0 {
		t.Fatalf("rate must skip series with <2 non-stale samples, got %+v", val)
	}
}

// TestRegularNaNInRate: an arithmetic NaN is not a stale marker; it survives
// into the window and propagates through the rate arithmetic.
func TestRegularNaNInRate(t *testing.T) {
	const end int64 = 60_000
	metric := NewLabels("__name__", "c")
	store := NewMemStorage()
	store.AddSeries(metric,
		Point{T: end - 20_000, V: 1},
		Point{T: end - 10_000, V: math.NaN()},
		Point{T: end, V: 3},
	)
	eng := NewEngine(store)
	got := rateOf(t, eng, Increase, time.Minute, end)
	if !math.IsNaN(got) || IsStaleNaN(got) {
		t.Fatalf("expected a propagated regular NaN, got %v", got)
	}
}
