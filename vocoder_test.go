package taskpkg

import (
	"io"
	"math"
	"math/rand"
	"sort"
	"testing"
)

const testRate = 16000

// tone returns length samples of a cosine at the given frequencies Hz.
func tone(length int, freq ...float64) []float64 {
	out := make([]float64, length)
	for i := range out {
		t := 2 * math.Pi * float64(i) / testRate
		for _, f := range freq {
			out[i] += 0.5 * math.Cos(2*math.Pi*f*float64(i)/testRate+t)
		}
	}
	return out
}

// peakFreq returns the single dominant spectral peak in Hz.
func peakFreq(t *testing.T, x []float64) float64 {
	t.Helper()
	return peakFreqs(t, x, 1)[0]
}

// peakFreqs returns the up-to count strongest distinct spectral peaks in Hz.
// Edge samples are dropped because the analysis window cannot fully overlap
// there; local maxima keep a strong tone's leakage sidelobes out of the list.
func peakFreqs(t *testing.T, x []float64, count int) []float64 {
	t.Helper()
	trim := len(x) / 8
	trimmed := x[trim : len(x)-trim]
	n := 1
	for n < len(trimmed) {
		n <<= 1
	}
	window := hannWindow(len(trimmed))
	spectrum := make([]complex128, n)
	for i, sample := range trimmed {
		spectrum[i] = complex(sample*window[i], 0)
	}
	fft(spectrum, false)

	type peak struct {
		bin int
		mag float64
	}
	var peaks []peak
	for k := 1; k < n/2; k++ {
		mag := math.Hypot(real(spectrum[k]), imag(spectrum[k]))
		if mag > math.Hypot(real(spectrum[k-1]), imag(spectrum[k-1])) &&
			mag > math.Hypot(real(spectrum[k+1]), imag(spectrum[k+1])) {
			peaks = append(peaks, peak{k, mag})
		}
	}
	if len(peaks) == 0 {
		t.Fatal("no spectral peak found")
	}
	sort.Slice(peaks, func(i, j int) bool { return peaks[i].mag > peaks[j].mag })
	if len(peaks) > count {
		peaks = peaks[:count]
	}
	out := make([]float64, len(peaks))
	for i, p := range peaks {
		out[i] = float64(p.bin) * testRate / float64(n)
	}
	return out
}

// drain collects the full vocoder output using reads of readSize.
func drain(t *testing.T, v *Vocoder, readSize int) []float64 {
	t.Helper()
	var out []float64
	buf := make([]float64, readSize)
	for {
		n, err := v.Read(buf)
		out = append(out, buf[:n]...)
		if err == io.EOF {
			return out
		}
		if err != nil {
			t.Fatalf("unexpected read error: %v", err)
		}
	}
}

// stretch runs the streaming vocoder over in using fixed-size writes.
func stretch(t *testing.T, fftLen, hop int, num, den int64, in []float64, writeSize int) []float64 {
	t.Helper()
	v, err := New(fftLen, hop, num, den)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for off := 0; off < len(in); off += writeSize {
		end := off + writeSize
		if end > len(in) {
			end = len(in)
		}
		if _, err := v.Write(in[off:end]); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	v.Flush()
	return drain(t, v, 137)
}

func TestFFTMatchesNaiveDFT(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for _, n := range []int{2, 4, 8, 16, 64, 256} {
		a := make([]complex128, n)
		ref := make([]complex128, n)
		for i := range a {
			a[i] = complex(rng.NormFloat64(), rng.NormFloat64())
			ref[i] = a[i]
		}

		fft(a, false)
		for k := range ref {
			var sum complex128
			for j, sample := range ref {
				angle := -2 * math.Pi * float64(k) * float64(j) / float64(n)
				sum += sample * complex(math.Cos(angle), math.Sin(angle))
			}
			if math.Abs(real(sum)-real(a[k])) > 1e-9 ||
				math.Abs(imag(sum)-imag(a[k])) > 1e-9 {
				t.Fatalf("n=%d bin=%d: fft %v != dft %v", n, k, a[k], sum)
			}
		}

		fft(a, true)
		for i := range ref {
			if math.Abs(real(ref[i])-real(a[i])) > 1e-10 ||
				math.Abs(imag(ref[i])-imag(a[i])) > 1e-10 {
				t.Fatalf("n=%d inverse mismatch at %d: %v != %v", n, i, a[i], ref[i])
			}
		}
	}
}

func TestSingleToneKeepsPitch(t *testing.T) {
	const fftLen, hop = 1024, 256
	const inputFreq = 440.0
	in := tone(testRate, inputFreq) // one second

	for _, tc := range []struct {
		name     string
		num, den int64
	}{
		{"double", 2, 1},
		{"halve", 1, 2},
		{"rational 3/2", 3, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := stretch(t, fftLen, hop, tc.num, tc.den, in, 500)

			want := int(math.Round(float64(len(in)) * float64(tc.num) / float64(tc.den)))
			if math.Abs(float64(len(out)-want)) > hop {
				t.Fatalf("duration = %d, want %d ± %d", len(out), want, hop)
			}

			// FFT-bin frequency resolution of the trimmed analysis window.
			tolerance := 1.5 * testRate / (float64(len(out)) * 0.75)
			if got := peakFreq(t, out); math.Abs(got-inputFreq) > tolerance {
				t.Fatalf("dominant frequency = %.2f Hz, want %.2f ± %.2f",
					got, inputFreq, tolerance)
			}
		})
	}
}

func TestDualToneKeepsBothPitches(t *testing.T) {
	const fftLen, hop = 2048, 512
	f1, f2 := 700.0, 2300.0
	in := tone(testRate, f1, f2)

	out := stretch(t, fftLen, hop, 3, 2, in, 1000)

	want := int(math.Round(float64(len(in)) * 1.5))
	if math.Abs(float64(len(out)-want)) > hop {
		t.Fatalf("duration = %d, want %d ± %d", len(out), want, hop)
	}

	tolerance := 1.5 * testRate / (float64(len(out)) * 0.75)
	peaks := peakFreqs(t, out, 2)
	if len(peaks) < 2 {
		t.Fatalf("expected two distinct peaks, got %d", len(peaks))
	}
	matches := func(peak float64) bool {
		return math.Abs(peak-f1) <= tolerance || math.Abs(peak-f2) <= tolerance
	}
	if !matches(peaks[0]) || !matches(peaks[1]) {
		t.Fatalf("peaks %v do not cover %.0f Hz and %.0f Hz (±%.2f)",
			peaks, f1, f2, tolerance)
	}
	if math.Abs(peaks[0]-peaks[1]) < tolerance {
		t.Fatalf("peaks collapsed: %v", peaks)
	}
}

func TestSilenceStaysSilent(t *testing.T) {
	const fftLen, hop = 512, 128
	in := make([]float64, testRate)

	for _, ratio := range [][2]int64{{2, 1}, {1, 2}, {3, 2}} {
		out := stretch(t, fftLen, hop, ratio[0], ratio[1], in, 333)
		want := int(math.Round(float64(len(in)) * float64(ratio[0]) / float64(ratio[1])))
		if math.Abs(float64(len(out)-want)) > hop {
			t.Fatalf("ratio %d/%d: duration = %d, want %d ± %d",
				ratio[0], ratio[1], len(out), want, hop)
		}
		for i, sample := range out {
			if math.IsNaN(sample) || math.IsInf(sample, 0) || sample != 0 {
				t.Fatalf("ratio %d/%d: invalid silence at %d: %v",
					ratio[0], ratio[1], i, sample)
			}
		}
	}
}

func TestChunkingIsEquivalent(t *testing.T) {
	const fftLen, hop = 1024, 256
	in := tone(testRate, 330.0)

	reference := stretch(t, fftLen, hop, 3, 2, in, len(in))
	for _, writeSize := range []int{1, 7, 255, 256, 1000, 4096} {
		got := stretch(t, fftLen, hop, 3, 2, in, writeSize)
		if len(got) != len(reference) {
			t.Fatalf("write size %d: length %d != reference %d",
				writeSize, len(got), len(reference))
		}
		for i := range reference {
			if math.Abs(got[i]-reference[i]) > 1e-12 {
				t.Fatalf("write size %d: sample %d differs: %.10f vs %.10f",
					writeSize, i, got[i], reference[i])
			}
		}
	}
}

func TestReadChunkingIsEquivalent(t *testing.T) {
	const fftLen, hop = 512, 128
	in := tone(testRate/2, 520.0)

	v, _ := New(fftLen, hop, 2, 1)
	_, _ = v.Write(in)
	v.Flush()
	reference := drain(t, v, len(in)+10)

	for _, readSize := range []int{1, 3, 64, 129, 999} {
		v, _ := New(fftLen, hop, 2, 1)
		_, _ = v.Write(in)
		v.Flush()
		got := drain(t, v, readSize)
		if len(got) != len(reference) {
			t.Fatalf("read size %d: length %d != %d", readSize, len(got), len(reference))
		}
		for i := range reference {
			if got[i] != reference[i] {
				t.Fatalf("read size %d: sample %d differs", readSize, i)
			}
		}
	}
}

func TestIdentityRatioReconstructs(t *testing.T) {
	const fftLen, hop = 1024, 256
	in := tone(testRate, 440.0)

	out := stretch(t, fftLen, hop, 1, 1, in, 409)
	if len(out) != len(in) {
		t.Fatalf("identity duration = %d, want %d", len(out), len(in))
	}

	// The first and last analysis windows cannot fully overlap, so the
	// Hann normalization is only meaningful in the interior; there the
	// phase vocoder must reconstruct essentially to machine precision.
	var errEnergy float64
	count := 0
	for i := fftLen; i < len(in)-fftLen; i++ {
		errEnergy += (in[i] - out[i]) * (in[i] - out[i])
		count++
	}
	if rms := math.Sqrt(errEnergy / float64(count)); rms > 1e-8 {
		t.Fatalf("identity reconstruction RMS error too large: %v", rms)
	}
}

func TestRatioChangesAtFrameBoundary(t *testing.T) {
	const fftLen, hop = 1024, 256
	in := tone(testRate, 440.0)

	run := func(writeSize int) []float64 {
		v, err := New(fftLen, hop, 2, 1)
		if err != nil {
			t.Fatal(err)
		}
		half := len(in) / 2
		if _, err := v.Write(in[:half]); err != nil {
			t.Fatal(err)
		}
		if err := v.SetRatio(1, 2); err != nil {
			t.Fatal(err)
		}
		for off := half; off < len(in); off += writeSize {
			end := off + writeSize
			if end > len(in) {
				end = len(in)
			}
			if _, err := v.Write(in[off:end]); err != nil {
				t.Fatal(err)
			}
		}
		v.Flush()
		return drain(t, v, 257)
	}

	reference := run(len(in))
	for _, size := range []int{1, 13, 257, 900} {
		got := run(size)
		if len(got) != len(reference) {
			t.Fatalf("size %d: length %d != %d", size, len(got), len(reference))
		}
		for i := range reference {
			if math.Abs(got[i]-reference[i]) > 1e-12 {
				t.Fatalf("size %d: sample %d differs", size, i)
			}
		}
	}

	tolerance := 1.5 * testRate / (float64(len(reference)) * 0.75)
	if got := peakFreq(t, reference); math.Abs(got-440.0) > tolerance {
		t.Fatalf("pitch drifted under ratio change: %.2f Hz", got)
	}
}

func TestSetRatioWaitsForFrameBoundary(t *testing.T) {
	const fftLen, hop = 512, 128
	in := tone(2048, 300.0)

	// A ratio scheduled before any full frame is available must only
	// apply to the first rendered frame; outputs produced while less than
	// fftLen samples are buffered stay empty regardless of chunking.
	v, err := New(fftLen, hop, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := v.Write(in[:fftLen-1]); n != fftLen-1 {
		t.Fatalf("partial write consumed %d samples", n)
	}
	if err := v.SetRatio(2, 1); err != nil {
		t.Fatal(err)
	}
	pre := make([]float64, 64)
	if n, _ := v.Read(pre); n != 0 {
		t.Fatalf("output appeared before a frame boundary: %d samples", n)
	}

	// Completing the frame consumes it under the pending 2/1 ratio.
	if _, err := v.Write(in[fftLen-1 : fftLen]); err != nil {
		t.Fatal(err)
	}

	// The first frame is anchored at position zero, so finalized output
	// appears once the second frame establishes a non-zero boundary.
	if _, err := v.Write(in[fftLen : fftLen+hop]); err != nil {
		t.Fatal(err)
	}
	if n, _ := v.Read(pre); n == 0 {
		t.Fatal("expected output after the second frame boundary")
	}
}

func TestNonPowerOfTwoHop(t *testing.T) {
	// hop = 300 does not divide fftLen; the rational accumulator must
	// still produce a ratio-accurate duration within one analysis hop.
	const fftLen, hop = 1024, 300
	in := make([]float64, testRate)
	for _, ratio := range [][2]int64{{2, 1}, {1, 2}, {5, 3}} {
		out := stretch(t, fftLen, hop, ratio[0], ratio[1], in, 499)
		want := int(math.Round(float64(len(in)) * float64(ratio[0]) / float64(ratio[1])))
		if math.Abs(float64(len(out)-want)) > hop {
			t.Fatalf("ratio %d/%d: duration = %d, want %d ± %d",
				ratio[0], ratio[1], len(out), want, hop)
		}
	}
}

func TestInvalidParameters(t *testing.T) {
	cases := []struct {
		fftLen, hop int
		num, den    int64
	}{
		{0, 1, 1, 1},
		{1000, 1, 1, 1}, // not a power of two
		{1024, 0, 1, 1},
		{1024, 1024, 1, 1},
		{1024, 256, 0, 1},
		{1024, 256, 1, 0},
		{1024, 256, -1, 1},
	}
	for _, tc := range cases {
		if _, err := New(tc.fftLen, tc.hop, tc.num, tc.den); err == nil {
			t.Fatalf("New(%+v) expected error", tc)
		}
	}

	v, err := New(1024, 256, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := v.SetRatio(0, 1); err == nil {
		t.Fatal("SetRatio(0,1) expected error")
	}
}

func TestProcessConvenienceAndEmptyInput(t *testing.T) {
	in := tone(4000, 660.0)
	out, err := Process(512, 128, 2, 1, in)
	if err != nil {
		t.Fatal(err)
	}
	want := int(math.Round(float64(len(in)) * 2))
	if math.Abs(float64(len(out)-want)) > 128 {
		t.Fatalf("Process duration = %d, want %d ± 128", len(out), want)
	}

	empty, err := Process(512, 128, 2, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(empty) != 0 {
		t.Fatalf("empty input produced %d samples", len(empty))
	}
}
