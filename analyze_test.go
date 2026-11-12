package taskpkg

import (
	"math"
	"reflect"
	"strings"
	"testing"
)

func sine(samples, sampleRate int, frequency, amplitude, phase float64) []float64 {
	out := make([]float64, samples)
	for i := range out {
		out[i] = amplitude * math.Sin(2*math.Pi*frequency*float64(i)/float64(sampleRate)+phase)
	}
	return out
}

func signalSet(samples int) ([]string, [][]float64) {
	left := sine(samples, 48000, 440, 0.20, 0)
	right := make([]float64, samples)
	copy(right, left)
	for i := range right {
		right[i] *= 0.75
	}
	inverted := make([]float64, samples)
	for i := range inverted {
		inverted[i] = -0.9 * left[i]
	}
	return []string{"L", "R", "Ls", "Rinv"}, [][]float64{left, right, left, inverted}
}

func TestAnalyzeInPhaseAndInverted(t *testing.T) {
	names, samples := signalSet(8 * 1024)
	cfg := DefaultConfig()
	cfg.WindowSize = 1024
	cfg.ConfirmationWindows = 2
	cfg.CorrelationThreshold = 0.95
	cfg.MinEnergyRatio = 0.5
	cfg.MaxEnergyRatio = 2.0

	report, err := Analyze(names, samples, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Windows) != 8 {
		t.Fatalf("windows = %d, want 8", len(report.Windows))
	}
	if len(report.Edges) != 6 {
		t.Fatalf("edges = %d, want 6: %+v", len(report.Edges), report.Edges)
	}
	if !report.Consistent {
		t.Fatalf("expected a consistent assignment, conflict: %+v", report.Conflict)
	}
	wantPolarity := map[int]int{0: 1, 1: 1, 2: 1, 3: -1}
	for channel, want := range wantPolarity {
		if report.Assignment[channel] != want {
			t.Errorf("channel %d assignment = %d, want %d", channel, report.Assignment[channel], want)
		}
	}
	for _, edge := range report.Edges {
		want := 1
		if edge.A == 3 || edge.B == 3 {
			want = -1
		}
		if edge.Sign != want {
			t.Errorf("edge %d-%d sign = %d, want %d", edge.A, edge.B, edge.Sign, want)
		}
		if edge.Consecutive < 2 {
			t.Errorf("edge %d-%d has only %d consecutive windows", edge.A, edge.B, edge.Consecutive)
		}
	}
	for _, window := range report.Windows {
		for _, pair := range window.Pairs {
			if math.Abs(math.Abs(pair.Correlation)-1) > 1e-12 {
				t.Errorf("window %d pair %d-%d correlation = %.16f", window.Index, pair.A, pair.B, pair.Correlation)
			}
		}
	}
}

func TestDelayDoesNotInventAnInversion(t *testing.T) {
	samples := 8 * 512
	left := sine(samples, 48000, 440, 0.25, 0)
	right := make([]float64, samples)
	copy(right[1:], left[:samples-1])
	cfg := DefaultConfig()
	cfg.WindowSize = 512
	cfg.ConfirmationWindows = 1
	cfg.CorrelationThreshold = 0.90

	report, err := Analyze([]string{"direct", "delayed"}, [][]float64{left, right}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Edges) != 1 {
		t.Fatalf("edges = %+v, want one delayed pair", report.Edges)
	}
	edge := report.Edges[0]
	if edge.Sign != 1 {
		t.Fatalf("one-sample delay sign = %d, want +1", edge.Sign)
	}
	if edge.Correlation < 0.95 {
		t.Fatalf("one-sample delay correlation = %.6f, expected high positive", edge.Correlation)
	}
}

func TestLargeZeroLagDelayIsUnknown(t *testing.T) {
	samples := 6 * 512
	left := sine(samples, 48000, 440, 0.25, 0)
	right := make([]float64, samples)
	// A 27-sample delay is roughly a quarter period at 440 Hz, so the
	// zero-lag correlation is near zero rather than a trustworthy inversion.
	copy(right[27:], left[:samples-27])
	cfg := DefaultConfig()
	cfg.WindowSize = 512
	cfg.ConfirmationWindows = 1
	cfg.CorrelationThreshold = 0.90

	report, err := Analyze([]string{"direct", "delayed"}, [][]float64{left, right}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Edges) != 0 {
		t.Fatalf("large zero-lag delay built an edge: %+v", report.Edges)
	}
	if len(report.Pairs) != 1 || report.Pairs[0].State != PairWeakCorrelation {
		t.Fatalf("delay pair = %+v, want weak_correlation", report.Pairs)
	}
}

func TestInconsistentTriangleReport(t *testing.T) {
	const (
		windowSize = 1024
		windows    = 2
	)
	n := windowSize * windows
	// Two orthogonal sinusoids over each fixed window produce this correlation
	// matrix with signs (+,-,+), whose product is negative:
	//   corr(0,1)=+0.5, corr(0,2)=-0.5, corr(1,2)=+0.5.
	z0 := sine(n, 48000, 48000*10/windowSize, 0.2, 0)
	z1 := sine(n, 48000, 48000*17/windowSize, 0.2, 0)
	channels := make([][]float64, 3)
	for i := 0; i < n; i++ {
		channels[0] = append(channels[0], z0[i])
		channels[1] = append(channels[1], 0.5*z0[i]+math.Sqrt(0.75)*z1[i])
		channels[2] = append(channels[2], -0.5*z0[i]+math.Sqrt(0.75)*z1[i])
	}
	cfg := DefaultConfig()
	cfg.WindowSize = windowSize
	cfg.ConfirmationWindows = 1
	cfg.CorrelationThreshold = 0.4

	report, err := Analyze([]string{"A", "B", "C"}, channels, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if report.Consistent {
		t.Fatalf("triangle with one negative relationship reported consistent: edges=%+v pairs=%+v", report.Edges, report.Pairs)
	}
	if report.Conflict == nil || report.Conflict.Length != 3 {
		t.Fatalf("conflict = %+v, want a three-edge cycle", report.Conflict)
	}
	if cycleSign(report.Conflict) != -1 {
		t.Fatalf("conflict sign = %d", cycleSign(report.Conflict))
	}
	for _, step := range report.Conflict.Steps {
		if step.EdgeIndex < 0 || step.EdgeIndex >= len(report.Edges) {
			t.Fatalf("step references invalid edge: %+v", step)
		}
		if report.Edges[step.EdgeIndex].Sign != step.Sign {
			t.Fatalf("step sign does not match edge: %+v", step)
		}
	}
	for _, diagnostic := range report.Diagnostics {
		if diagnostic.Code == "polarity_conflict" && diagnostic.Severity != "error" {
			t.Fatalf("conflict diagnostic = %+v", diagnostic)
		}
	}
}

func TestAnalyzeSamplesGeneratesNames(t *testing.T) {
	_, samples := signalSet(2 * 128)
	cfg := DefaultConfig()
	cfg.WindowSize = 128
	cfg.ConfirmationWindows = 1
	report, err := AnalyzeSamples(samples[:2], cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(report.Channels, []string{"ch0", "ch1"}) {
		t.Fatalf("channels = %v", report.Channels)
	}
}

func TestSilentAndEnergyOutlierAreUnknown(t *testing.T) {
	n := 4 * 256
	active := sine(n, 48000, 440, 0.2, 0)
	silent := make([]float64, n)
	for i := range silent {
		silent[i] = 0.4 // constant: zero energy after mean removal
	}
	loud := make([]float64, n)
	for i := range loud {
		loud[i] = 4 * active[i]
	}

	cfg := DefaultConfig()
	cfg.WindowSize = 256
	cfg.ConfirmationWindows = 1
	cfg.CorrelationThreshold = 0.9
	cfg.MinEnergyRatio = 0.5
	cfg.MaxEnergyRatio = 2.0
	report, err := Analyze([]string{"active", "silent", "loud"}, [][]float64{active, silent, loud}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Edges) != 0 {
		t.Fatalf("unknown pairs built edges: %+v", report.Edges)
	}
	for _, pair := range report.Pairs {
		if pair.A == 1 || pair.B == 1 {
			if pair.State != PairSilent {
				t.Errorf("pair %d-%d state = %s, want silent", pair.A, pair.B, pair.State)
			}
		}
		if pair.A == 0 && pair.B == 2 {
			if pair.State != PairEnergyOutOfRange {
				t.Errorf("active-loud state = %s, want energy_ratio_out_of_range", pair.State)
			}
		}
		if pair.Confirmed {
			t.Errorf("pair %d-%d unexpectedly confirmed", pair.A, pair.B)
		}
	}
}

func TestConsecutiveHitConfirmation(t *testing.T) {
	names, samples := signalSet(3 * 256)
	cfg := DefaultConfig()
	cfg.WindowSize = 256
	cfg.ConfirmationWindows = 3
	cfg.CorrelationThreshold = 0.9

	report, err := Analyze(names[:2], samples[:2], cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Edges) != 1 {
		t.Fatalf("three consecutive hits should build one edge: %+v", report.Edges)
	}
	if len(report.Pairs) != 1 || report.Pairs[0].MaxConsecutive != 3 {
		t.Fatalf("pair summary = %+v", report.Pairs)
	}
	if !report.Pairs[0].Confirmed {
		t.Fatalf("three consecutive hits should confirm: %+v", report.Pairs[0])
	}

	cfg.ConfirmationWindows = 4
	report, err = Analyze(names[:2], samples[:2], cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Edges) != 0 || report.Pairs[0].Confirmed {
		t.Fatalf("edge confirmed without four consecutive hits: edges=%+v pair=%+v", report.Edges, report.Pairs[0])
	}
}

func TestBlockEquivalenceWithOverlap(t *testing.T) {
	names, samples := signalSet(5000)
	cfg := DefaultConfig()
	cfg.WindowSize = 256
	cfg.HopSize = 128
	cfg.ConfirmationWindows = 2
	cfg.CorrelationThreshold = 0.9

	full, err := Analyze(names, samples, cfg)
	if err != nil {
		t.Fatal(err)
	}
	stream, err := NewAnalyzer(names, cfg)
	if err != nil {
		t.Fatal(err)
	}
	chunkSizes := []int{1, 16, 238, 3, 297, 1000, 2048, 1397}
	offsets := []int{0}
	for _, size := range chunkSizes {
		offsets = append(offsets, offsets[len(offsets)-1]+size)
	}
	for i := 0; i+1 < len(offsets); i++ {
		start, end := offsets[i], offsets[i+1]
		block := make([][]float64, len(samples))
		for channel := range samples {
			block[channel] = samples[channel][start:end]
		}
		if err := stream.Push(block); err != nil {
			t.Fatal(err)
		}
	}
	// Include a partial trailing window and an empty push.
	last := offsets[len(offsets)-1]
	block := make([][]float64, len(samples))
	for channel := range samples {
		block[channel] = samples[channel][last:]
	}
	if err := stream.Push(block); err != nil {
		t.Fatal(err)
	}
	if err := stream.Push(make([][]float64, len(samples))); err != nil {
		t.Fatal(err)
	}
	blocked := stream.Finish()
	if !reflect.DeepEqual(full, blocked) {
		t.Fatalf("blocked analysis differs from full analysis\nfull:    %#v\nblocked: %#v", full, blocked)
	}
}

func TestPushChannelEquivalence(t *testing.T) {
	names, samples := signalSet(3000)
	cfg := DefaultConfig()
	cfg.WindowSize = 256
	cfg.HopSize = 64
	cfg.ConfirmationWindows = 2

	full, err := Analyze(names, samples, cfg)
	if err != nil {
		t.Fatal(err)
	}
	stream, err := NewAnalyzer(names, cfg)
	if err != nil {
		t.Fatal(err)
	}
	chunkSizes := []int{1, 255, 300, 7, 1000, 1437}
	offset := 0
	for _, size := range chunkSizes {
		for channel := range samples {
			if err := stream.PushChannel(channel, samples[channel][offset:offset+size]); err != nil {
				t.Fatal(err)
			}
		}
		offset += size
	}
	blocked := stream.Finish()
	if !reflect.DeepEqual(full, blocked) {
		t.Fatalf("PushChannel analysis differs from full analysis\nfull:    %#v\nblocked: %#v", full, blocked)
	}
}

func TestAnalyzeValidation(t *testing.T) {
	cfg := DefaultConfig()
	cfg.WindowSize = 16
	if _, err := Analyze([]string{"a", "b"}, [][]float64{make([]float64, 16)}, cfg); err == nil {
		t.Fatal("mismatched channel count accepted")
	}
	if _, err := Analyze([]string{"a", "b"}, [][]float64{make([]float64, 16), make([]float64, 8)}, cfg); err == nil {
		t.Fatal("mismatched block lengths accepted")
	}
	if _, err := Analyze([]string{"a", "a"}, [][]float64{make([]float64, 16), make([]float64, 16)}, cfg); err == nil ||
		!strings.Contains(err.Error(), "unique") {
		t.Fatalf("duplicate names error = %v", err)
	}
	bad := make([]float64, 16)
	bad[3] = math.NaN()
	if _, err := Analyze([]string{"a", "b"}, [][]float64{bad, make([]float64, 16)}, cfg); err == nil {
		t.Fatal("NaN sample accepted")
	}
}
