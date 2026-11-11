package tsblock

import (
	"math"
	"math/rand"
	"testing"
)

// encodeAll seals a stream into frames and returns every frame's samples
// decoded in order.
func encodeAll(t *testing.T, samples []Sample, maxSize int) ([][]byte, []Sample) {
	t.Helper()

	enc := NewEncoder(maxSize)
	for _, s := range samples {
		if err := enc.Append(s); err != nil {
			t.Fatalf("append (%v): %v", s, err)
		}
	}
	frames, err := enc.Seal()
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if len(frames) == 0 {
		t.Fatal("seal returned no frames")
	}

	var got []Sample
	for fi, frame := range frames {
		dec, err := NewDecoder(frame)
		if err != nil {
			t.Fatalf("frame %d decode open: %v", fi, err)
		}
		count := dec.Count()
		var n uint64
		for dec.Next() {
			got = append(got, Sample{T: dec.Time(), V: dec.Value()})
			n++
		}
		if err := dec.Err(); err != nil {
			t.Fatalf("frame %d scan: %v", fi, err)
		}
		if n != count {
			t.Fatalf("frame %d: decoded %d samples, header says %d", fi, n, count)
		}
	}
	return frames, got
}

func assertRoundTrip(t *testing.T, want, got []Sample) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("sample count: got %d want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].T != want[i].T {
			t.Fatalf("sample %d timestamp: got %d want %d", i, got[i].T, want[i].T)
		}
		wantBits := math.Float64bits(want[i].V)
		gotBits := math.Float64bits(got[i].V)
		if gotBits != wantBits {
			t.Fatalf("sample %d value bits: got %#016x want %#016x", i, gotBits, wantBits)
		}
	}
}

func compressionRatio(t *testing.T, name string, samples []Sample, frames [][]byte) float64 {
	t.Helper()
	var compressed int
	for _, f := range frames {
		compressed += len(f)
	}
	raw := len(samples) * 16
	ratio := float64(compressed) / float64(raw)
	t.Logf("%s: samples=%d raw=%dB compressed=%dB ratio=%.4f (%.1f%% of raw, %.1fx smaller)",
		name, len(samples), raw, compressed, ratio, ratio*100, 1/ratio)
	return ratio
}

// specialValues covers NaN payloads, negative zero, infinities and subnormals.
func specialValues() []float64 {
	return []float64{
		math.NaN(),
		math.Float64frombits(0x7ff8000000000001), // signaling NaN payload
		math.Float64frombits(0xfff80000000000ab), // negative NaN payload
		math.Copysign(0, -1),                     // negative zero
		0,
		math.Inf(1),
		math.Inf(-1),
		math.Float64frombits(0x0000000000000001), // smallest subnormal
		math.Float64frombits(0x800fffffffffffff),
		3.141592653589793,
		-2.718281828459045,
	}
}

func TestRandomRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(20261111))
	const n = 20000
	samples := make([]Sample, n)

	ts := uint64(1_700_000_000_000)
	for i := range samples {
		var step uint64
		if rng.Intn(20) == 0 {
			step = uint64(rng.Intn(5000)) + 50 // big gap
		} else {
			step = 95 + uint64(rng.Intn(11)) // 95..105
		}
		ts += step

		var v float64
		switch rng.Intn(30) {
		case 0:
			v = specialValues()[rng.Intn(len(specialValues()))]
		case 1:
			v = math.Float64frombits(rng.Uint64()) // random bit pattern
		default:
			v = rng.NormFloat64() * 100
		}
		samples[i] = Sample{T: ts, V: v}
	}

	frames, got := encodeAll(t, samples, DefaultMaxBlockSize)
	assertRoundTrip(t, samples, got)
	compressionRatio(t, "random", samples, frames)

	if len(frames) < 2 {
		t.Fatalf("expected size-based sealing to produce multiple frames, got %d", len(frames))
	}
}

// realisticSamples builds a CPU-usage-like curve: slow drift, plateaus, spikes
// and a periodic component, on a near-constant tick.
func realisticSamples(n int, rng *rand.Rand) []Sample {
	out := make([]Sample, n)
	ts := uint64(1_700_000_000_000)
	level := 35.0
	for i := 0; i < n; i++ {
		ts += 100 + uint64(rng.Intn(3)-1) // 99..101
		// Long plateaus with tiny noise dominate real metrics; level changes
		// are small walks punctuated by occasional step shifts and spikes.
		switch {
		case i%2000 == 0:
			level = 80 + rng.Float64()*15 // spike
		case i%250 == 0:
			level += rng.NormFloat64() * 4 // occasional step
		default:
			level += rng.NormFloat64() * 0.02
		}
		if level < 2 {
			level = 2
		}
		if level > 98 {
			level = 98
		}
		v := level + math.Sin(float64(i)/40)*0.5
		if i%777 == 0 {
			v = math.Copysign(0, -1) // negative zero stays bit-identical
		}
		if i%991 == 0 {
			v = math.NaN()
		}
		out[i] = Sample{T: ts, V: v}
	}
	return out
}

func TestRealisticRoundTripAndRatio(t *testing.T) {
	rng := rand.New(rand.NewSource(424242))
	samples := realisticSamples(30000, rng)

	frames, got := encodeAll(t, samples, DefaultMaxBlockSize)
	assertRoundTrip(t, samples, got)
	ratio := compressionRatio(t, "realistic-cpu", samples, frames)

	// Smooth metrics compress well: timestamps mostly use 1-3 bits and values
	// share XOR windows. Require a clear saving over raw 16B/sample even with
	// the injected NaN, negative-zero and spike outliers.
	if ratio > 0.55 {
		t.Fatalf("compression ratio %.3f exceeds 0.55 budget", ratio)
	}
}

func TestSpecialValuesBitExact(t *testing.T) {
	vals := specialValues()
	samples := make([]Sample, len(vals))
	for i, v := range vals {
		samples[i] = Sample{T: uint64(1000 + i*100), V: v}
	}
	_, got := encodeAll(t, samples, 1<<20)
	assertRoundTrip(t, samples, got)

	if math.Float64bits(got[0].V) != math.Float64bits(math.NaN()) {
		t.Fatalf("canonical NaN bits not preserved: %#x", math.Float64bits(got[0].V))
	}
	if math.Float64bits(got[3].V) != math.Float64bits(math.Copysign(0, -1)) {
		t.Fatalf("negative zero not preserved: %#x", math.Float64bits(got[3].V))
	}
}
