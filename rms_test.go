package rms

import (
	"fmt"
	"math"
	"math/big"
	"math/rand"
	"testing"
)

// refWindow is a deliberately simple reference implementation: it keeps
// every squared sample and recomputes the mean square from scratch.
type refWindow struct {
	max, win int
	squares  []*big.Rat
}

func newRefWindow(max int) *refWindow { return &refWindow{max: max, win: max} }

func (r *refWindow) add(x float64) {
	v := new(big.Rat).SetFloat64(x)
	r.squares = append(r.squares, new(big.Rat).Mul(v, v))
	if len(r.squares) > r.max {
		r.squares = r.squares[1:]
	}
}

func (r *refWindow) setWindow(n int) { r.win = n }

func (r *refWindow) meanSquare() *big.Rat {
	n := r.win
	if len(r.squares) < n {
		n = len(r.squares)
	}
	if n == 0 {
		return nil
	}
	s := new(big.Rat)
	for _, q := range r.squares[len(r.squares)-n:] {
		s.Add(s, q)
	}
	return s.Quo(s, big.NewRat(int64(n), 1))
}

func mustMeanSquare(t *testing.T, w *Window) *big.Rat {
	t.Helper()
	ms := w.MeanSquare()
	if ms == nil {
		t.Fatal("MeanSquare returned nil")
	}
	return ms
}

func checkEqualRef(t *testing.T, w *Window, r *refWindow) {
	t.Helper()
	got, want := w.MeanSquare(), r.meanSquare()
	if got == nil || want == nil {
		if got != want {
			t.Fatalf("MeanSquare nil mismatch: got %v want %v", got, want)
		}
		return
	}
	if got.Cmp(want) != 0 {
		t.Fatalf("MeanSquare = %s, want %s", got.RatString(), want.RatString())
	}
}

// checkBracket verifies 0 <= lo <= hi and lo^2 <= ms <= hi^2 exactly.
func checkBracket(t *testing.T, ms *big.Rat, lo, hi *big.Float) {
	t.Helper()
	if lo.Sign() < 0 {
		t.Fatalf("lo is negative: %s", lo.Text('e', 10))
	}
	if lo.Cmp(hi) > 0 {
		t.Fatalf("lo > hi: %s > %s", lo.Text('e', 10), hi.Text('e', 10))
	}
	lr, _ := lo.Rat(nil)
	hr, _ := hi.Rat(nil)
	l2 := new(big.Rat).Mul(lr, lr)
	h2 := new(big.Rat).Mul(hr, hr)
	if l2.Cmp(ms) > 0 {
		t.Fatalf("lo^2 = %s > mean square %s", l2.RatString(), ms.RatString())
	}
	if h2.Cmp(ms) < 0 {
		t.Fatalf("hi^2 = %s < mean square %s", h2.RatString(), ms.RatString())
	}
}

func TestExactFractionAndPartialWindow(t *testing.T) {
	w := New(10) // window larger than the number of samples
	w.Add(1)
	w.Add(2)
	// Only 2 samples present: normalize by 2, not by the window length.
	ms := mustMeanSquare(t, w)
	if ms.RatString() != "5/2" {
		t.Fatalf("MeanSquare = %s, want 5/2", ms.RatString())
	}
	if w.Count() != 2 {
		t.Fatalf("Count = %d, want 2", w.Count())
	}
	w.Add(3)
	if ms := mustMeanSquare(t, w); ms.RatString() != "14/3" {
		t.Fatalf("MeanSquare = %s, want 14/3", ms.RatString())
	}
}

func TestExactFractionNonTerminating(t *testing.T) {
	w := New(3)
	third := big.NewRat(1, 3)
	w.AddRat(third)
	w.AddRat(third)
	w.AddRat(third)
	// mean square = (3 * 1/9) / 3 = 1/9, an exact non-terminating fraction.
	if ms := mustMeanSquare(t, w); ms.RatString() != "1/9" {
		t.Fatalf("MeanSquare = %s, want 1/9", ms.RatString())
	}
}

func TestExtremeValuesNoOverflow(t *testing.T) {
	extremes := []float64{
		math.MaxFloat64,
		-math.MaxFloat64,
		1e308,
		-1e308,
		5e-324, // smallest positive denormal; x*x underflows to 0 in float64
		math.SmallestNonzeroFloat64,
	}
	for _, x := range extremes {
		naive := x * x
		if math.IsInf(naive, 0) || (naive == 0 && x != 0) {
			// Confirm the naive float64 path really is broken here.
			t.Logf("x=%g: naive float64 square = %g (broken)", x, naive)
		}
		w := New(4)
		w.Add(x)
		ms := mustMeanSquare(t, w)
		want := new(big.Rat).SetFloat64(x)
		want.Mul(want, want)
		if ms.Cmp(want) != 0 {
			t.Fatalf("x=%g: MeanSquare mismatch", x)
		}
		if x != 0 && ms.Sign() <= 0 {
			t.Fatalf("x=%g: MeanSquare = %s, want positive", x, ms.RatString())
		}
		lo, hi, ok := w.SqrtInterval(128)
		if !ok {
			t.Fatal("SqrtInterval not ok")
		}
		checkBracket(t, ms, lo, hi)
		// sqrt(x^2) = |x|; the interval must bracket |x|.
		abs := new(big.Float).SetPrec(256).SetFloat64(math.Abs(x))
		if lo.Cmp(abs) > 0 || hi.Cmp(abs) < 0 {
			t.Fatalf("x=%g: |x| not in [%s, %s]", x, lo.Text('e', 5), hi.Text('e', 5))
		}
	}
}

func TestMixedExtremesMeanSquare(t *testing.T) {
	w := New(2)
	w.Add(math.MaxFloat64)
	w.Add(5e-324)
	ms := mustMeanSquare(t, w)
	a := new(big.Rat).SetFloat64(math.MaxFloat64)
	a.Mul(a, a)
	b := new(big.Rat).SetFloat64(5e-324)
	b.Mul(b, b)
	want := new(big.Rat).Add(a, b)
	want.Quo(want, big.NewRat(2, 1))
	if ms.Cmp(want) != 0 {
		t.Fatalf("MeanSquare = %s, want %s", ms.RatString(), want.RatString())
	}
}

func TestShrinkExpandWindow(t *testing.T) {
	w := New(8)
	r := newRefWindow(8)
	for i := 1; i <= 8; i++ {
		w.Add(float64(i))
		r.add(float64(i))
	}
	checkEqualRef(t, w, r)

	// Shrink: statistics cover only the most recent 3 samples.
	w.SetWindow(3)
	r.setWindow(3)
	checkEqualRef(t, w, r)
	if ms := mustMeanSquare(t, w); ms.RatString() != "77/3" { // (36+49+64)/3... 6^2+7^2+8^2 = 149
		t.Logf("shrink mean square = %s", ms.RatString())
	}
	want := new(big.Rat).Quo(big.NewRat(36+49+64, 1), big.NewRat(3, 1))
	if ms := mustMeanSquare(t, w); ms.Cmp(want) != 0 {
		t.Fatalf("after shrink MeanSquare = %s, want %s", ms.RatString(), want.RatString())
	}

	// Expand again: retained history (up to maxWindow) comes back.
	w.SetWindow(8)
	r.setWindow(8)
	checkEqualRef(t, w, r)
	wantSum := int64(0)
	for i := 1; i <= 8; i++ {
		wantSum += int64(i * i)
	}
	want = new(big.Rat).Quo(big.NewRat(wantSum, 1), big.NewRat(8, 1))
	if ms := mustMeanSquare(t, w); ms.Cmp(want) != 0 {
		t.Fatalf("after expand MeanSquare = %s, want %s", ms.RatString(), want.RatString())
	}

	// Shrink, push more samples (evicting old history beyond maxWindow),
	// then expand: evicted samples must stay gone.
	w.SetWindow(2)
	r.setWindow(2)
	for i := 9; i <= 20; i++ {
		w.Add(float64(i))
		r.add(float64(i))
	}
	w.SetWindow(8)
	r.setWindow(8)
	checkEqualRef(t, w, r)
	wantSum = 0
	for i := 13; i <= 20; i++ {
		wantSum += int64(i * i)
	}
	want = new(big.Rat).Quo(big.NewRat(wantSum, 1), big.NewRat(8, 1))
	if ms := mustMeanSquare(t, w); ms.Cmp(want) != 0 {
		t.Fatalf("after churn MeanSquare = %s, want %s", ms.RatString(), want.RatString())
	}
}

func TestChunkBoundaries(t *testing.T) {
	const total = 200
	data := make([]float64, total)
	rng := rand.New(rand.NewSource(42))
	for i := range data {
		data[i] = rng.NormFloat64() * 1e6
	}
	// Reference: one sample at a time.
	single := New(16)
	for _, x := range data {
		single.Add(x)
	}
	// Same data fed in chunks of every size that stresses boundaries
	// around the 16-sample window.
	for _, chunk := range []int{1, 2, 3, 5, 7, 15, 16, 17, 31, 64, total} {
		w := New(16)
		for i := 0; i < total; i += chunk {
			end := i + chunk
			if end > total {
				end = total
			}
			w.AddAll(data[i:end]...)
		}
		got, want := mustMeanSquare(t, w), mustMeanSquare(t, single)
		if got.Cmp(want) != 0 {
			t.Fatalf("chunk=%d: MeanSquare = %s, want %s", chunk, got.RatString(), want.RatString())
		}
		lo1, hi1, _ := w.SqrtInterval(128)
		lo2, hi2, _ := single.SqrtInterval(128)
		if lo1.Cmp(lo2) != 0 || hi1.Cmp(hi2) != 0 {
			t.Fatalf("chunk=%d: sqrt interval differs", chunk)
		}
	}
}

func TestWraparoundAndResizeFuzz(t *testing.T) {
	const maxWin = 37
	w := New(maxWin)
	r := newRefWindow(maxWin)
	rng := rand.New(rand.NewSource(1))
	magnitudes := []float64{1e-300, 1e-3, 1, 1e3, 1e150, 1e308}
	for step := 0; step < 3000; step++ {
		if rng.Intn(10) < 7 {
			x := magnitudes[rng.Intn(len(magnitudes))] * (rng.Float64() - 0.5)
			w.Add(x)
			r.add(x)
		} else {
			n := 1 + rng.Intn(maxWin)
			w.SetWindow(n)
			r.setWindow(n)
		}
		checkEqualRef(t, w, r)
		if w.Count() != minInt(r.win, len(r.squares)) {
			t.Fatalf("step %d: Count = %d, want %d", step, w.Count(), minInt(r.win, len(r.squares)))
		}
		if step%100 == 0 {
			ms := mustMeanSquare(t, w)
			lo, hi, ok := w.SqrtInterval(96)
			if !ok {
				t.Fatal("SqrtInterval not ok")
			}
			checkBracket(t, ms, lo, hi)
		}
	}
}

func TestSqrtIntervalPrecision(t *testing.T) {
	w := New(4)
	w.AddAll(1, 2, 3, 4) // mean square = 30/4 = 15/2
	ms := mustMeanSquare(t, w)
	if ms.RatString() != "15/2" {
		t.Fatalf("MeanSquare = %s, want 15/2", ms.RatString())
	}
	width := func(prec uint) *big.Float {
		lo, hi, ok := w.SqrtInterval(prec)
		if !ok {
			t.Fatal("SqrtInterval not ok")
		}
		checkBracket(t, ms, lo, hi)
		d := new(big.Float).SetPrec(256).Sub(hi, lo)
		return d
	}
	narrow, wide := width(256), width(16)
	if narrow.Cmp(wide) >= 0 {
		t.Fatalf("higher precision did not shrink interval: %s vs %s",
			narrow.Text('e', 5), wide.Text('e', 5))
	}
	// sqrt(15/2) is irrational; a 256-bit interval must be tiny.
	limit := new(big.Float).SetPrec(256).SetFloat64(1e-70)
	if narrow.Cmp(limit) >= 0 {
		t.Fatalf("256-bit interval too wide: %s", narrow.Text('e', 5))
	}
}

func TestEmptyWindow(t *testing.T) {
	w := New(4)
	if ms := w.MeanSquare(); ms != nil {
		t.Fatalf("MeanSquare = %v, want nil", ms)
	}
	if _, _, ok := w.SqrtInterval(64); ok {
		t.Fatal("SqrtInterval ok on empty window")
	}
	if w.Count() != 0 || w.Len() != 0 {
		t.Fatal("empty window has nonzero count")
	}
}

func TestPanics(t *testing.T) {
	mustPanic := func(name string, f func()) {
		defer func() {
			if recover() == nil {
				t.Fatalf("%s did not panic", name)
			}
		}()
		f()
	}
	mustPanic("New(0)", func() { New(0) })
	mustPanic("SetWindow(0)", func() { New(4).SetWindow(0) })
	mustPanic("SetWindow(5)>max", func() { New(4).SetWindow(5) })
	mustPanic("Add(NaN)", func() { New(4).Add(math.NaN()) })
	mustPanic("Add(+Inf)", func() { New(4).Add(math.Inf(1)) })
	mustPanic("SqrtInterval(0)", func() {
		w := New(2)
		w.Add(1)
		w.SqrtInterval(0)
	})
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func ExampleWindow() {
	w := New(4)
	w.AddAll(1, 2, 3, 4, 5) // window keeps the last 4 samples
	ms := w.MeanSquare()
	lo, hi, _ := w.SqrtInterval(53)
	fmt.Printf("mean square = %s\n", ms.RatString())
	fmt.Printf("rms in [%s, %s]\n", lo.Text('f', 16), hi.Text('f', 16))
	// Output:
	// mean square = 27/2
	// rms in [3.6742346141747668, 3.6742346141747673]
}
