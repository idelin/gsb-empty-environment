package aries

import (
	"runtime"
	"sync"
	"testing"
)

func TestSparseSetGetZeroDelete(t *testing.T) {
	p := NewSparsePage()
	if got := p.Get(7); got != 0 {
		t.Fatalf("empty cell = %d, want 0", got)
	}
	p.Set(7, 42)
	p.Set(1023, 1)
	if got := p.Get(7); got != 42 {
		t.Fatalf("cell 7 = %d, want 42", got)
	}
	if p.NonZero() != 2 {
		t.Fatalf("NonZero = %d, want 2", p.NonZero())
	}
	p.Set(7, 0) // zero deletes the cell
	if got := p.Get(7); got != 0 {
		t.Fatalf("deleted cell = %d, want 0", got)
	}
	if p.NonZero() != 1 {
		t.Fatalf("NonZero after delete = %d, want 1", p.NonZero())
	}
	// Setting same value twice must not double-count.
	p.Set(1023, 1)
	if p.NonZero() != 1 {
		t.Fatalf("NonZero after idempotent set = %d, want 1", p.NonZero())
	}
}

func TestAdaptiveConversion(t *testing.T) {
	p := NewSparsePage()
	if p.Dense() {
		t.Fatal("new page must be sparse")
	}
	// Fill up to exactly the densify threshold: still sparse.
	for i := 0; i < densifyThreshold; i++ {
		p.Set(uint32(i), uint32(i+1))
	}
	if p.Dense() {
		t.Fatalf("page densified at %d cells, threshold is >%d", p.NonZero(), densifyThreshold)
	}
	// One more cell crosses the threshold.
	p.Set(uint32(densifyThreshold), 1)
	if !p.Dense() {
		t.Fatalf("page did not densify at %d cells", p.NonZero())
	}
	// Data must survive the conversion.
	for i := 0; i <= densifyThreshold; i++ {
		if got := p.Get(uint32(i)); got != uint32(i+1) && i < densifyThreshold {
			t.Fatalf("cell %d = %d after densify, want %d", i, got, i+1)
		}
	}
	// Drain down to the sparsify threshold: still dense (hysteresis).
	for i := 0; i < densifyThreshold+1-sparsifyThreshold; i++ {
		p.Set(uint32(i), 0)
	}
	if !p.Dense() {
		t.Fatalf("page sparsified at %d cells, threshold is <%d", p.NonZero(), sparsifyThreshold)
	}
	// One more deletion crosses the lower threshold.
	p.Set(uint32(densifyThreshold-sparsifyThreshold+1), 0)
	if p.Dense() {
		t.Fatalf("page did not sparsify at %d cells", p.NonZero())
	}
	if p.NonZero() != sparsifyThreshold-1 {
		t.Fatalf("NonZero = %d, want %d", p.NonZero(), sparsifyThreshold-1)
	}
}

func TestSerializeRoundTrip(t *testing.T) {
	mkSparse := func() *SparsePage {
		p := NewSparsePage()
		p.LSN = 98765
		p.Set(0, 1)
		p.Set(513, 0xDEADBEEF)
		p.Set(1023, 7)
		return p
	}
	mkDense := func() *SparsePage {
		p := NewSparsePage()
		p.LSN = 123456
		for i := 0; i < NumCells; i++ {
			p.Set(uint32(i), uint32(i*3+1))
		}
		return p
	}
	for name, src := range map[string]*SparsePage{"sparse": mkSparse(), "dense": mkDense()} {
		blob := src.Serialize()
		got, err := DeserializePage(blob)
		if err != nil {
			t.Fatalf("%s: deserialize: %v", name, err)
		}
		if got.LSN != src.LSN {
			t.Fatalf("%s: LSN = %d, want %d", name, got.LSN, src.LSN)
		}
		if got.Dense() != src.Dense() {
			t.Fatalf("%s: dense = %v, want %v", name, got.Dense(), src.Dense())
		}
		if got.NonZero() != src.NonZero() {
			t.Fatalf("%s: NonZero = %d, want %d", name, got.NonZero(), src.NonZero())
		}
		for i := 0; i < NumCells; i++ {
			if got.Get(uint32(i)) != src.Get(uint32(i)) {
				t.Fatalf("%s: cell %d = %d, want %d", name, i, got.Get(uint32(i)), src.Get(uint32(i)))
			}
		}
	}
}

func TestCompressionEffectiveness(t *testing.T) {
	// Massive sparse data: 3 live cells in a 4 KiB page.
	sparse := NewSparsePage()
	sparse.Set(1, 10)
	sparse.Set(500, 20)
	sparse.Set(1023, 30)
	if n := len(sparse.Serialize()); n > 32 {
		t.Fatalf("sparse page serialized to %d bytes, want <= 32", n)
	}
	// Dense container over mostly-zero data: RLE must compress well below 4 KiB.
	dense := NewSparsePage()
	for i := 0; i < densifyThreshold+1; i++ {
		dense.Set(uint32(i*2), uint32(i+1)) // every other cell
	}
	if !dense.Dense() {
		t.Fatal("expected dense container")
	}
	if n := len(dense.Serialize()); n > 1200 {
		t.Fatalf("RLE dense page serialized to %d bytes, want <= 1200 (raw = %d)", n, NumCells*4)
	}
	// Memory: sparse container must be far smaller than the dense array.
	if sparse.MemoryBytes() >= NumCells*4 {
		t.Fatalf("sparse MemoryBytes = %d, want < %d", sparse.MemoryBytes(), NumCells*4)
	}
}

func TestDeserializeCorrupt(t *testing.T) {
	if _, err := DeserializePage(nil); err == nil {
		t.Fatal("expected error for empty input")
	}
	if _, err := DeserializePage([]byte{0x80}); err == nil {
		t.Fatal("expected error for truncated input")
	}
	if _, err := DeserializePage([]byte{0x01, 0x09}); err == nil {
		t.Fatal("expected error for unknown mode")
	}
}

func TestSparsePageConcurrent(t *testing.T) {
	p := NewSparsePage()
	const workers = 8
	const ops = 5000
	var wg sync.WaitGroup
	for g := 0; g < workers; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < ops; i++ {
				off := uint32((g*ops + i) % NumCells)
				p.Set(off, uint32(i+1))
				_ = p.Get(off)
				if i%3 == 0 {
					p.Set(off, 0)
				}
				if i%97 == 0 {
					_ = p.Serialize()
					_ = p.NonZero()
				}
			}
		}(g)
	}
	wg.Wait()
	// Invariant: NonZero must exactly match the number of non-zero cells.
	cnt := 0
	for i := 0; i < NumCells; i++ {
		if p.Get(uint32(i)) != 0 {
			cnt++
		}
	}
	if cnt != p.NonZero() {
		t.Fatalf("NonZero = %d, actual non-zero cells = %d", p.NonZero(), cnt)
	}
	// Invariant: container representation must match the thresholds.
	if p.Dense() && p.NonZero() < sparsifyThreshold {
		t.Fatalf("dense container below sparsify threshold (%d)", p.NonZero())
	}
	if !p.Dense() && p.NonZero() > densifyThreshold {
		t.Fatalf("sparse container above densify threshold (%d)", p.NonZero())
	}
}

func TestNoMemoryLeak(t *testing.T) {
	// workload densifies many pages, serializes them, then clears every
	// cell; each page must convert back to sparse and release its array.
	workload := func() (total uint64, denseLeft int, bytesLeft int) {
		pages := make([]*SparsePage, 2000)
		for i := range pages {
			p := NewSparsePage()
			for j := 0; j < 400; j++ {
				p.Set(uint32(j*7%NumCells), uint32(j+1))
			}
			pages[i] = p
		}
		for _, p := range pages {
			total += uint64(len(p.Serialize()))
		}
		for _, p := range pages {
			for j := 0; j < 400; j++ {
				p.Set(uint32(j*7%NumCells), 0)
			}
			if p.Dense() {
				denseLeft++
			}
			bytesLeft += p.MemoryBytes()
		}
		return total, denseLeft, bytesLeft
	}

	_, denseLeft, bytesLeft := workload()
	if denseLeft != 0 {
		t.Fatalf("%d pages failed to convert back to sparse", denseLeft)
	}
	if bytesLeft != 0 {
		t.Fatalf("containers retained %d bytes after clearing", bytesLeft)
	}

	runtime.GC()
	var m1 runtime.MemStats
	runtime.ReadMemStats(&m1)
	_, _, _ = workload()
	runtime.GC()
	var m2 runtime.MemStats
	runtime.ReadMemStats(&m2)
	const slack = 1 << 20 // 1 MiB
	if m2.HeapAlloc > m1.HeapAlloc+slack {
		t.Fatalf("heap grew across identical workloads: %d -> %d bytes", m1.HeapAlloc, m2.HeapAlloc)
	}
	if before, after := runtime.NumGoroutine(), runtime.NumGoroutine(); after > before {
		t.Fatalf("goroutine leak: %d -> %d", before, after)
	}
}
