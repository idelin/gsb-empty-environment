package resample

import (
	"math"
	"math/big"
	"testing"
)

// oneShot is the exact reference model: the whole input stream is read in
// a single pass and every output is computed with exact rational arithmetic
// on the absolute time axis (positions in input-sample units).
func oneShot(samples []float64, segs []Segment, switches []int64) []*big.Rat {
	if len(samples) == 0 {
		return nil
	}
	n := int64(len(samples))
	sw := make([]*big.Rat, len(switches))
	for j, at := range switches {
		sw[j] = big.NewRat(at, 1)
	}
	pos := new(big.Rat)
	limit := big.NewRat(n, 1)
	var out []*big.Rat
	for pos.Cmp(limit) < 0 {
		out = append(out, interpAt(samples, pos))
		// step of the segment active at pos
		j := 0
		for j < len(sw) && pos.Cmp(sw[j]) >= 0 {
			j++
		}
		g := gcd(segs[j].Num, segs[j].Den)
		pos.Add(pos, big.NewRat(segs[j].Den/g, segs[j].Num/g))
	}
	return out
}

// interpAt evaluates the linear interpolation of samples at an exact
// rational position, clamping the window to the last sample.
func interpAt(samples []float64, pos *big.Rat) *big.Rat {
	n := int64(len(samples))
	i := floorRat(pos)
	j0, j1 := i, i+1
	if j0 > n-1 {
		j0 = n - 1
	}
	if j1 > n-1 {
		j1 = n - 1
	}
	frac := new(big.Rat).Sub(pos, big.NewRat(i, 1))
	x0 := new(big.Rat).SetFloat64(samples[j0])
	x1 := new(big.Rat).SetFloat64(samples[j1])
	// x0 + (x1-x0)*frac
	d := new(big.Rat).Sub(x1, x0)
	return d.Mul(d, frac).Add(d, x0)
}

// assertClose compares streamed float64 output against exact rationals.
func assertClose(t *testing.T, got []float64, want []*big.Rat) {
	t.Helper()
	if int64(len(got)) != int64(len(want)) {
		t.Fatalf("output count: got %d, want %d", len(got), len(want))
	}
	for k := range want {
		w, _ := want[k].Float64()
		tol := 1e-9 * (1 + math.Abs(w))
		if math.Abs(got[k]-w) > tol {
			t.Fatalf("output %d: got %v, want %v (exact %s)", k, got[k], w, want[k].RatString())
		}
	}
}

// feedChunks pushes samples through write in the given chunk sizes and
// collects all output, then appends the flush result.
func feedChunks(t *testing.T, write func([]float64) ([]float64, error), flush func() ([]float64, error), samples []float64, chunks []int) []float64 {
	t.Helper()
	var out []float64
	off := 0
	ci := 0
	for off < len(samples) {
		n := chunks[ci%len(chunks)]
		ci++
		if n > len(samples)-off {
			n = len(samples) - off
		}
		o, err := write(samples[off : off+n])
		if err != nil {
			t.Fatalf("write: %v", err)
		}
		out = append(out, o...)
		off += n
	}
	o, err := flush()
	if err != nil {
		t.Fatalf("flush: %v", err)
	}
	return append(out, o...)
}
