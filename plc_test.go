package taskpkg

import (
	"math"
	"math/rand"
	"reflect"
	"testing"
)

func testConfig() Config {
	return Config{
		Window:         256,
		Order:          8,
		MinPeriod:      4,
		MaxPeriod:      64,
		MinCorrelation: 0.70,
		Decay:          0.999,
		Crossfade:      8,
		Seed:           7,
	}
}

func TestSilenceStaysSilent(t *testing.T) {
	p, err := New(testConfig())
	if err != nil {
		t.Fatal(err)
	}
	zeros := make([]float64, 300)
	p.FeedStereo(zeros, zeros)

	left, right := p.ConcealStereo(100)
	for i := range left {
		if left[i] != 0 || right[i] != 0 {
			t.Fatalf("silent gap generated non-zero sample at %d: %g, %g", i, left[i], right[i])
		}
	}
	analysis := p.LastGapAnalysis()
	if analysis.Periodic || analysis.ResidualRMS[0] != 0 || analysis.ResidualRMS[1] != 0 {
		t.Fatalf("unexpected silence analysis: %+v", analysis)
	}
}

func TestStereoUsesCommonPeriodWithoutPhaseCancellation(t *testing.T) {
	cfg := testConfig()
	cfg.Window = 192
	cfg.Order = 10
	cfg.MinPeriod = 5
	cfg.MaxPeriod = 40
	cfg.Crossfade = 12
	p, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	const period = 13
	n := 260
	left := make([]float64, n)
	for i := range left {
		left[i] = 0.6 * math.Sin(2*math.Pi*float64(i)/period)
	}
	right := negate(left)
	p.FeedStereo(left, right)

	outLeft, outRight := p.ConcealStereo(90)
	analysis := p.LastGapAnalysis()
	if !analysis.Periodic || analysis.Period != period {
		t.Fatalf("common period = (%d, %g, periodic=%v), want %d",
			analysis.Period, analysis.PeriodCorrelation, analysis.Periodic, period)
	}
	if analysis.Models[0].Order == 0 || analysis.Models[1].Order == 0 {
		t.Fatalf("expected independent non-zero predictors: %+v", analysis.Models)
	}

	var leftEnergy, rightEnergy, cross float64
	for i := range outLeft {
		leftEnergy += outLeft[i] * outLeft[i]
		rightEnergy += outRight[i] * outRight[i]
		cross += outLeft[i] * outRight[i]
	}
	if leftEnergy < 1 || rightEnergy < 1 {
		t.Fatalf("anti-phase channels were cancelled: energies %g, %g", leftEnergy, rightEnergy)
	}
	if cross >= 0 {
		t.Fatalf("concealed channels lost anti-phase relationship: cross=%g", cross)
	}
	for i := range outLeft {
		if math.Abs(outLeft[i]+outRight[i]) > 1e-10 {
			t.Fatalf("anti-phase mismatch at %d: %g + %g", i, outLeft[i], outRight[i])
		}
	}
}

func TestNoiseIsReproducibleZeroMeanAndEnergyMatched(t *testing.T) {
	cfg := testConfig()
	cfg.MinCorrelation = 0.99
	cfg.Order = 4
	cfg.MaxPeriod = 48
	rng := rand.New(rand.NewSource(123))
	n := cfg.Window * 2
	noisy := make([]float64, n)
	for i := range noisy {
		noisy[i] = rng.NormFloat64()
	}

	run := func(seed int64) ([]float64, GapAnalysis, *PLC) {
		local := cfg
		local.Seed = seed
		p, err := New(local)
		if err != nil {
			t.Fatal(err)
		}
		p.FeedStereo(noisy, noisy)
		left, _ := p.ConcealStereo(512)
		return left, p.LastGapAnalysis(), p
	}

	left1, analysis1, p1 := run(101)
	left2, _, _ := run(101)
	left3, _, _ := run(202)
	if !reflect.DeepEqual(left1, left2) {
		t.Fatal("same seed produced different concealment output")
	}
	if reflect.DeepEqual(left1, left3) {
		t.Fatal("different seeds produced identical noise")
	}
	if analysis1.Periodic {
		t.Fatalf("white-noise test unexpectedly used periodic residuals: %+v", analysis1)
	}

	// In noise mode the last retained residual window is exactly 256 values
	// from a normalized noise block, so both mean and RMS are exact.
	residual := p1.channel[0].residualHistory.snapshot()
	if len(residual) != cfg.Window {
		t.Fatalf("residual window length = %d, want %d", len(residual), cfg.Window)
	}
	var mean float64
	for _, v := range residual {
		mean += v
	}
	mean /= float64(len(residual))
	if math.Abs(mean) > 1e-12*analysis1.ResidualRMS[0] {
		t.Fatalf("noise mean = %g, want zero (RMS %g)", mean, analysis1.ResidualRMS[0])
	}
	if got := rms(residual); math.Abs(got-analysis1.ResidualRMS[0]) > 1e-10 {
		t.Fatalf("noise RMS = %g, want measured residual RMS %g", got, analysis1.ResidualRMS[0])
	}
}

func TestGapDecayIsExactEnvelope(t *testing.T) {
	cfg := testConfig()
	cfg.Crossfade = 0
	cfg.Decay = 0.9
	n := 300
	signal := arSignal(n, 0.8, -0.15, 0.03, 5)

	controlled, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	undampedCfg := cfg
	undampedCfg.Decay = 1
	undamped, err := New(undampedCfg)
	if err != nil {
		t.Fatal(err)
	}
	controlled.FeedStereo(signal, signal)
	undamped.FeedStereo(signal, signal)

	dampedGap, _ := controlled.ConcealStereo(80)
	fullGap, _ := undamped.ConcealStereo(80)
	for i := range dampedGap {
		wantGain := math.Pow(cfg.Decay, float64(i))
		want := fullGap[i] * wantGain
		if math.Abs(dampedGap[i]-want) > 1e-12*math.Max(1, math.Abs(want)) {
			t.Fatalf("gap sample %d = %g, want %g (gain %g)", i, dampedGap[i], want, wantGain)
		}
	}
}

func TestRecoveryCrossfadeAndExactBoundary(t *testing.T) {
	cfg := testConfig()
	cfg.Crossfade = 6
	p, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	const period = 11
	prefix := periodicSignal(240, period)
	p.FeedStereo(prefix, negate(prefix))
	p.ConcealStereo(40)

	resumed := periodicSignal(30, period)
	outLeft, outRight := p.FeedStereo(resumed, negate(resumed))
	if len(outLeft) != len(resumed) || len(outRight) != len(resumed) {
		t.Fatalf("recovery changed sample count: %d, %d, want %d", len(outLeft), len(outRight), len(resumed))
	}
	// The last blended sample has full real weight; later samples pass through.
	for i := cfg.Crossfade - 1; i < len(resumed); i++ {
		if outLeft[i] != resumed[i] || outRight[i] != -resumed[i] {
			t.Fatalf("recovery sample %d did not reach real PCM: got (%g, %g)", i, outLeft[i], outRight[i])
		}
	}
	if p.InGap() {
		t.Fatal("engine still reports a gap after crossfade")
	}
}

func TestInterruptedRecoveryRestartsFromRealSamples(t *testing.T) {
	cfg := testConfig()
	cfg.Crossfade = 7
	p, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	signal := periodicSignal(240, 12)
	p.FeedStereo(signal, negate(signal))

	firstGap, _ := p.ConcealStereo(30)
	partial := periodicSignal(3, 12)
	partialOut, _ := p.FeedStereo(partial, negate(partial))
	secondGap, _ := p.ConcealStereo(30)
	final := periodicSignal(20, 12)
	finalOut, _ := p.FeedStereo(final, negate(final))

	for _, block := range [][]float64{firstGap, partialOut, secondGap, finalOut} {
		for i, v := range block {
			if math.IsNaN(v) || math.IsInf(v, 0) {
				t.Fatalf("non-finite sample in interrupted recovery at %d", i)
			}
		}
	}
	if p.InGap() {
		t.Fatal("engine still in a gap after a full recovery")
	}
	for i := cfg.Crossfade - 1; i < len(finalOut); i++ {
		if finalOut[i] != final[i] {
			t.Fatalf("final recovery sample %d = %g, want %g", i, finalOut[i], final[i])
		}
	}
}

func TestResetReplaysIdentically(t *testing.T) {
	cfg := testConfig()
	cfg.Seed = 99
	signal := arSignal(320, 0.75, -0.2, 0.05, 31)
	p, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	run := func() ([]float64, []float64) {
		p.FeedStereo(signal, signal)
		return p.ConcealStereo(100)
	}
	left1, right1 := run()
	p.Reset()
	left2, right2 := run()
	if !reflect.DeepEqual(left1, left2) || !reflect.DeepEqual(right1, right2) {
		t.Fatal("Reset did not restore deterministic engine state")
	}
}

func TestArbitraryChunkingProducesIdenticalOutput(t *testing.T) {
	cfg := testConfig()
	cfg.Window = 128
	cfg.MaxPeriod = 32
	cfg.Crossfade = 9

	const (
		prefixLen = 200
		gapLen    = 70
		resumeLen = 80
	)
	total := prefixLen + gapLen + resumeLen
	left := make([]float64, total)
	right := make([]float64, total)
	rng := rand.New(rand.NewSource(41))
	for i := 0; i < prefixLen; i++ {
		left[i] = arStep(rng, 0.7, -0.1, 0.08, i)
		right[i] = -left[i]
	}
	for i := prefixLen; i < prefixLen+gapLen; i++ {
		left[i] = arStep(rng, 0.7, -0.1, 0.08, i)
		right[i] = -left[i]
	}
	for i := prefixLen + gapLen; i < total; i++ {
		left[i] = arStep(rng, 0.7, -0.1, 0.08, i)
		right[i] = -left[i]
	}
	valid := make([]bool, total)
	for i := range valid {
		valid[i] = i < prefixLen || i >= prefixLen+gapLen
	}

	one, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	wantLeft, wantRight := one.ProcessStereo(left, right, valid)

	chunked, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var gotLeft, gotRight []float64
	for start := 0; start < total; {
		size := 1 + (start*7)%19
		if start+size > total {
			size = total - start
		}
		l, r := chunked.ProcessStereo(left[start:start+size], right[start:start+size], valid[start:start+size])
		gotLeft = append(gotLeft, l...)
		gotRight = append(gotRight, r...)
		start += size
	}
	if !reflect.DeepEqual(gotLeft, wantLeft) || !reflect.DeepEqual(gotRight, wantRight) {
		t.Fatal("arbitrary host chunk boundaries changed concealment output")
	}
}

func TestLongGapSynthesisStaysFiniteAndBounded(t *testing.T) {
	cfg := testConfig()
	cfg.Decay = 1
	cfg.Crossfade = 0
	p, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	signal := arSignal(600, 0.85, -0.2, 0.05, 17)
	p.FeedStereo(signal, signal)
	left, _ := p.ConcealStereo(5000)
	for i, v := range left {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			t.Fatalf("non-finite sample at %d", i)
		}
	}
	first := rms(left[:100])
	last := rms(left[4900:])
	if last > 2*first+1e-9 {
		t.Fatalf("synthesis grew across a long gap: first RMS %g, last RMS %g", first, last)
	}
}

func TestProcessStereoIntoAndInterleavedKeepCounts(t *testing.T) {
	p, err := New(testConfig())
	if err != nil {
		t.Fatal(err)
	}
	left := make([]float64, 50)
	right := make([]float64, 50)
	valid := make([]bool, 50)
	for i := range valid {
		valid[i] = i < 20 || i >= 30
	}
	dstLeft := make([]float64, 50)
	dstRight := make([]float64, 50)
	if err := p.ProcessStereoInto(dstLeft, dstRight, left, right, valid); err != nil {
		t.Fatal(err)
	}

	p.Reset()
	interleaved := make([]float64, 100)
	out := p.ProcessInterleavedStereo(interleaved, valid)
	if len(out) != len(interleaved) {
		t.Fatalf("interleaved output length = %d, want %d", len(out), len(interleaved))
	}
}

func TestConfigValidation(t *testing.T) {
	if err := DefaultConfig(48000).Validate(); err != nil {
		t.Fatalf("default 48 kHz config invalid: %v", err)
	}
	if err := DefaultConfig(0).Validate(); err != nil {
		t.Fatalf("fallback config invalid: %v", err)
	}
	cfg := testConfig()
	cfg.MaxPeriod = cfg.Window
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected invalid period range")
	}
	cfg = testConfig()
	cfg.Decay = 1.1
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected invalid decay")
	}
	cfg = testConfig()
	cfg.MinCorrelation = math.NaN()
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected invalid NaN correlation")
	}
}

func periodicSignal(n, period int) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = math.Sin(2*math.Pi*float64(i)/float64(period)) +
			0.25*math.Sin(4*math.Pi*float64(i)/float64(period))
	}
	return out
}

func arSignal(n int, a1, a2, sigma float64, seed int64) []float64 {
	rng := rand.New(rand.NewSource(seed))
	out := make([]float64, n)
	prev2, prev1 := 0.0, 0.0
	for i := range out {
		e := rng.NormFloat64() * sigma
		out[i] = a1*prev1 + a2*prev2 + e
		prev2, prev1 = prev1, out[i]
	}
	return out
}

func arStep(rng *rand.Rand, a1, a2, sigma float64, index int) float64 {
	// Stateless helper used only to produce a varying but bounded test stream.
	prev1 := float64((index+1)%11) / 11.0
	prev2 := float64(index%7) / 7.0
	return a1*prev1 + a2*prev2 + rng.NormFloat64()*sigma
}
