package sparse

import (
	"math/rand"
	"runtime"
	"sync"
	"testing"
)

// mustHave asserts the set contains exactly the given keys.
func mustHave(t *testing.T, s *Set, want []uint32) {
	t.Helper()
	if s.Cardinality() != len(want) {
		t.Fatalf("cardinality=%d, want %d", s.Cardinality(), len(want))
	}
	for _, k := range want {
		if !s.Contains(k) {
			t.Fatalf("missing key %d", k)
		}
	}
	got := s.ToSlice()
	if len(got) != len(want) {
		t.Fatalf("ToSlice len=%d, want %d", len(got), len(want))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("ToSlice[%d]=%d, want %d", i, got[i], want[i])
		}
	}
}

func TestEmptySet(t *testing.T) {
	var s Set
	if s.Cardinality() != 0 {
		t.Fatal("empty set has nonzero cardinality")
	}
	if s.Contains(0) || s.Contains(1<<32-1) {
		t.Fatal("empty set contains keys")
	}
	if s.Remove(42) {
		t.Fatal("remove on empty set reports success")
	}
	if got := s.ToSlice(); got != nil && len(got) != 0 {
		t.Fatalf("ToSlice=%v", got)
	}
	base := s.MemBytes()
	s.Optimize()
	if s.MemBytes() != base {
		t.Fatal("Optimize changed empty-set memory baseline")
	}
}

func TestSingleAndBoundaryKeys(t *testing.T) {
	var s Set
	keys := []uint32{0, 1, ContainerSize - 1, ContainerSize, 1<<32 - 1}
	for _, k := range keys {
		if !s.Add(k) {
			t.Fatalf("Add(%d) failed", k)
		}
	}
	for _, k := range keys {
		if s.Add(k) {
			t.Fatalf("duplicate Add(%d) reported as new", k)
		}
	}
	mustHave(t, &s, []uint32{0, 1, ContainerSize - 1, ContainerSize, 1<<32 - 1})

	// Remove boundaries of container shards.
	if !s.Remove(ContainerSize - 1) {
		t.Fatal("remove shard-boundary key failed")
	}
	if !s.Remove(1 << 32 - 1) {
		t.Fatal("remove max key failed")
	}
	if s.Remove(1 << 32 - 1) {
		t.Fatal("double remove reported success")
	}
	mustHave(t, &s, []uint32{0, 1, ContainerSize})
}

func TestDenseContainerConversion(t *testing.T) {
	var s Set
	// Fill one full shard (65536 keys): array -> bitmap conversion.
	for k := uint32(0); k < ContainerSize; k++ {
		s.Add(k)
	}
	if s.Cardinality() != ContainerSize {
		t.Fatalf("cardinality=%d", s.Cardinality())
	}
	memBitmap := s.MemBytes()
	if memBitmap > 8192+256 {
		t.Fatalf("dense shard not using bitmap: mem=%d", memBitmap)
	}
	// Drain below the hysteresis threshold: bitmap -> array conversion.
	for k := uint32(0); k < ContainerSize-1000; k++ {
		s.Remove(k)
	}
	if s.Cardinality() != 1000 {
		t.Fatalf("cardinality=%d", s.Cardinality())
	}
	memArray := s.MemBytes()
	if memArray >= memBitmap {
		t.Fatalf("no compaction after drain: bitmap=%d array=%d", memBitmap, memArray)
	}
	// Drain fully: shard must be dropped, memory back near baseline.
	for k := uint32(ContainerSize - 1000); k < ContainerSize; k++ {
		s.Remove(k)
	}
	if s.ContainerCount() != 0 {
		t.Fatalf("empty shard not dropped: containers=%d", s.ContainerCount())
	}
	var empty Set
	if s.MemBytes() != empty.MemBytes() {
		t.Fatalf("memory leak after full drain: got %d, want %d", s.MemBytes(), empty.MemBytes())
	}
}

func TestRunContainerOptimize(t *testing.T) {
	var s Set
	// 10k keys in 3 long runs: run encoding must beat array and bitmap.
	runs := [][2]uint32{{100, 5000}, {1 << 20, 3000}, {1 << 30, 2000}}
	total := 0
	for _, r := range runs {
		for k := r[0]; k < r[0]+r[1]; k++ {
			s.Add(k)
			total++
		}
	}
	if s.Cardinality() != total {
		t.Fatalf("cardinality=%d, want %d", s.Cardinality(), total)
	}
	before := s.MemBytes()
	reclaimed := s.Optimize()
	after := s.MemBytes()
	if reclaimed != before-after {
		t.Fatalf("reclaimed=%d but before=%d after=%d", reclaimed, before, after)
	}
	if after > 1024 {
		t.Fatalf("run encoding not compact: %d bytes for %d keys", after, total)
	}
	// Data intact after optimization.
	for _, r := range runs {
		for k := r[0]; k < r[0]+r[1]; k += 97 {
			if !s.Contains(k) {
				t.Fatalf("key %d lost during Optimize", k)
			}
		}
	}
	// Optimizing twice is a no-op.
	if s.Optimize() != 0 {
		t.Fatal("second Optimize reclaimed memory")
	}
}

func TestAlternatingWorstCase(t *testing.T) {
	var s Set
	// Alternating bits: worst case for run encoding.
	for k := uint32(0); k < 10000; k += 2 {
		s.Add(k)
	}
	s.Optimize()
	// 5000 isolated keys must fall back to array (2B/key), not runs (4B/key)
	// or bitmap (8192B).
	if mem := s.MemBytes(); mem > 2*5000+512 {
		t.Fatalf("worst-case runs not converted back to array: mem=%d", mem)
	}
	for k := uint32(0); k < 10000; k += 2 {
		if !s.Contains(k) {
			t.Fatalf("key %d lost", k)
		}
		if s.Contains(k + 1) {
			t.Fatalf("phantom key %d", k+1)
		}
	}
}

func TestRunSplitAndMerge(t *testing.T) {
	var s Set
	// Build [10..19], split it by removing the middle, then merge it back.
	for k := uint32(10); k < 20; k++ {
		s.Add(k)
	}
	s.Optimize()
	s.Remove(15)
	s.Optimize()
	for k := uint32(10); k < 20; k++ {
		if k == 15 {
			if s.Contains(k) {
				t.Fatal("split hole vanished")
			}
			continue
		}
		if !s.Contains(k) {
			t.Fatalf("key %d lost after split", k)
		}
	}
	s.Add(15)
	s.Optimize()
	for k := uint32(10); k < 20; k++ {
		if !s.Contains(k) {
			t.Fatalf("key %d lost after merge", k)
		}
	}
	// Adjacent inserts merge runs: fill 0..999 out of order.
	var s2 Set
	for k := 999; k >= 0; k-- {
		s2.Add(uint32(k))
	}
	s2.Optimize()
	if mem := s2.MemBytes(); mem > 128 {
		t.Fatalf("descending fill did not coalesce into one run: mem=%d", mem)
	}
	if s2.Cardinality() != 1000 {
		t.Fatalf("cardinality=%d", s2.Cardinality())
	}
}

func TestClearResetsMemory(t *testing.T) {
	var empty Set
	base := empty.MemBytes()
	var s Set
	for k := uint32(0); k < 200000; k++ {
		s.Add(k * 7)
	}
	s.Optimize()
	s.Clear()
	if s.Cardinality() != 0 || s.ContainerCount() != 0 {
		t.Fatal("Clear left state behind")
	}
	if s.MemBytes() != base {
		t.Fatalf("memory leak after Clear: got %d, want %d", s.MemBytes(), base)
	}
}

func TestSnapshotRoundTrip(t *testing.T) {
	var s Set
	// Mix of sparse, dense and run-friendly shards.
	for k := uint32(0); k < ContainerSize; k++ { // full dense shard
		s.Add(k)
	}
	for k := uint32(1 << 20); k < 1<<20+5000; k++ { // run shard
		s.Add(k)
	}
	for _, k := range []uint32{1 << 30, 1<<30 + 7, 1<<31 + 12345, 1<<32 - 1} { // sparse
		s.Add(k)
	}
	// Snapshot the unoptimized (bitmap-heavy) set: gzip must shrink the
	// repetitive 8 KiB bitmap payload.
	raw0 := s.Snapshot()
	comp0 := CompressSnapshot(raw0)
	if len(comp0) >= len(raw0) {
		t.Fatalf("gzip did not compress bitmap snapshot: raw=%d gzip=%d", len(raw0), len(comp0))
	}
	t.Logf("bitmap snapshot: %d bytes raw, %d bytes gzip", len(raw0), len(comp0))

	s.Optimize()

	snap := s.Snapshot()
	loaded, err := LoadSnapshot(snap)
	if err != nil {
		t.Fatalf("LoadSnapshot: %v", err)
	}
	if !s.Equals(loaded) {
		t.Fatal("snapshot round-trip mismatch")
	}

	// gzip compression round-trip.
	comp := CompressSnapshot(snap)
	raw, err := DecompressSnapshot(comp)
	if err != nil {
		t.Fatalf("DecompressSnapshot: %v", err)
	}
	loaded2, err := LoadSnapshot(raw)
	if err != nil {
		t.Fatalf("LoadSnapshot(decompressed): %v", err)
	}
	if !s.Equals(loaded2) {
		t.Fatal("compressed round-trip mismatch")
	}
	t.Logf("snapshot: %d bytes raw, %d bytes gzip for %d keys", len(snap), len(comp), s.Cardinality())
}

func TestSnapshotCorruption(t *testing.T) {
	var s Set
	for k := uint32(0); k < 100; k++ {
		s.Add(k * 3)
	}
	snap := s.Snapshot()
	if _, err := LoadSnapshot(snap[:len(snap)/2]); err == nil {
		t.Fatal("truncated snapshot accepted")
	}
	bad := append([]byte(nil), snap...)
	bad[1] = 0xFF // corrupt first shard key
	if _, err := LoadSnapshot(bad); err == nil {
		t.Fatal("corrupt shard key accepted")
	}
	badTag := append([]byte(nil), snap...)
	badTag[2] = 99 // invalid container tag (after count + key varints)
	if _, err := LoadSnapshot(badTag); err == nil {
		t.Fatal("invalid tag accepted")
	}
	trailing := append(append([]byte(nil), snap...), 0x00)
	if _, err := LoadSnapshot(trailing); err == nil {
		t.Fatal("trailing garbage accepted")
	}
	if _, err := DecompressSnapshot([]byte("not gzip")); err == nil {
		t.Fatal("invalid gzip stream accepted")
	}
}

func TestCloneIndependence(t *testing.T) {
	var s Set
	for k := uint32(0); k < 5000; k++ {
		s.Add(k)
	}
	s.Optimize()
	c := s.Clone()
	if !s.Equals(c) {
		t.Fatal("clone differs from original")
	}
	// Mutating the clone must not affect the original.
	for k := uint32(0); k < 5000; k += 2 {
		c.Remove(k)
	}
	if s.Cardinality() != 5000 {
		t.Fatalf("original affected by clone mutation: card=%d", s.Cardinality())
	}
}

func TestConcurrentAccess(t *testing.T) {
	var s Set
	const workers = 8
	const perWorker = 2000
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(w)))
			base := uint32(w * perWorker)
			for i := 0; i < perWorker; i++ {
				s.Add(base + uint32(i)) // disjoint ranges: no contention on values
			}
			for i := 0; i < 500; i++ {
				k := uint32(rng.Intn(workers * perWorker))
				s.Contains(k)
				s.Cardinality()
				s.MemBytes()
			}
			for i := 0; i < perWorker; i += 2 {
				s.Remove(base + uint32(i))
			}
		}(w)
	}
	wg.Wait()
	// Exactly the odd offsets of each worker range remain.
	if want := workers * perWorker / 2; s.Cardinality() != want {
		t.Fatalf("cardinality=%d, want %d", s.Cardinality(), want)
	}
	for w := 0; w < workers; w++ {
		for i := 1; i < perWorker; i += 2 {
			if !s.Contains(uint32(w*perWorker + i)) {
				t.Fatalf("missing key %d", w*perWorker+i)
			}
		}
	}
	// Concurrent Optimize must be safe and preserve data.
	var wg2 sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg2.Add(1)
		go func() {
			defer wg2.Done()
			s.Optimize()
		}()
	}
	wg2.Wait()
	if want := workers * perWorker / 2; s.Cardinality() != want {
		t.Fatalf("cardinality after Optimize=%d, want %d", s.Cardinality(), want)
	}
}

func TestNoGoroutineLeak(t *testing.T) {
	base := runtime.NumGoroutine()
	for round := 0; round < 3; round++ {
		var s Set
		var wg sync.WaitGroup
		for w := 0; w < 4; w++ {
			wg.Add(1)
			go func(w int) {
				defer wg.Done()
				for i := 0; i < 1000; i++ {
					s.Add(uint32(w*1000 + i))
				}
			}(w)
		}
		wg.Wait()
		s.Optimize()
		s.Snapshot()
		s.Clear()
	}
	if got := runtime.NumGoroutine(); got > base {
		t.Fatalf("goroutine leak: base=%d now=%d", base, got)
	}
}

// TestMassiveSparseMemoryBound is the "massive sparse data" acceptance
// check: 2M keys spread over the full uint32 space must stay far below the
// dense bitmap cost of the covered shards.
func TestMassiveSparseMemoryBound(t *testing.T) {
	var s Set
	const n = 2000000
	// 2M keys: 1000 runs of 2000 keys scattered across the key space.
	for r := 0; r < 1000; r++ {
		start := uint32(r) * 4000000
		for k := start; k < start+2000; k++ {
			s.Add(k)
		}
	}
	if s.Cardinality() != n {
		t.Fatalf("cardinality=%d, want %d", s.Cardinality(), n)
	}
	before := s.MemBytes()
	s.Optimize()
	after := s.MemBytes()
	t.Logf("2M sparse keys: %d bytes before optimize, %d after (%.2f bytes/key)",
		before, after, float64(after)/n)
	// 1000 runs x ~30 containers must be dramatically smaller than the
	// ~3.9M keys of bitmap coverage; require well under 4 bytes/key.
	if after > 4*n {
		t.Fatalf("memory bound violated: %d bytes for %d keys", after, n)
	}
	// Spot-check data integrity.
	for r := 0; r < 1000; r += 7 {
		start := uint32(r) * 4000000
		if !s.Contains(start) || !s.Contains(start+1999) || s.Contains(start+2000) {
			t.Fatalf("run %d corrupted", r)
		}
	}
}
