package resample

import (
	"math/rand"
	"testing"
)

func randSamples(rng *rand.Rand, n int) []float64 {
	x := make([]float64, n)
	for i := range x {
		x[i] = rng.NormFloat64()
	}
	return x
}

// streamFixed feeds x through r in the given chunk sizes and returns the
// concatenation of all Write outputs plus the Flush tail.
func streamFixed(r *Resampler, x []float64, chunks []int) []float64 {
	var out []float64
	pos := 0
	for _, c := range chunks {
		if pos+c > len(x) {
			c = len(x) - pos
		}
		out = append(out, r.Write(x[pos:pos+c])...)
		pos += c
	}
	out = append(out, r.Write(x[pos:])...)
	out = append(out, r.Flush()...)
	return out
}

func randomChunks(rng *rand.Rand, n int) []int {
	var chunks []int
	left := n
	for left > 0 {
		c := 1 + rng.Intn(left)
		chunks = append(chunks, c)
		left -= c
	}
	return chunks
}

func equalFloats(a, b []float64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Arbitrary chunkings of the same stream must reproduce the one-shot exact
// rational model bit-for-bit, including the output count.
func TestChunkedMatchesOneShot(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	steps := [][2]int64{{1, 1}, {3, 2}, {2, 3}, {1, 4}, {4, 1}, {7, 3}, {3, 7}, {160, 147}, {147, 160}}
	ns := []int{0, 1, 2, 3, 5, 17, 100, 257}
	for _, st := range steps {
		for _, n := range ns {
			x := randSamples(rng, n)
			want := ResampleAll(x, st[0], st[1])
			for trial := 0; trial < 5; trial++ {
				r := New(st[0], st[1])
				got := streamFixed(r, x, randomChunks(rng, n))
				if !equalFloats(got, want) {
					t.Fatalf("step %d/%d n=%d trial=%d: chunked output differs from one-shot model (got %d samples, want %d)",
						st[0], st[1], n, trial, len(got), len(want))
				}
			}
		}
	}
}

// The one-shot model's output count must match the exact rational formula
// ceil(N*stepDen/stepNum), and streaming must agree.
func TestOutputCount(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for _, st := range [][2]int64{{1, 1}, {3, 2}, {2, 3}, {1, 4}, {5, 1}, {7, 3}} {
		for n := int64(1); n <= 300; n++ {
			x := randSamples(rng, int(n))
			want := (n*st[1] + st[0] - 1) / st[0]
			if got := int64(len(ResampleAll(x, st[0], st[1]))); got != want {
				t.Fatalf("step %d/%d n=%d: one-shot count %d, want %d", st[0], st[1], n, got, want)
			}
			r := New(st[0], st[1])
			got := int64(len(streamFixed(r, x, randomChunks(rng, int(n)))))
			if got != want {
				t.Fatalf("step %d/%d n=%d: streamed count %d, want %d", st[0], st[1], n, got, want)
			}
		}
	}
}

func TestEmptyInput(t *testing.T) {
	r := New(3, 2)
	if out := r.Flush(); out != nil {
		t.Fatalf("flush with no input: got %v, want nil", out)
	}
	r = New(3, 2)
	if out := r.Write(nil); out != nil {
		t.Fatalf("write of empty chunk: got %v, want nil", out)
	}
	if out := r.Flush(); out != nil {
		t.Fatalf("flush after empty writes: got %v, want nil", out)
	}
	if got := ResampleAll(nil, 3, 2); got != nil {
		t.Fatalf("one-shot empty input: got %v, want nil", got)
	}
}

// The tail outputs that lack a right interpolation point must repeat the
// last sample.
func TestTailRepeatsLastSample(t *testing.T) {
	x := []float64{0, 10, 20, 30, 40}
	// Step 3/2: outputs at t = 0, 1.5, 3, 4.5. The last one needs x[5],
	// which does not exist, so it repeats x[4] = 40.
	want := []float64{0, 15, 30, 40}
	if got := ResampleAll(x, 3, 2); !equalFloats(got, want) {
		t.Fatalf("one-shot: got %v, want %v", got, want)
	}
	r := New(3, 2)
	got := append(r.Write(x), r.Flush()...)
	if !equalFloats(got, want) {
		t.Fatalf("streamed: got %v, want %v", got, want)
	}
	// Upsampling step 1/3 over two samples: t = 0, 1/3, 2/3, 1, 4/3, 5/3.
	x = []float64{0, 30}
	want = []float64{0, 10, 20, 30, 30, 30}
	if got := ResampleAll(x, 1, 3); !equalFloats(got, want) {
		t.Fatalf("one-shot upsample: got %v, want %v", got, want)
	}
	r = New(1, 3)
	got = append(r.Write(x), r.Flush()...)
	if !equalFloats(got, want) {
		t.Fatalf("streamed upsample: got %v, want %v", got, want)
	}
}

// Internal buffering must stay O(1) no matter how long the stream runs.
func TestBoundedBuffer(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for _, st := range [][2]int64{{1, 3}, {3, 1}, {2, 5}} {
		r := New(st[0], st[1])
		total := 0
		for i := 0; i < 100000; i++ {
			total += len(r.Write([]float64{rng.NormFloat64()}))
			if b := r.Buffered(); b > 2 {
				t.Fatalf("step %d/%d: buffered %d samples after %d writes", st[0], st[1], b, i+1)
			}
		}
		total += len(r.Flush())
		if r.OutCount() != int64(total) {
			t.Fatalf("OutCount %d != emitted %d", r.OutCount(), total)
		}
	}
}

func TestWriteAfterFlushPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic on Write after Flush")
		}
	}()
	r := New(1, 1)
	r.Write([]float64{1})
	r.Flush()
	r.Write([]float64{2})
}
