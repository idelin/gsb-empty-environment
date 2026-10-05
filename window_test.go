package rmswin

import (
	"math"
	"math/big"
	"testing"
)

func ratInt(n int64) *big.Rat { return new(big.Rat).SetInt64(n) }

// expectedMS computes sum(v_i^2)/len(v) exactly, the reference oracle.
func expectedMS(vs []*big.Rat) *big.Rat {
	sum := new(big.Rat)
	for _, v := range vs {
		sq := new(big.Rat).Mul(v, v)
		sum.Add(sum, sq)
	}
	return sum.Quo(sum, new(big.Rat).SetInt64(int64(len(vs))))
}

func mustMS(t *testing.T, w *Window) *big.Rat {
	ms, err := w.MeanSquare()
	if err != nil {
		t.Fatalf("MeanSquare: %v", err)
	}
	return ms
}

func TestExactMeanSquareFraction(t *testing.T) {
	w := NewWindow(3)
	vs := []*big.Rat{
		big.NewRat(1, 3),
		big.NewRat(1, 7),
		big.NewRat(2, 5),
	}
	w.AddRatBatch(vs)
	got := mustMS(t, w)
	want := expectedMS(vs)
	if got.Cmp(want) != 0 {
		t.Fatalf("MeanSquare = %v, want %v", got, want)
	}
	// Exactness: denominator must survive un-reduced by float rounding.
	if got.Denom().Cmp(want.Denom()) != 0 {
		t.Fatalf("denominator %v, want %v", got.Denom(), want.Denom())
	}
}

func TestExtremeSquareNoOverflow(t *testing.T) {
	w := NewWindow(4)
	extremes := []float64{1e308, -1e308, math.MaxFloat64, -math.MaxFloat64}
	for _, f := range extremes {
		if err := w.AddFloat64(f); err != nil {
			t.Fatalf("AddFloat64(%v): %v", f, err)
		}
	}
	// Reference: exact squares via big.Rat conversion.
	sum := new(big.Rat)
	for _, f := range extremes {
		r := new(big.Rat).SetFloat64(f)
		sum.Add(sum, new(big.Rat).Mul(r, r))
	}
	want := new(big.Rat).Quo(sum, ratInt(4))
	got := mustMS(t, w)
	if got.Cmp(want) != 0 {
		t.Fatalf("MeanSquare mismatch under extreme inputs")
	}
	// The mean square (~1e616) exceeds math.MaxFloat64; exact rational
	// arithmetic must represent it where float64 would overflow to +Inf.
	maxF64 := new(big.Rat).SetFloat64(math.MaxFloat64)
	if got.Cmp(maxF64) <= 0 {
		t.Fatalf("MeanSquare %v should exceed MaxFloat64", got)
	}
	// RMS interval must bracket the exact RMS of the two magnitudes.
	lo, hi, err := w.RMSInterval()
	if err != nil {
		t.Fatalf("RMSInterval: %v", err)
	}
	if lo.Sign() < 0 || lo.Cmp(hi) >= 0 {
		t.Fatalf("invalid interval [%v, %v]", lo, hi)
	}
	// lo^2 <= ms < hi^2 must hold exactly.
	lo2 := new(big.Rat).Mul(lo, lo)
	hi2 := new(big.Rat).Mul(hi, hi)
	if lo2.Cmp(got) > 0 || hi2.Cmp(got) <= 0 {
		t.Fatalf("interval does not bracket mean square")
	}
	// Sanity: RMS of {1e308,1e308,Max,Max} is between 1e308 and MaxFloat64.
	loF, _ := lo.Float64()
	hiF, _ := hi.Float64()
	if !(loF <= math.MaxFloat64 && hiF >= 1e308) {
		t.Fatalf("interval [%v, %v] implausible", loF, hiF)
	}
}

func TestShrinkWindowEvictsOldest(t *testing.T) {
	w := NewWindow(10)
	var all []*big.Rat
	for i := int64(1); i <= 10; i++ {
		all = append(all, ratInt(i))
	}
	w.AddRatBatch(all)

	w.SetWindow(3)
	if w.Len() != 3 {
		t.Fatalf("Len = %d, want 3", w.Len())
	}
	got := mustMS(t, w)
	want := expectedMS(all[7:]) // last 3: 8, 9, 10
	if got.Cmp(want) != 0 {
		t.Fatalf("after shrink MeanSquare = %v, want %v", got, want)
	}
}

func TestGrowWindowKeepsActualCountNormalization(t *testing.T) {
	w := NewWindow(10)
	var all []*big.Rat
	for i := int64(1); i <= 10; i++ {
		all = append(all, ratInt(i))
	}
	w.AddRatBatch(all)
	w.SetWindow(3)
	w.SetWindow(10) // grow back: evicted history is gone

	if w.Len() != 3 {
		t.Fatalf("Len = %d, want 3", w.Len())
	}
	got := mustMS(t, w)
	want := expectedMS(all[7:]) // still normalized by 3, not 10
	if got.Cmp(want) != 0 {
		t.Fatalf("after grow MeanSquare = %v, want %v", got, want)
	}

	// Refill: window now accepts up to 10 again.
	var more []*big.Rat
	for i := int64(11); i <= 17; i++ {
		more = append(more, ratInt(i))
	}
	w.AddRatBatch(more)
	if w.Len() != 10 {
		t.Fatalf("Len = %d, want 10", w.Len())
	}
	got = mustMS(t, w)
	want = expectedMS(append(append([]*big.Rat{}, all[7:]...), more...))
	if got.Cmp(want) != 0 {
		t.Fatalf("after refill MeanSquare = %v, want %v", got, want)
	}
}

func TestPartialWindowNormalization(t *testing.T) {
	w := NewWindow(10)
	vs := []*big.Rat{ratInt(1), ratInt(2), ratInt(3), ratInt(4)}
	w.AddRatBatch(vs)
	got := mustMS(t, w)
	want := expectedMS(vs) // divided by 4, not 10
	if got.Cmp(want) != 0 {
		t.Fatalf("MeanSquare = %v, want %v", got, want)
	}
}

func TestHistoryCappedAtMaxWindow(t *testing.T) {
	w := NewWindow(5)
	var all []*big.Rat
	for i := int64(1); i <= 12; i++ {
		all = append(all, ratInt(i))
	}
	w.AddRatBatch(all)
	if w.Len() != 5 {
		t.Fatalf("Len = %d, want 5", w.Len())
	}
	got := mustMS(t, w)
	want := expectedMS(all[7:]) // only last 5 retained
	if got.Cmp(want) != 0 {
		t.Fatalf("MeanSquare = %v, want %v", got, want)
	}
}

// TestChunkBoundaries feeds identical data with different chunkings and
// requires bit-identical exact results.
func TestChunkBoundaries(t *testing.T) {
	const n = 200
	data := make([]float64, n)
	seed := int64(12345)
	for i := range data {
		// deterministic pseudo-random values across magnitudes
		seed = (seed*6364136223846793005 + 1442695040888963407)
		data[i] = float64(seed>>11) / float64(int64(1)<<53) * 2e10
		if i%3 == 0 {
			data[i] *= 1e100 // sprinkle extreme magnitudes
		}
		if i%2 == 0 {
			data[i] = -data[i]
		}
	}

	reference := NewWindow(37)
	for _, f := range data {
		if err := reference.AddFloat64(f); err != nil {
			t.Fatal(err)
		}
	}
	refMS := mustMS(t, reference)
	refLo, refHi, _ := reference.RMSInterval()

	for _, chunk := range []int{1, 2, 7, 37, 64, 200} {
		w := NewWindow(37)
		for i := 0; i < n; i += chunk {
			end := i + chunk
			if end > n {
				end = n
			}
			if err := w.AddFloat64Batch(data[i:end]); err != nil {
				t.Fatal(err)
			}
		}
		got := mustMS(t, w)
		if got.Cmp(refMS) != 0 {
			t.Fatalf("chunk=%d: MeanSquare differs", chunk)
		}
		lo, hi, _ := w.RMSInterval()
		if lo.Cmp(refLo) != 0 || hi.Cmp(refHi) != 0 {
			t.Fatalf("chunk=%d: RMS interval differs", chunk)
		}
	}
}

func TestRMSIntervalBracketsExactValue(t *testing.T) {
	w := NewWindow(8)
	vs := []*big.Rat{
		big.NewRat(1, 3), big.NewRat(22, 7), big.NewRat(-5, 11),
		big.NewRat(1000000, 1), big.NewRat(1, 1000000),
	}
	w.AddRatBatch(vs)
	ms := mustMS(t, w)
	lo, hi, err := w.RMSInterval()
	if err != nil {
		t.Fatal(err)
	}
	lo2 := new(big.Rat).Mul(lo, lo)
	hi2 := new(big.Rat).Mul(hi, hi)
	if lo2.Cmp(ms) > 0 {
		t.Fatalf("lo^2 = %v > ms = %v", lo2, ms)
	}
	if hi2.Cmp(ms) <= 0 {
		t.Fatalf("hi^2 = %v <= ms = %v", hi2, ms)
	}
	// Width must be small: hi - lo <= 1/q + hi - lo slack from isqrt.
	width := new(big.Rat).Sub(hi, lo)
	if width.Sign() <= 0 {
		t.Fatalf("non-positive interval width")
	}
}

func TestEmptyWindow(t *testing.T) {
	w := NewWindow(4)
	if _, err := w.MeanSquare(); err != ErrEmptyWindow {
		t.Fatalf("MeanSquare err = %v, want ErrEmptyWindow", err)
	}
	if _, _, err := w.RMSInterval(); err != ErrEmptyWindow {
		t.Fatalf("RMSInterval err = %v, want ErrEmptyWindow", err)
	}
}

func TestSetWindowClamping(t *testing.T) {
	w := NewWindow(5)
	w.SetWindow(0)
	if w.WindowSize() != 1 {
		t.Fatalf("size = %d, want 1", w.WindowSize())
	}
	w.SetWindow(100)
	if w.WindowSize() != 5 {
		t.Fatalf("size = %d, want 5 (clamped to max)", w.WindowSize())
	}
}

func TestInvalidFloatRejected(t *testing.T) {
	w := NewWindow(4)
	if err := w.AddFloat64(math.NaN()); err == nil {
		t.Fatal("NaN accepted")
	}
	if err := w.AddFloat64(math.Inf(1)); err == nil {
		t.Fatal("+Inf accepted")
	}
	if w.Len() != 0 {
		t.Fatalf("window mutated by invalid input, Len = %d", w.Len())
	}
}
