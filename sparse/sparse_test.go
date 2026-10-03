package sparse

import (
	"math/rand"
	"runtime"
	"sync"
	"testing"

	"wireopt/wire"
)

func TestBasicSetGetDelete(t *testing.T) {
	a := New()
	if _, ok := a.Get(5); ok {
		t.Fatal("empty array should miss")
	}
	a.Set(5, 50)
	a.Set(1, 10)
	a.Set(5, 55) // overwrite
	if a.Len() != 2 {
		t.Fatalf("Len = %d, want 2", a.Len())
	}
	if v, ok := a.Get(5); !ok || v != 55 {
		t.Fatalf("Get(5) = %d,%v", v, ok)
	}
	if !a.Delete(5) {
		t.Fatal("Delete(5) should hit")
	}
	if a.Delete(5) {
		t.Fatal("second Delete(5) should miss")
	}
	if _, ok := a.Get(5); ok {
		t.Fatal("Get(5) after delete should miss")
	}
	if a.Len() != 1 {
		t.Fatalf("Len = %d, want 1", a.Len())
	}
	// Zero values must be distinguishable from absence.
	a.Set(9, 0)
	if v, ok := a.Get(9); !ok || v != 0 {
		t.Fatalf("zero value: Get(9) = %d,%v", v, ok)
	}
}

func TestAdaptiveConversion(t *testing.T) {
	a := New()
	const n = 1000
	for i := uint32(0); i < n; i++ {
		a.Set(i, uint64(i)*3)
	}
	if !a.Dense() {
		t.Fatal("contiguous fill should convert to dense")
	}
	denseMem := a.MemBytes()
	sparseMem := n * 48
	if denseMem >= sparseMem {
		t.Fatalf("dense mem %d should beat sparse estimate %d", denseMem, sparseMem)
	}
	for i := uint32(0); i < n; i++ {
		if v, ok := a.Get(i); !ok || v != uint64(i)*3 {
			t.Fatalf("dense Get(%d) = %d,%v", i, v, ok)
		}
	}
	// Drain the middle: two survivors 1000 apart, density collapses
	// and the container must convert back to the sparse regime.
	for i := uint32(1); i < n-1; i++ {
		a.Delete(i)
	}
	if a.Dense() {
		t.Fatal("sparse regime should convert back to map")
	}
	if a.Len() != 2 {
		t.Fatalf("Len = %d, want 2", a.Len())
	}
	if v, ok := a.Get(0); !ok || v != 0 {
		t.Fatalf("surviving entry 0 = %d,%v", v, ok)
	}
	if v, ok := a.Get(n - 1); !ok || v != uint64(n-1)*3 {
		t.Fatalf("surviving entry = %d,%v", v, ok)
	}
}

func TestSparseStaysSparseOnHugeSpan(t *testing.T) {
	a := New()
	a.Set(0, 1)
	a.Set(1<<30, 2) // gigantic span, few entries
	a.Set(1<<31, 3)
	if a.Dense() {
		t.Fatal("huge span must never densify")
	}
	if a.Span() != uint64(1<<31)+1 {
		t.Fatalf("Span = %d", a.Span())
	}
	if v, ok := a.Get(1 << 30); !ok || v != 2 {
		t.Fatalf("Get = %d,%v", v, ok)
	}
}

func TestDenseOutOfWindowFallsBack(t *testing.T) {
	a := New()
	for i := uint32(0); i < 100; i++ {
		a.Set(i, uint64(i))
	}
	if !a.Dense() {
		t.Fatal("expected dense")
	}
	a.Set(1<<30, 999) // far outside the dense window
	if a.Dense() {
		t.Fatal("out-of-window insert should drop to sparse")
	}
	for i := uint32(0); i < 100; i++ {
		if v, ok := a.Get(i); !ok || v != uint64(i) {
			t.Fatalf("Get(%d) = %d,%v", i, v, ok)
		}
	}
	if v, ok := a.Get(1 << 30); !ok || v != 999 {
		t.Fatalf("outlier = %d,%v", v, ok)
	}
}

func collect(a *Array) map[uint32]uint64 {
	out := make(map[uint32]uint64)
	a.Range(func(idx uint32, v uint64) bool {
		out[idx] = v
		return true
	})
	return out
}

func TestCompressRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 20; trial++ {
		a := New()
		want := make(map[uint32]uint64)
		n := rng.Intn(500)
		for i := 0; i < n; i++ {
			idx := uint32(rng.Intn(1 << 22))
			v := uint64(rng.Int63n(1 << 40))
			a.Set(idx, v)
			want[idx] = v
		}
		blob := a.Compress()
		b, err := Decompress(blob)
		if err != nil {
			t.Fatalf("trial %d: Decompress: %v", trial, err)
		}
		got := collect(b)
		if len(got) != len(want) {
			t.Fatalf("trial %d: got %d entries, want %d", trial, len(got), len(want))
		}
		for k, v := range want {
			if got[k] != v {
				t.Fatalf("trial %d: key %d = %d, want %d", trial, k, got[k], v)
			}
		}
	}
	// Empty array round-trips.
	b, err := Decompress(New().Compress())
	if err != nil || b.Len() != 0 {
		t.Fatalf("empty roundtrip: len=%d err=%v", b.Len(), err)
	}
}

func TestCompressTruncationSafety(t *testing.T) {
	a := New()
	for i := uint32(0); i < 50; i++ {
		a.Set(i*7, uint64(i))
	}
	blob := a.Compress()
	for i := 0; i < len(blob); i++ {
		if _, err := Decompress(blob[:i]); err == nil {
			t.Fatalf("prefix %d: want error", i)
		}
	}
	// Trailing garbage is rejected.
	if _, err := Decompress(append(append([]byte{}, blob...), 0x00)); err != ErrCorrupt {
		t.Fatalf("trailing garbage: got %v", err)
	}
	// Declared count larger than the payload.
	bad := wire.AppendVarint(nil, 1<<40)
	if _, err := Decompress(bad); err != ErrCorrupt {
		t.Fatalf("inflated count: got %v", err)
	}
}

func TestConcurrentAccess(t *testing.T) {
	a := New()
	const workers = 8
	const perWorker = 2000
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			base := uint32(w * perWorker)
			for i := uint32(0); i < perWorker; i++ {
				a.Set(base+i, uint64(w)*1e6+uint64(i))
			}
			for i := uint32(0); i < perWorker; i++ {
				if _, ok := a.Get(base + i); !ok {
					t.Errorf("missing key %d", base+i)
					return
				}
			}
			_ = a.Compress()
			_ = a.MemBytes()
			_ = a.Span()
			// Delete even entries of own range.
			for i := uint32(0); i < perWorker; i += 2 {
				a.Delete(base + i)
			}
		}(w)
	}
	wg.Wait()
	if a.Len() != workers*perWorker/2 {
		t.Fatalf("Len = %d, want %d", a.Len(), workers*perWorker/2)
	}
	for w := 0; w < workers; w++ {
		base := uint32(w * perWorker)
		for i := uint32(1); i < perWorker; i += 2 {
			want := uint64(w)*1e6 + uint64(i)
			if v, ok := a.Get(base + i); !ok || v != want {
				t.Fatalf("key %d = %d,%v want %d", base+i, v, ok, want)
			}
		}
	}
}

func TestNoGoroutineLeak(t *testing.T) {
	before := runtime.NumGoroutine()
	var wg sync.WaitGroup
	for w := 0; w < 16; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			a := New()
			for i := uint32(0); i < 500; i++ {
				a.Set(i, uint64(w)+uint64(i))
			}
			_, _ = Decompress(a.Compress())
		}(w)
	}
	wg.Wait()
	// Allow the scheduler a moment to retire finished goroutines.
	for i := 0; i < 100; i++ {
		if runtime.NumGoroutine() <= before {
			return
		}
		runtime.Gosched()
	}
	t.Fatalf("goroutine leak: before=%d after=%d", before, runtime.NumGoroutine())
}

func TestRangeOrder(t *testing.T) {
	a := New()
	idxs := []uint32{50, 3, 900, 1, 77}
	for _, i := range idxs {
		a.Set(i, uint64(i))
	}
	prev := uint32(0)
	first := true
	a.Range(func(idx uint32, v uint64) bool {
		if !first && idx <= prev {
			t.Fatalf("Range not ascending: %d after %d", idx, prev)
		}
		first = false
		prev = idx
		return true
	})
	// Early stop.
	count := 0
	a.Range(func(idx uint32, v uint64) bool {
		count++
		return false
	})
	if count != 1 {
		t.Fatalf("early stop visited %d entries", count)
	}
}
