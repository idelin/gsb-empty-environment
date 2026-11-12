package stft

import (
	"errors"
	"math"
	"math/rand"
	"testing"
)

func identityCallback(spectrum []complex128) error {
	return nil
}

func periodicHann(n int) []float64 {
	w := make([]float64, n)
	for i := range w {
		w[i] = 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(n))
	}
	return w
}

func maxAbsDiff(a, b []float64) float64 {
	if len(a) != len(b) {
		return math.Inf(1)
	}
	var max float64
	for i := range a {
		if d := math.Abs(a[i] - b[i]); d > max {
			max = d
		}
	}
	return max
}

func runChunked(t *testing.T, window []float64, hop int, cb SpectrumFunc, input []float64, chunks []int) ([]float64, int) {
	t.Helper()
	a, err := New(window, hop, cb)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	var out []float64
	frames := 0
	wrapped := cb
	if cb != nil {
		wrapped = func(s []complex128) error {
			frames++
			return cb(s)
		}
	}
	if wrapped != nil {
		a.cb = wrapped
	}

	pos := 0
	for _, size := range chunks {
		if size > len(input)-pos {
			size = len(input) - pos
		}
		chunk, err := a.Push(input[pos : pos+size])
		if err != nil {
			t.Fatalf("Push at %d: %v", pos, err)
		}
		out = append(out, chunk...)
		pos += size
		if pos == len(input) {
			break
		}
	}
	if pos < len(input) {
		chunk, err := a.Push(input[pos:])
		if err != nil {
			t.Fatalf("final Push: %v", err)
		}
		out = append(out, chunk...)
	}
	tail, err := a.Flush()
	if err != nil {
		t.Fatalf("Flush: %v", err)
	}
	out = append(out, tail...)
	if wrapped == nil {
		frames = -1
	}
	return out, frames
}

func TestFFTMatchesNaiveDFT(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for n := 1; n <= 37; n++ {
		x := make([]complex128, n)
		for i := range x {
			x[i] = complex(rng.NormFloat64(), rng.NormFloat64())
		}

		got := append([]complex128(nil), x...)
		p := newFFTPlan(n)
		p.forward(got)

		want := make([]complex128, n)
		for k := 0; k < n; k++ {
			for j := 0; j < n; j++ {
				angle := -2 * math.Pi * float64(k*j) / float64(n)
				want[k] += x[j] * complex(math.Cos(angle), math.Sin(angle))
			}
		}
		for k := 0; k < n; k++ {
			d := got[k] - want[k]
			if math.Hypot(real(d), imag(d)) > 1e-9*float64(n) {
				t.Fatalf("n=%d k=%d: got %v want %v", n, k, got[k], want[k])
			}
		}

		p.inverse(got)
		for i := 0; i < n; i++ {
			d := got[i] - x[i]
			if math.Hypot(real(d), imag(d)) > 1e-10*float64(n) {
				t.Fatalf("n=%d inverse[%d]: got %v want %v", n, i, got[i], x[i])
			}
		}
	}
}

func TestIdentityReconstruction(t *testing.T) {
	tests := []struct {
		name   string
		n, hop int
	}{
		{"even power of two", 64, 16},
		{"odd bluestein", 63, 15},
		{"tiny odd", 15, 3},
		{"tiny even", 16, 4},
		{"large odd", 255, 63},
	}

	const length = 701
	sine := make([]float64, length)
	for i := range sine {
		sine[i] = 0.7 * math.Sin(2*math.Pi*41*float64(i)/float64(length))
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := periodicHann(tc.n)

			impulses := [][]float64{make([]float64, length), make([]float64, length), make([]float64, length)}
			impulses[0][0] = 1
			impulses[1][length/2] = 1
			impulses[2][length-1] = 1
			for i, input := range impulses {
				out, err := ProcessAll(w, tc.hop, identityCallback, input)
				if err != nil {
					t.Fatalf("impulse %d: %v", i, err)
				}
				if len(out) != length {
					t.Fatalf("impulse %d: got %d samples, want %d", i, len(out), length)
				}
				if err := maxAbsDiff(out, input); err > 1e-10 {
					t.Fatalf("impulse %d: max error %.3g", i, err)
				}
			}

			out, err := ProcessAll(w, tc.hop, identityCallback, sine)
			if err != nil {
				t.Fatalf("sine: %v", err)
			}
			if len(out) != length {
				t.Fatalf("sine: got %d samples, want %d", len(out), length)
			}
			if err := maxAbsDiff(out, sine); err > 1e-10 {
				t.Fatalf("sine: max error %.3g", err)
			}
		})
	}
}

func TestRandomPCMChunkingMatchesWholeSignal(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	const length = 5003
	pcm := make([]float64, length)
	for i := range pcm {
		pcm[i] = float64(rng.Intn(65536)-32768) / 32768
	}

	w := periodicHann(256)
	const hop = 64
	batch, batchFrames := runChunked(t, w, hop, identityCallback, pcm, []int{length})

	chunks := make([]int, 0, 128)
	for total := 0; total < length; {
		size := 1 + rng.Intn(97)
		chunks = append(chunks, size)
		total += size
	}
	chunked, chunkedFrames := runChunked(t, w, hop, identityCallback, pcm, chunks)
	if len(chunked) != length {
		t.Fatalf("got %d samples after flush, want %d", len(chunked), length)
	}
	if batchFrames != chunkedFrames {
		t.Fatalf("callback frame count differs: batch=%d chunked=%d", batchFrames, chunkedFrames)
	}
	if d := maxAbsDiff(chunked, pcm); d > 1e-10 {
		t.Fatalf("chunked reconstruction max error %.3g", d)
	}
	if d := maxAbsDiff(batch, chunked); d > 1e-12 {
		t.Fatalf("batch and chunked differ by %.3g", d)
	}
}

func TestFlushReturnsExactlyInputSamples(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	w := periodicHann(32)
	for _, length := range []int{0, 1, 2, 7, 15, 16, 17, 31, 32, 33, 63, 64, 65, 999, 1000, 1001} {
		input := make([]float64, length)
		for i := range input {
			input[i] = rng.NormFloat64() * 0.1
		}
		out, err := ProcessAll(w, 5, identityCallback, input)
		if err != nil {
			t.Fatalf("length %d: %v", length, err)
		}
		if len(out) != length {
			t.Fatalf("length %d: got %d samples", length, len(out))
		}
		if length > 0 {
			if d := maxAbsDiff(out, input); d > 1e-10 {
				t.Fatalf("length %d: max error %.3g", length, d)
			}
		}
	}
}

func TestZeroWindowSumRejected(t *testing.T) {
	zero := make([]float64, 16)
	_, err := ProcessAll(zero, 4, nil, make([]float64, 32))
	if !errors.Is(err, ErrZeroWindowSum) {
		t.Fatalf("all-zero window: got %v, want ErrZeroWindowSum", err)
	}

	// hop > window leaves gaps between frames; effective samples in the gap
	// have zero accumulated window energy and must be rejected.
	ones := make([]float64, 4)
	for i := range ones {
		ones[i] = 1
	}
	_, err = ProcessAll(ones, 8, nil, make([]float64, 16))
	if !errors.Is(err, ErrZeroWindowSum) {
		t.Fatalf("gap: got %v, want ErrZeroWindowSum", err)
	}
}

func TestNonHermitianCallbackRejected(t *testing.T) {
	w := periodicHann(32)
	badPair := func(s []complex128) error {
		s[1] += complex(0.3, 0.1)
		return nil
	}
	if _, err := ProcessAll(w, 8, badPair, make([]float64, 128)); !errors.Is(err, ErrNonHermitian) {
		t.Fatalf("asymmetric bins: got %v, want ErrNonHermitian", err)
	}

	badDC := func(s []complex128) error {
		s[0] = complex(real(s[0]), 0.25)
		return nil
	}
	if _, err := ProcessAll(w, 8, badDC, make([]float64, 128)); !errors.Is(err, ErrNonHermitian) {
		t.Fatalf("imaginary DC: got %v, want ErrNonHermitian", err)
	}
}

func TestHermitianCallbackScalesSignal(t *testing.T) {
	const length = 1000
	input := make([]float64, length)
	for i := range input {
		input[i] = 0.8 * math.Sin(2*math.Pi*73*float64(i)/float64(length))
	}
	scale := func(s []complex128) error {
		for i := range s {
			s[i] *= 0.5
		}
		return nil
	}
	out, err := ProcessAll(periodicHann(128), 32, scale, input)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != length {
		t.Fatalf("got %d samples, want %d", len(out), length)
	}
	want := make([]float64, length)
	for i := range want {
		want[i] = input[i] * 0.5
	}
	if d := maxAbsDiff(out, want); d > 1e-10 {
		t.Fatalf("max error %.3g", d)
	}
}

func TestResetAndFlushLifecycle(t *testing.T) {
	w := periodicHann(16)
	a, err := New(w, 4, nil)
	if err != nil {
		t.Fatal(err)
	}
	input := make([]float64, 40)
	input[5] = 1
	out, err := a.Push(input)
	if err != nil {
		t.Fatal(err)
	}
	tail, err := a.Flush()
	if err != nil {
		t.Fatal(err)
	}
	all := append(out, tail...)
	if len(all) != len(input) {
		t.Fatalf("got %d samples, want %d", len(all), len(input))
	}
	if _, err := a.Push([]float64{1}); !errors.Is(err, ErrFlushed) {
		t.Fatalf("push after flush: got %v", err)
	}

	a.Reset()
	out, err = a.Push(input)
	if err != nil {
		t.Fatal(err)
	}
	tail, err = a.Flush()
	if err != nil {
		t.Fatal(err)
	}
	all2 := append(out, tail...)
	if len(all2) != len(input) {
		t.Fatalf("after reset got %d samples, want %d", len(all2), len(input))
	}
	if d := maxAbsDiff(all, all2); d != 0 {
		t.Fatalf("reset result differs by %.3g", d)
	}
}

func TestConfigValidationAndWindowCopy(t *testing.T) {
	if _, err := New(nil, 4, nil); !errors.Is(err, ErrInvalidWindow) {
		t.Fatalf("empty window: got %v", err)
	}
	if _, err := New([]float64{1, 2}, 0, nil); !errors.Is(err, ErrInvalidHop) {
		t.Fatalf("zero hop: got %v", err)
	}
	if _, err := New([]float64{1, math.NaN()}, 2, nil); !errors.Is(err, ErrInvalidWindow) {
		t.Fatalf("NaN window: got %v", err)
	}

	w := []float64{1, 2, 3, 4}
	a, err := New(w, 2, nil)
	if err != nil {
		t.Fatal(err)
	}
	w[0] = 99
	if a.window[0] != 1 {
		t.Fatalf("window was not copied")
	}
}
