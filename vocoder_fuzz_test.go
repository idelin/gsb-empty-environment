package taskpkg

import (
	"math"
	"testing"
)

func FuzzStretchInvariants(f *testing.F) {
	f.Add(1024, 256, int64(2), int64(1), 3000, 7)
	f.Add(512, 128, int64(1), int64(3), 1700, 1)
	f.Add(256, 64, int64(5), int64(2), 999, 123)

	f.Fuzz(func(t *testing.T, fftLen, hop int, num, den int64, samples, writeSize int) {
		// Restrict to the constructor's supported configuration space.
		if fftLen < 64 || fftLen > 4096 || fftLen&(fftLen-1) != 0 {
			return
		}
		if hop < 1 || hop >= fftLen {
			return
		}
		if num < 1 || num > 16 || den < 1 || den > 16 {
			return
		}
		if samples < 0 || samples > 6000 {
			return
		}
		if writeSize < 1 {
			return
		}

		in := make([]float64, samples)
		for i := range in {
			in[i] = math.Sin(float64(i) * 0.07)
		}

		out, err := Process(fftLen, hop, num, den, in)
		if err != nil {
			t.Fatalf("Process: %v", err)
		}

		// No non-finite values may ever escape the vocoder.
		for i, sample := range out {
			if math.IsNaN(sample) || math.IsInf(sample, 0) {
				t.Fatalf("non-finite output at %d: %v", i, sample)
			}
		}

		// Duration must follow the ratio to within one analysis hop.
		want := int(math.Round(float64(samples) * float64(num) / float64(den)))
		if diff := math.Abs(float64(len(out) - want)); diff > float64(hop) {
			t.Fatalf("duration = %d, want %d ± %d", len(out), want, hop)
		}

		// Determinism: a second run with the same stream must match.
		again, err := Process(fftLen, hop, num, den, in)
		if err != nil {
			t.Fatal(err)
		}
		if len(again) != len(out) {
			t.Fatalf("non-deterministic length: %d vs %d", len(again), len(out))
		}
		for i := range out {
			if again[i] != out[i] {
				t.Fatalf("non-deterministic sample at %d", i)
			}
		}
	})
}
