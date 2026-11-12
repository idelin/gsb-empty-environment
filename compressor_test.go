package taskpkg

import (
	"math"
	"testing"
)

const testSampleRate = 48000.0

func baseConfig(channels, lookahead int) Config {
	return Config{
		SampleRate:       testSampleRate,
		Channels:         channels,
		LookaheadSamples: lookahead,
		ThresholdDB:      -20,
		Ratio:            4,
		KneeDB:           6,
		AttackSeconds:    0,
		ReleaseSeconds:   0,
	}
}

func requireClose(t *testing.T, got, want, tolerance float64) {
	t.Helper()
	if math.IsNaN(got) || math.IsInf(got, 0) {
		t.Fatalf("got non-finite value %v, want %v", got, want)
	}
	diff := math.Abs(got - want)
	scale := math.Max(1, math.Abs(want))
	if diff > tolerance*scale {
		t.Fatalf("got %v, want %v (tolerance %v)", got, want, tolerance)
	}
}

func TestStaticGainDBSoftKnee(t *testing.T) {
	const threshold = -20.0
	const ratio = 4.0
	const knee = 6.0
	slope := 1/ratio - 1

	tests := []struct {
		name     string
		inputDB  float64
		expected float64
	}{
		{"below knee", -30, 0},
		{"lower knee edge", threshold - knee/2, 0},
		{"knee center", threshold, slope * (knee / 2) * (knee / 2) / (2 * knee)},
		{"upper knee edge", threshold + knee/2, slope * (knee / 2)},
		{"above knee", 0, slope * 20},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			requireClose(t, StaticGainDB(tc.inputDB, threshold, ratio, knee), tc.expected, 1e-12)
		})
	}

	requireClose(t, StaticGainDB(-20, threshold, ratio, 0), 0, 1e-12)
	requireClose(t, StaticGainDB(0, threshold, ratio, 0), slope*20, 1e-12)
	requireClose(t, StaticGainDB(0, threshold, 0, knee), 0, 1e-12)
}

func TestLevelDBSilenceIsFinite(t *testing.T) {
	for _, peak := range []float64{0, math.Copysign(0, -1), math.NaN(), math.Inf(1), math.Inf(-1)} {
		level := LevelDB(peak, DefaultSilenceFloorDB)
		if math.IsNaN(level) || math.IsInf(level, 0) {
			t.Fatalf("LevelDB(%v) = %v, want a finite floor", peak, level)
		}
		requireClose(t, level, DefaultSilenceFloorDB, 1e-12)
	}

	requireClose(t, LevelDB(1, DefaultSilenceFloorDB), 0, 1e-12)
	requireClose(t, LevelDB(0.5, DefaultSilenceFloorDB), -6.020599913279624, 1e-12)
}

func TestStepLatencyAndLinkedChannels(t *testing.T) {
	const lookahead = 64
	c := MustNewCompressor(baseConfig(2, lookahead))
	frames := lookahead + 40
	input := make([][]float64, 2)
	input[0] = make([]float64, frames)
	input[1] = make([]float64, frames)
	for i := range input[0] {
		input[0][i] = 1
		input[1][i] = 0.5
	}

	output, gainDB, err := c.Process(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(gainDB) != frames {
		t.Fatalf("gain track length %d, want %d", len(gainDB), frames)
	}

	wantGainDB := StaticGainDB(0, -20, 4, 6)
	wantGain := math.Pow(10, wantGainDB/20)
	for i := 0; i < lookahead; i++ {
		if output[0][i] != 0 || output[1][i] != 0 {
			t.Fatalf("output before lookahead at sample %d: %v, %v", i, output[0][i], output[1][i])
		}
		requireClose(t, gainDB[i], wantGainDB, 1e-12)
	}
	for i := lookahead; i < frames; i++ {
		requireClose(t, output[0][i], wantGain, 1e-12)
		requireClose(t, output[1][i], 0.5*wantGain, 1e-12)
		requireClose(t, gainDB[i], wantGainDB, 1e-12)
		requireClose(t, output[1][i]/output[0][i], 0.5, 1e-12)
	}
}

func TestShortImpulsePreservedAfterLookahead(t *testing.T) {
	const lookahead = 32
	cfg := baseConfig(1, lookahead)
	cfg.ThresholdDB = -20
	cfg.Ratio = 4
	cfg.KneeDB = 0
	cfg.AttackSeconds = 0
	cfg.ReleaseSeconds = 0.05
	c := MustNewCompressor(cfg)

	frames := lookahead + 40
	input := make([][]float64, 1)
	input[0] = make([]float64, frames)
	input[0][10] = 1

	output, gainDB, err := c.Process(input)
	if err != nil {
		t.Fatal(err)
	}
	firstNonzero := -1
	for i, sample := range output[0] {
		if sample != 0 {
			if firstNonzero >= 0 {
				t.Fatalf("impulse produced more than one nonzero output (samples %d and %d)", firstNonzero, i)
			}
			firstNonzero = i
		}
	}
	if firstNonzero != 10+lookahead {
		t.Fatalf("impulse arrived at sample %d, want %d", firstNonzero, 10+lookahead)
	}
	if output[0][firstNonzero] <= 0.1 {
		t.Fatalf("impulse was not compressed enough: %v", output[0][firstNonzero])
	}
	for _, gain := range gainDB {
		if math.IsNaN(gain) || math.IsInf(gain, 0) {
			t.Fatalf("non-finite gain in track: %v", gain)
		}
	}
}

func TestDetectorUsesLoudestChannel(t *testing.T) {
	c := MustNewCompressor(baseConfig(2, 0))
	frames := 64
	input := make([][]float64, 2)
	input[0] = make([]float64, frames)
	input[1] = make([]float64, frames)
	for i := range input[0] {
		input[0][i] = 0.001
		input[1][i] = 0.5
	}

	output, gainDB, err := c.Process(input)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < frames; i++ {
		ratio0 := output[0][i] / input[0][i]
		ratio1 := output[1][i] / input[1][i]
		requireClose(t, ratio0, ratio1, 1e-12)
		requireClose(t, gainDB[i], StaticGainDB(-6.020599913279624, -20, 4, 6), 1e-12)
	}
}

func TestBlockwiseParameterUpdateKeepsDelay(t *testing.T) {
	const lookahead = 8
	cfg := baseConfig(1, lookahead)
	cfg.KneeDB = 0
	cfg.Ratio = 2
	cfg.AttackSeconds = 0
	cfg.ReleaseSeconds = 0
	c := MustNewCompressor(cfg)

	frames := 48
	input := make([][]float64, 1)
	input[0] = make([]float64, frames)
	for i := range input[0] {
		input[0][i] = 1
	}

	output := make([][]float64, 1)
	output[0] = make([]float64, frames)
	gainTrack := make([]float64, frames)
	blocks := []struct {
		start int
		end   int
		ratio float64
	}{
		{0, 16, 2},
		{16, 32, 4},
		{32, 48, 8},
	}
	for _, block := range blocks {
		if err := c.SetParams(Params{
			ThresholdDB:    -20,
			Ratio:          block.ratio,
			KneeDB:         0,
			AttackSeconds:  0,
			ReleaseSeconds: 0,
		}); err != nil {
			t.Fatal(err)
		}
		part := make([][]float64, 1)
		part[0] = input[0][block.start:block.end]
		partOutput, partGain, err := c.Process(part)
		if err != nil {
			t.Fatal(err)
		}
		copy(output[0][block.start:block.end], partOutput[0])
		copy(gainTrack[block.start:block.end], partGain)
		if c.LatencySamples() != lookahead {
			t.Fatalf("parameter update changed latency to %d", c.LatencySamples())
		}
	}

	for i := 0; i < lookahead; i++ {
		if output[0][i] != 0 {
			t.Fatalf("sample %d escaped lookahead: %v", i, output[0][i])
		}
	}
	expected := []struct {
		start int
		end   int
		ratio float64
	}{
		{lookahead, 16, 2},
		{16, 32, 4},
		{32, 48, 8},
	}
	for _, segment := range expected {
		wantDB := StaticGainDB(0, -20, segment.ratio, 0)
		want := math.Pow(10, wantDB/20)
		for i := segment.start; i < segment.end; i++ {
			requireClose(t, gainTrack[i], wantDB, 1e-12)
			requireClose(t, output[0][i], want, 1e-12)
		}
	}
}

func TestParameterUpdateAppliesToNextDetectionSample(t *testing.T) {
	cfg := baseConfig(1, 4)
	cfg.KneeDB = 0
	cfg.Ratio = 2
	cfg.AttackSeconds = 0
	cfg.ReleaseSeconds = 0
	c := MustNewCompressor(cfg)

	for i := 0; i < 12; i++ {
		_, gain, err := c.ProcessSample([]float64{1})
		if err != nil {
			t.Fatal(err)
		}
		requireClose(t, gain, StaticGainDB(0, -20, 2, 0), 1e-12)
	}
	if err := c.SetParams(Params{ThresholdDB: -20, Ratio: 4, KneeDB: 0}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 12; i++ {
		_, gain, err := c.ProcessSample([]float64{1})
		if err != nil {
			t.Fatal(err)
		}
		requireClose(t, gain, StaticGainDB(0, -20, 4, 0), 1e-12)
	}
}

func TestBlockSplittingDoesNotLoseSamples(t *testing.T) {
	cfg := baseConfig(2, 17)
	cfg.AttackSeconds = 0.004
	cfg.ReleaseSeconds = 0.08
	frames := 300
	input := make([][]float64, 2)
	for ch := range input {
		input[ch] = make([]float64, frames)
	}
	for i := 0; i < frames; i++ {
		if i >= 40 && i < 90 {
			input[0][i] = 0.8
			input[1][i] = -0.3
		} else if i >= 150 && i < 170 {
			input[0][i] = 0.02
			input[1][i] = 0.02
		}
	}

	whole := MustNewCompressor(cfg)
	wantOutput, wantGain, err := whole.Process(input)
	if err != nil {
		t.Fatal(err)
	}

	split := MustNewCompressor(cfg)
	var gotOutput [][]float64
	var gotGain []float64
	positions := []int{0, 3, 17, 18, 64, 129, 223, 300}
	for i := 0; i+1 < len(positions); i++ {
		part := make([][]float64, 2)
		for ch := range part {
			part[ch] = input[ch][positions[i]:positions[i+1]]
		}
		out, gain, err := split.Process(part)
		if err != nil {
			t.Fatal(err)
		}
		if gotOutput == nil {
			gotOutput = make([][]float64, 2)
			gotOutput[0] = make([]float64, 0, frames)
			gotOutput[1] = make([]float64, 0, frames)
		}
		gotOutput[0] = append(gotOutput[0], out[0]...)
		gotOutput[1] = append(gotOutput[1], out[1]...)
		gotGain = append(gotGain, gain...)
	}

	for ch := 0; ch < 2; ch++ {
		for i := 0; i < frames; i++ {
			requireClose(t, gotOutput[ch][i], wantOutput[ch][i], 1e-12)
		}
	}
	for i := 0; i < frames; i++ {
		requireClose(t, gotGain[i], wantGain[i], 1e-12)
	}
}

func TestSilenceAndNonFiniteInputStayFinite(t *testing.T) {
	cfg := baseConfig(2, 16)
	cfg.AttackSeconds = 0.002
	cfg.ReleaseSeconds = 0.05
	c := MustNewCompressor(cfg)

	input := make([][]float64, 2)
	input[0] = make([]float64, 50)
	input[1] = make([]float64, 50)
	for i := 25; i < 32; i++ {
		input[0][i] = 1
		input[1][i] = math.NaN()
	}
	input[1][31] = math.Inf(1)

	output, gainDB, err := c.Process(input)
	if err != nil {
		t.Fatal(err)
	}
	for i := range gainDB {
		if math.IsNaN(gainDB[i]) || math.IsInf(gainDB[i], 0) {
			t.Fatalf("non-finite gain at %d: %v", i, gainDB[i])
		}
		for ch := range output {
			if math.IsNaN(output[ch][i]) || math.IsInf(output[ch][i], 0) {
				t.Fatalf("non-finite output at channel %d sample %d: %v", ch, i, output[ch][i])
			}
		}
	}
	requireClose(t, gainDB[0], 0, 1e-12)

	quiet := make([][]float64, 2)
	for ch := range quiet {
		quiet[ch] = make([]float64, 100)
	}
	out, gains, err := c.Process(quiet)
	if err != nil {
		t.Fatal(err)
	}
	for i := range gains {
		if math.IsNaN(gains[i]) || math.IsInf(gains[i], 0) {
			t.Fatalf("silence produced non-finite gain at %d: %v", i, gains[i])
		}
		for ch := range out {
			if out[ch][i] != 0 {
				t.Fatalf("silence produced nonzero output: %v", out[ch][i])
			}
		}
	}
}

func TestAttackAndReleaseSmoothing(t *testing.T) {
	cfg := baseConfig(1, 0)
	cfg.KneeDB = 0
	cfg.AttackSeconds = 0.010
	cfg.ReleaseSeconds = 0.100
	c := MustNewCompressor(cfg)

	target := StaticGainDB(0, -20, 4, 0)
	attackCoeff := SmoothingCoefficient(cfg.AttackSeconds, cfg.SampleRate)

	_, gain, err := c.ProcessSample([]float64{1})
	if err != nil {
		t.Fatal(err)
	}
	requireClose(t, gain, (1-attackCoeff)*target, 1e-12)

	for i := 1; i < 480; i++ {
		_, _, err = c.ProcessSample([]float64{1})
		if err != nil {
			t.Fatal(err)
		}
	}
	requireClose(t, c.EnvelopeDB(), target*(1-math.Exp(-1)), 1e-12)

	releaseStart := c.EnvelopeDB()
	releaseCoeff := SmoothingCoefficient(cfg.ReleaseSeconds, cfg.SampleRate)
	_, releaseGain, err := c.ProcessSample([]float64{0})
	if err != nil {
		t.Fatal(err)
	}
	requireClose(t, releaseGain, releaseCoeff*releaseStart, 1e-12)
}

func TestInterleavedMatchesDeinterleaved(t *testing.T) {
	cfg := baseConfig(2, 11)
	cfg.AttackSeconds = 0.001
	cfg.ReleaseSeconds = 0.03
	frames := 80
	deinterleaved := make([][]float64, 2)
	for ch := range deinterleaved {
		deinterleaved[ch] = make([]float64, frames)
	}
	for i := 0; i < frames; i++ {
		deinterleaved[0][i] = math.Sin(float64(i) * 0.2)
		deinterleaved[1][i] = math.Cos(float64(i) * 0.1)
	}
	interleaved := make([]float64, frames*2)
	for i := 0; i < frames; i++ {
		interleaved[2*i] = deinterleaved[0][i]
		interleaved[2*i+1] = deinterleaved[1][i]
	}

	a := MustNewCompressor(cfg)
	wantOutput, wantGain, err := a.Process(deinterleaved)
	if err != nil {
		t.Fatal(err)
	}
	b := MustNewCompressor(cfg)
	gotOutput, gotGain, err := b.ProcessInterleaved(interleaved)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < frames; i++ {
		requireClose(t, gotOutput[2*i], wantOutput[0][i], 1e-12)
		requireClose(t, gotOutput[2*i+1], wantOutput[1][i], 1e-12)
		requireClose(t, gotGain[i], wantGain[i], 1e-12)
	}
}

func TestInvalidConfigurationAndBlocks(t *testing.T) {
	bad := baseConfig(1, 4)
	bad.Ratio = 0
	if _, err := NewCompressor(bad); err == nil {
		t.Fatal("expected zero ratio to be rejected")
	}
	bad = baseConfig(0, 4)
	if _, err := NewCompressor(bad); err == nil {
		t.Fatal("expected zero channels to be rejected")
	}
	bad = baseConfig(1, -1)
	if _, err := NewCompressor(bad); err == nil {
		t.Fatal("expected negative lookahead to be rejected")
	}

	c := MustNewCompressor(baseConfig(1, 4))
	if _, _, err := c.Process([][]float64{{1}, {}}); err == nil {
		t.Fatal("expected channel count mismatch")
	}
	if _, _, err := c.Process([][]float64{{1, 2}}); err != nil {
		t.Fatal(err)
	}
	stereo := MustNewCompressor(baseConfig(2, 4))
	if _, _, err := stereo.ProcessInterleaved([]float64{1, 2, 3}); err == nil {
		t.Fatal("expected interleaved length mismatch")
	}
}

func TestFlushReturnsDelayedTail(t *testing.T) {
	const lookahead = 8
	cfg := baseConfig(1, lookahead)
	cfg.ReleaseSeconds = 0.100
	c := MustNewCompressor(cfg)
	input := make([][]float64, 1)
	input[0] = make([]float64, 16)
	for i := range input[0] {
		input[0][i] = 1
	}
	output, _, err := c.Process(input)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < lookahead; i++ {
		requireClose(t, output[0][i], 0, 1e-12)
	}

	tail, tailGain := c.Flush()
	if len(tail[0]) != lookahead || len(tailGain) != lookahead {
		t.Fatalf("flush length (%d, %d), want %d", len(tail[0]), len(tailGain), lookahead)
	}
	releaseCoeff := SmoothingCoefficient(cfg.ReleaseSeconds, cfg.SampleRate)
	envelope := StaticGainDB(0, -20, 4, 6)
	for i := range tail[0] {
		envelope *= releaseCoeff
		want := math.Pow(10, envelope/20)
		requireClose(t, tail[0][i], want, 1e-12)
	}
}
