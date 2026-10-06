package resample

import (
	"math/rand"
	"testing"
)

func TestChunkingsMatchOneShot(t *testing.T) {
	ratios := [][2]int64{{1, 1}, {2, 1}, {3, 2}, {2, 3}, {7, 5}, {1, 4}, {5, 3}}
	rng := rand.New(rand.NewSource(42))
	for _, rt := range ratios {
		for _, n := range []int{1, 2, 3, 10, 97, 256} {
			samples := make([]float64, n)
			for i := range samples {
				samples[i] = rng.NormFloat64()
			}
			want := oneShot(samples, []Segment{{rt[0], rt[1]}}, nil)

			// Random chunk sizes between 1 and 2*n+3, fixed per iteration.
			var chunks []int
			for c := 0; c < 3; c++ {
				chunks = append(chunks, 1+rng.Intn(2*n+3))
			}
			r, err := New(rt[0], rt[1])
			if err != nil {
				t.Fatal(err)
			}
			got := feedChunks(t, r.Write, r.Flush, samples, chunks)
			assertClose(t, got, want)

			if int64(len(got)) != r.TotalOutputCount(int64(n)) {
				t.Fatalf("ratio %d/%d n=%d: count %d != TotalOutputCount %d",
					rt[0], rt[1], n, len(got), r.TotalOutputCount(int64(n)))
			}
		}
	}
}

func TestOutputCountExact(t *testing.T) {
	// K = ceil(N*num/den), verified against the rational reference.
	r, _ := New(3, 2)
	for n := int64(0); n <= 50; n++ {
		if got, want := r.TotalOutputCount(n), (n*3+1)/2; got != want {
			t.Fatalf("n=%d: got %d want %d", n, got, want)
		}
	}
}

func TestFlushRepeatsLastSample(t *testing.T) {
	// 2x upsampling of [10, 20]: positions 0, 0.5, 1, 1.5 -> 10, 15, 20, 20.
	r, _ := New(2, 1)
	out, err := r.Write([]float64{10, 20})
	if err != nil {
		t.Fatal(err)
	}
	tail, err := r.Flush()
	if err != nil {
		t.Fatal(err)
	}
	got := append(out, tail...)
	want := []float64{10, 15, 20, 20}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for k := range want {
		if got[k] != want[k] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
}

func TestEmptyInputNoOutput(t *testing.T) {
	r, _ := New(3, 2)
	out, err := r.Write(nil)
	if err != nil || len(out) != 0 {
		t.Fatalf("empty write: out=%v err=%v", out, err)
	}
	out, err = r.Flush()
	if err != nil || len(out) != 0 {
		t.Fatalf("empty flush: out=%v err=%v", out, err)
	}
}

func TestWriteAfterFlush(t *testing.T) {
	r, _ := New(1, 1)
	if _, err := r.Flush(); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Write([]float64{1}); err != ErrWriteAfterFlush {
		t.Fatalf("want ErrWriteAfterFlush, got %v", err)
	}
	// Flush is idempotent.
	if out, err := r.Flush(); err != nil || out != nil {
		t.Fatalf("second flush: out=%v err=%v", out, err)
	}
}

func TestBufferDoesNotGrowWithStream(t *testing.T) {
	for _, rt := range [][2]int64{{1, 1}, {2, 1}, {1, 3}, {7, 5}} {
		r, _ := New(rt[0], rt[1])
		for i := 0; i < 200000; i++ {
			if _, err := r.Write([]float64{float64(i)}); err != nil {
				t.Fatal(err)
			}
			if b := r.Buffered(); b > 2 {
				t.Fatalf("ratio %d/%d: buffer grew to %d", rt[0], rt[1], b)
			}
		}
		if _, err := r.Flush(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestNonPositiveRatio(t *testing.T) {
	if _, err := New(0, 1); err != ErrNonPositiveRatio {
		t.Fatalf("got %v", err)
	}
	if _, err := New(1, -2); err != ErrNonPositiveRatio {
		t.Fatalf("got %v", err)
	}
}
