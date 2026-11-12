package taskpkg

import (
	"math"
	"math/rand"
	"testing"
)

const tol = 1e-4

// directConv is the reference full time-domain convolution.
func directConv(x, h []float64) []float64 {
	if len(x) == 0 || len(h) == 0 {
		return nil
	}
	y := make([]float64, len(x)+len(h)-1)
	for i, xv := range x {
		if xv == 0 {
			continue
		}
		for j, hv := range h {
			y[i+j] += xv * hv
		}
	}
	return y
}

func randSlice(r *rand.Rand, n int) []float64 {
	s := make([]float64, n)
	for i := range s {
		s[i] = r.Float64()*2 - 1
	}
	return s
}

func toF32(s []float64) []float32 {
	o := make([]float32, len(s))
	for i, v := range s {
		o[i] = float32(v)
	}
	return o
}

func toF64(s []float32) []float64 {
	o := make([]float64, len(s))
	for i, v := range s {
		o[i] = float64(v)
	}
	return o
}

func maxAbsDiff(a, b []float64) float64 {
	m := 0.0
	for i := range a {
		if d := math.Abs(a[i] - b[i]); d > m {
			m = d
		}
	}
	return m
}

func checkClose(t *testing.T, got, want []float64, context string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: length = %d, want %d", context, len(got), len(want))
	}
	if d := maxAbsDiff(got, want); d > tol {
		n := 0
		for i := range got {
			if math.Abs(got[i]-want[i]) == d {
				n = i
				break
			}
		}
		t.Fatalf("%s: max abs diff %g at sample %d (got %v, want %v)", context, d, n, got[n], want[n])
	}
}

// feed pushes sig through c in chunks of the given sizes (cycled) and
// returns all Process output plus the flushed tail.
func feed(c *Convolver, sig []float64, chunks ...int) []float64 {
	var got []float64
	fed, ci := 0, 0
	for fed < len(sig) {
		n := chunks[ci%len(chunks)]
		ci++
		if n > len(sig)-fed {
			n = len(sig) - fed
		}
		got = append(got, toF64(c.Process(toF32(sig[fed:fed+n])))...)
		fed += n
	}
	return append(got, toF64(c.Flush())...)
}

func TestProcessMatchesDirectConvolution(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	const block = 64
	ir := randSlice(rng, 300) // not a multiple of the block size
	sig := randSlice(rng, 1000)
	c := NewConvolver(block, toF32(ir))

	var got []float64
	fed := 0
	for i, n := range []int{1, 63, 64, 65, 100, 7, 250, 500} {
		_ = i
		if n > len(sig)-fed {
			n = len(sig) - fed
		}
		got = append(got, toF64(c.Process(toF32(sig[fed:fed+n])))...)
		fed += n
	}
	if fed < len(sig) {
		got = append(got, toF64(c.Process(toF32(sig[fed:])))...)
	}
	// Before Flush, exactly the complete blocks may have been emitted.
	if want := len(sig) / block * block; len(got) != want {
		t.Fatalf("pre-flush output = %d samples, want %d", len(got), want)
	}
	got = append(got, toF64(c.Flush())...)

	want := directConv(sig, ir)
	if len(got) != len(want) {
		t.Fatalf("total output = %d samples, want %d (signal %d + tail %d)",
			len(got), len(want), len(sig), len(ir)-1)
	}
	checkClose(t, got, want, "partitioned vs direct convolution")
}

func TestPartialBlockInputNotDropped(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	const block = 64
	ir := randSlice(rng, 50)
	sig := randSlice(rng, 10) // less than one block
	c := NewConvolver(block, toF32(ir))

	if out := c.Process(toF32(sig)); len(out) != 0 {
		t.Fatalf("Process with partial block returned %d samples, want 0", len(out))
	}
	got := toF64(c.Flush())
	checkClose(t, got, directConv(sig, ir), "partial-block flush")
}

func TestImpulseResponseAndTailLength(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	const block = 32
	ir := randSlice(rng, 100)
	c := NewConvolver(block, toF32(ir))
	if tl := c.TailLength(); tl != len(ir)-1 {
		t.Fatalf("TailLength = %d, want %d", tl, len(ir)-1)
	}
	got := feed(c, []float64{1}, 1)
	// An impulse must reproduce the IR exactly: length == len(ir).
	checkClose(t, got, ir, "impulse response")
}

func TestCrossfadeUpdate(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	const block = 64
	const fade = 100
	const boundary = 3 * block
	ir1 := randSlice(rng, 200)
	ir2 := randSlice(rng, 150)
	sig := randSlice(rng, 800)

	c := NewConvolver(block, toF32(ir1))
	var got []float64
	got = append(got, toF64(c.Process(toF32(sig[:boundary])))...)
	c.SetIR(toF32(ir2), fade)
	got = append(got, feed(c, sig[boundary:], 37, 65)...)

	conv1 := directConv(sig, ir1)
	conv2 := directConv(sig, ir2)
	// The fade ends long before the input stops, so the final tail is the
	// new IR's tail only.
	total := len(sig) + len(ir2) - 1
	want := make([]float64, total)
	for n := range want {
		var a, b float64
		if n < len(conv1) {
			a = conv1[n]
		}
		if n < len(conv2) {
			b = conv2[n]
		}
		switch {
		case n < boundary:
			want[n] = a
		case n < boundary+fade:
			g := float64(n-boundary) / float64(fade-1)
			want[n] = a*(1-g) + b*g
		default:
			want[n] = b
		}
	}
	checkClose(t, got, want, "crossfade update")

	// Inside the fade region the output must be a genuine mix: it differs
	// from both pure filters, i.e. the old tail is still present.
	mid := boundary + fade/2
	if math.Abs(got[mid]-conv2[mid]) < 1e-3 {
		t.Fatalf("mid-fade sample %d equals pure new filter (%v); old tail missing", mid, got[mid])
	}
	if math.Abs(got[mid]-conv1[mid]) < 1e-3 {
		t.Fatalf("mid-fade sample %d equals pure old filter (%v); new filter missing", mid, got[mid])
	}
}

func TestFlushDuringCrossfade(t *testing.T) {
	rng := rand.New(rand.NewSource(11))
	const block = 16
	const fade = 128 // longer than the remaining input, so Flush runs mid-fade
	const boundary = 2 * block
	ir1 := randSlice(rng, 50)
	ir2 := randSlice(rng, 40)
	sig := randSlice(rng, 100)

	c := NewConvolver(block, toF32(ir1))
	got := toF64(c.Process(toF32(sig[:boundary])))
	c.SetIR(toF32(ir2), fade)
	got = append(got, feed(c, sig[boundary:], 100)...)

	conv1 := directConv(sig, ir1)
	conv2 := directConv(sig, ir2)
	// Flush happens while the fade is still running (the fade region
	// [32,160) extends past the end of input at 100), so the emitted tail
	// extends to the longer of the two IRs.
	total := len(sig) + max(len(ir1), len(ir2)) - 1
	want := make([]float64, total)
	for n := range want {
		var a, b float64
		if n < len(conv1) {
			a = conv1[n]
		}
		if n < len(conv2) {
			b = conv2[n]
		}
		switch {
		case n < boundary:
			want[n] = a
		case n < boundary+fade:
			g := float64(n-boundary) / float64(fade-1)
			want[n] = a*(1-g) + b*g
		default:
			want[n] = b
		}
	}
	checkClose(t, got, want, "flush during crossfade")
}

func TestHardSwitchAtBlockBoundary(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	const block = 32
	const boundary = 2 * block
	ir1 := randSlice(rng, 70)
	ir2 := randSlice(rng, 45)
	sig := randSlice(rng, 300)

	c := NewConvolver(block, toF32(ir1))
	var got []float64
	got = append(got, toF64(c.Process(toF32(sig[:boundary])))...)
	c.SetIR(toF32(ir2), 0) // immediate switch, old tail explicitly cut
	got = append(got, feed(c, sig[boundary:], 17)...)

	conv1 := directConv(sig, ir1)
	conv2 := directConv(sig, ir2)
	want := make([]float64, len(sig)+len(ir2)-1)
	for n := range want {
		if n < boundary {
			want[n] = conv1[n]
		} else if n < len(conv2) {
			want[n] = conv2[n]
		}
	}
	checkClose(t, got, want, "hard switch")
}

func TestUpdateDoesNotClearOldTail(t *testing.T) {
	rng := rand.New(rand.NewSource(5))
	const block = 32
	const fade = 96
	ir1 := randSlice(rng, 80)
	ir2 := randSlice(rng, 80)
	// Input stops right at the update boundary; everything after it is the
	// old IR's tail fading out against the new IR's (zero) output.
	sig := randSlice(rng, 2*block)

	c := NewConvolver(block, toF32(ir1))
	got := toF64(c.Process(toF32(sig)))
	c.SetIR(toF32(ir2), fade)
	got = append(got, toF64(c.Flush())...)

	// The new IR immediately sees the preserved input history, so after
	// the boundary the output is the crossfade between both full
	// convolutions; the old tail rings out through the fade instead of
	// being cleared.
	conv1 := directConv(sig, ir1)
	conv2 := directConv(sig, ir2)
	total := len(sig) + len(ir1) - 1
	want := make([]float64, total)
	for n := range want {
		var a, b float64
		if n < len(conv1) {
			a = conv1[n]
		}
		if n < len(conv2) {
			b = conv2[n]
		}
		switch {
		case n < 2*block:
			want[n] = a
		case n < 2*block+fade:
			g := float64(n-2*block) / float64(fade-1)
			want[n] = a*(1-g) + b*g
		default:
			want[n] = b
		}
	}
	checkClose(t, got, want, "old tail after update")
	// Sanity: the tail right after the boundary must be audibly non-zero,
	// proving it was not cleared by SetIR.
	if math.Abs(got[2*block]) < 1e-3 {
		t.Fatalf("old tail appears cleared at the update boundary: %v", got[2*block])
	}
}

func TestReset(t *testing.T) {
	rng := rand.New(rand.NewSource(9))
	const block = 32
	ir := randSlice(rng, 100)
	c := NewConvolver(block, toF32(ir))
	_ = feed(c, randSlice(rng, 500), 64)
	c.Reset()
	if tl := c.TailLength(); tl != len(ir)-1 {
		t.Fatalf("TailLength after Reset = %d, want %d", tl, len(ir)-1)
	}
	got := feed(c, []float64{1}, 1)
	checkClose(t, got, ir, "impulse response after Reset")
}

func TestEmptyIR(t *testing.T) {
	c := NewConvolver(16, nil)
	sig := randSlice(rand.New(rand.NewSource(4)), 40)
	got := feed(c, sig, 7)
	if len(got) != len(sig) {
		t.Fatalf("empty IR output = %d samples, want %d", len(got), len(sig))
	}
	for i, v := range got {
		if v != 0 {
			t.Fatalf("empty IR output[%d] = %v, want 0", i, v)
		}
	}
}
