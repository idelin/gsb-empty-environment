package audioalign

import (
	"errors"
	"math"
	"testing"
)

func TestAmbiguousRepeatedBeats(t *testing.T) {
	const (
		n = 8192
		p = 256
	)
	ref := make([]float64, n)
	for i := 0; i < n; i += p {
		ref[i] = 1
	}
	delayed := make([]float64, n+1024)
	copy(delayed, ref)
	// Extend the periodic pattern into the padded tail so the final
	// windows can still evaluate the period-shifted lag 256.
	for i := n; i < len(delayed); i += p {
		delayed[i] = 1
	}

	base := Config{
		WindowSize: 512, HopSize: 256,
		MinLag: 0, MaxLag: 320,
		SmoothWeight: 0, SilenceRMS: 1e-12,
		CorrEpsilon: 1e-9, CostEpsilon: 1e-9,
	}

	// Tight drift limit: only constant-lag curves survive. Lags 0 and 256
	// both correlate perfectly, so two globally optimal paths are required.
	tight := base
	tight.MaxDrift = 0
	tight.MaxPaths = 16
	res, err := Align(ref, delayed, tight)
	if err != nil {
		t.Fatalf("Align failed: %v", err)
	}
	if len(res.Paths) < 2 {
		t.Fatalf("expected multiple optimal paths, got %d", len(res.Paths))
	}
	if res.Truncated {
		t.Fatal("did not expect truncation with only constant curves")
	}
	firstCost := res.Paths[0].Cost
	seen := map[string]bool{}
	for _, w := range res.Windows {
		if !w.Ambiguous || w.Confidence > base.CorrEpsilon {
			t.Fatalf("window %d should be ambiguous, confidence %g", w.Index, w.Confidence)
		}
	}
	for _, path := range res.Paths {
		if math.Abs(path.Cost-firstCost) > 1e-9 {
			t.Fatalf("unequal optimal costs: %g vs %g", path.Cost, firstCost)
		}
		for j := 1; j < len(path.Lags); j++ {
			if path.Lags[j] != path.Lags[j-1] {
				t.Fatal("tight drift limit allowed a non-constant curve")
			}
		}
		seen[lagKey(rationalLags(path.Lags))] = true
	}
	zeroPath := make([]int, len(res.Windows))
	shiftPath := make([]int, len(res.Windows))
	for i := range shiftPath {
		shiftPath[i] = p
	}
	if !seen[lagKey(zeroPath)] {
		t.Fatalf("zero-lag constant path missing: %v", seen)
	}
	if !seen[lagKey(shiftPath)] {
		t.Fatalf("period-shift constant path missing: %v", seen)
	}

	// Construct a small, fully ambiguous DP directly: two tied lags per
	// window, zero smoothing and a permissive drift limit give 2^w equal
	// paths, which MaxPaths must truncate deterministically.
	w := 6
	lagSets := make([][]int, w)
	costSets := make([][]float64, w)
	centers := make([]int, w)
	for j := range lagSets {
		lagSets[j] = []int{0, 256}
		costSets[j] = []float64{0, 0}
		centers[j] = 256 * (j + 1)
	}
	looseCfg := Config{
		HopSize: 256, MaxDrift: 400, SmoothWeight: 0,
		MaxPaths: 8, CostEpsilon: 1e-9,
	}
	paths2, truncated2, err := solveDP(lagSets, costSets, centers, looseCfg)
	if err != nil {
		t.Fatalf("solveDP failed: %v", err)
	}
	if len(paths2) != 8 || !truncated2 {
		t.Fatalf("expected 8 truncated paths, got %d (truncated=%v)", len(paths2), truncated2)
	}
}

func TestSilenceAndErrorCases(t *testing.T) {
	if _, err := Align(nil, []float64{1}, Config{}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("empty input: want ErrInvalidConfig, got %v", err)
	}
	if _, err := Align([]float64{1}, []float64{1}, Config{MinLag: 10, MaxLag: 1}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("bad lag range: want ErrInvalidConfig, got %v", err)
	}

	silent := make([]float64, 4096)
	if _, err := Align(silent, silent, Config{WindowSize: 512, HopSize: 256}); !errors.Is(err, ErrNoActiveWindows) {
		t.Fatalf("all-silent input: want ErrNoActiveWindows, got %v", err)
	}

	// Silence present in only one track still disqualifies the window.
	sc := synthConfig{
		n: 6000, driftStart: 20, driftSlope: 0.002,
		noiseAmp: 0.5, silencePeriod: 2048, silenceGap: 600,
		silenceBoth: false, seed: 11,
	}
	ref, delayed := synthPCM(sc)
	cfg := Config{
		WindowSize: 512, HopSize: 256, MinLag: 0, MaxLag: 48,
		SilenceRMS: 1e-6,
	}
	res, err := Align(ref, delayed, cfg)
	if err != nil {
		t.Fatalf("Align failed: %v", err)
	}
	if len(res.Skipped) == 0 {
		t.Fatal("reference-only silence must skip windows")
	}
	totalWindows := (len(ref)-cfg.WindowSize)/cfg.HopSize + 1
	if len(res.Windows)+len(res.Skipped) != totalWindows {
		t.Fatalf("window coverage: active %d + skipped %d != %d",
			len(res.Windows), len(res.Skipped), totalWindows)
	}

	// No overlapping samples at any lag means no active windows.
	if _, err := Align(ref, delayed, Config{WindowSize: 512, HopSize: 256, MinLag: 9000, MaxLag: 9100}); !errors.Is(err, ErrNoActiveWindows) {
		t.Fatalf("non-overlapping lag range: want ErrNoActiveWindows, got %v", err)
	}
}

func TestSubsampleRational(t *testing.T) {
	sc := synthConfig{
		n: 8192, driftStart: 40.5, driftSlope: 0.0005,
		noiseAmp: 0.6, beatPeriod: 211, beatAmp: 0.7,
		subsample: true, seed: 5,
	}
	ref, delayed := synthPCM(sc)

	integer, err := Align(ref, delayed, Config{
		WindowSize: 512, HopSize: 128, MinLag: 24, MaxLag: 64,
	})
	if err != nil {
		t.Fatalf("integer Align failed: %v", err)
	}
	for _, p := range integer.Paths {
		for _, r := range p.Lags {
			if r.Den != 1 {
				t.Fatalf("refinement disabled but got fractional offset %s", r)
			}
		}
	}

	res, err := Align(ref, delayed, Config{
		WindowSize: 512, HopSize: 128, MinLag: 24, MaxLag: 64,
		RefineSubsample: true, SubsampleQ: 16,
	})
	if err != nil {
		t.Fatalf("subsample Align failed: %v", err)
	}
	sawFractional := false
	for j, w := range res.Windows {
		center := w.Center.Float()
		want := sc.driftStart + sc.driftSlope*center
		for _, p := range res.Paths {
			r := p.Lags[j]
			// Reduced fractions must represent multiples of 1/16.
			if (r.Num*16)%r.Den != 0 {
				t.Fatalf("offset %s is not a quantized 1/16-sample value", r)
			}
			got := r.Float()
			if math.Abs(got-want) > 0.25 {
				t.Fatalf("window %d refined lag %s, want ~%.3f", j, r, want)
			}
			if r.Den != 1 {
				sawFractional = true
			}
		}
	}
	if !sawFractional {
		t.Fatal("expected at least one genuinely fractional offset")
	}
}

func TestDriftVelocityLimit(t *testing.T) {
	// A constant offset means the true lag never changes between windows.
	sc := synthConfig{n: 6000, driftStart: 30, driftSlope: 0, noiseAmp: 0.6, seed: 13}
	ref, delayed := synthPCM(sc)

	res, err := Align(ref, delayed, Config{
		WindowSize: 512, HopSize: 256, MinLag: 0, MaxLag: 60, MaxDrift: 1,
	})
	if err != nil {
		t.Fatalf("Align failed: %v", err)
	}
	for _, p := range res.Paths {
		for j := 1; j < len(p.Lags); j++ {
			delta := math.Abs(p.Lags[j].Float() - p.Lags[j-1].Float())
			if delta > 1+1e-12 {
				t.Fatalf("transition %d violates MaxDrift: %g", j, delta)
			}
		}
	}

	// MaxDrift zero forces a constant curve; it must still find the offset.
	res0, err := Align(ref, delayed, Config{
		WindowSize: 512, HopSize: 256, MinLag: 0, MaxLag: 60, MaxDrift: 0,
	})
	if err != nil {
		t.Fatalf("zero-drift Align failed: %v", err)
	}
	for _, p := range res0.Paths {
		for j := 1; j < len(p.Lags); j++ {
			if p.Lags[j] != p.Lags[j-1] {
				t.Fatalf("MaxDrift=0 allowed lag change: %s -> %s", p.Lags[j-1], p.Lags[j])
			}
		}
		if got := p.Lags[0].Float(); math.Abs(got-30) > 1 {
			t.Fatalf("constant lag %s, want 30", p.Lags[0])
		}
	}

	// Disjoint per-window lag sets make every transition infeasible.
	lagSets := [][]int{{0}, {100}, {0}}
	costSets := [][]float64{{0.1}, {0.1}, {0.1}}
	centers := []int{256, 512, 768}
	if _, _, err := solveDP(lagSets, costSets, centers, Config{
		HopSize: 256, MaxDrift: 0, SmoothWeight: 0, MaxPaths: 4, CostEpsilon: 1e-15,
	}); !errors.Is(err, ErrNoFeasiblePath) {
		t.Fatalf("want ErrNoFeasiblePath, got %v", err)
	}
}
