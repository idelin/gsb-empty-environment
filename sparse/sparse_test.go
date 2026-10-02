package sparse

import (
	"math/rand"
	"runtime"
	"sync"
	"testing"
)

func TestBasicAddContains(t *testing.T) {
	s := New()
	if s.Len() != 0 {
		t.Fatalf("empty set len = %d", s.Len())
	}
	for _, v := range []uint32{5, 1, 100, 42, 5, 1} { // duplicates included
		s.Add(v)
	}
	if s.Len() != 4 {
		t.Fatalf("len = %d, want 4 (dedup)", s.Len())
	}
	for _, v := range []uint32{1, 5, 42, 100} {
		if !s.Contains(v) {
			t.Errorf("missing %d", v)
		}
	}
	for _, v := range []uint32{0, 2, 41, 43, 99, 101, 1 << 31} {
		if s.Contains(v) {
			t.Errorf("unexpected hit %d", v)
		}
	}
}

func TestBoundaryValues(t *testing.T) {
	s := New()
	for _, v := range []uint32{0, 1, 63, 64, 65, 1<<31 - 1, 1 << 31, ^uint32(0)} {
		s.Add(v)
	}
	for _, v := range []uint32{0, 1, 63, 64, 65, 1<<31 - 1, 1 << 31, ^uint32(0)} {
		if !s.Contains(v) {
			t.Errorf("missing boundary value %d", v)
		}
	}
	if s.Len() != 8 {
		t.Errorf("len = %d, want 8", s.Len())
	}
}

func TestBackendStaysArrayForSparse(t *testing.T) {
	s := New()
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 100; i++ { // under arrayMaxSize, extremely sparse
		s.Add(uint32(rng.Intn(1 << 30)))
	}
	if s.Backend() != BackendArray {
		t.Fatalf("backend = %s, want array", s.Backend())
	}
}

func TestBackendConvertsToRLE(t *testing.T) {
	s := New()
	// Clustered: long consecutive runs, low overall density.
	for base := uint32(0); base < 40; base++ {
		start := base * 100000
		for v := start; v < start+50; v++ {
			s.Add(v)
		}
	}
	if s.Backend() != BackendRLE {
		t.Fatalf("backend = %s, want rle", s.Backend())
	}
	if s.Len() != 2000 {
		t.Fatalf("len = %d, want 2000", s.Len())
	}
	// Spot-check across run boundaries.
	for base := uint32(0); base < 40; base++ {
		start := base * 100000
		if !s.Contains(start) || !s.Contains(start+49) {
			t.Errorf("run at %d broken", start)
		}
		if s.Contains(start+50) || (base > 0 && s.Contains(start-1)) {
			t.Errorf("run at %d leaked past boundary", start)
		}
	}
}

func TestBackendConvertsToBitmap(t *testing.T) {
	s := New()
	for v := uint32(0); v < 2000; v++ { // 100% dense
		s.Add(v)
	}
	if s.Backend() != BackendBitmap {
		t.Fatalf("backend = %s, want bitmap", s.Backend())
	}
	for v := uint32(0); v < 2000; v++ {
		if !s.Contains(v) {
			t.Fatalf("bitmap missing %d", v)
		}
	}
	if s.Contains(2000) {
		t.Error("bitmap false positive at 2000")
	}
}

func TestRunMerging(t *testing.T) {
	s := New()
	// Insert in an order that forces both forward and backward merges.
	for _, v := range []uint32{10, 12, 11, 14, 13, 5, 6, 7, 9, 8} {
		s.Add(v)
	}
	for v := uint32(5); v <= 14; v++ {
		if !s.Contains(v) {
			t.Errorf("missing %d after merges", v)
		}
	}
	if s.Contains(4) || s.Contains(15) {
		t.Error("run over-merged")
	}
	if s.Len() != 10 {
		t.Errorf("len = %d, want 10", s.Len())
	}
}

func collectAll(s *Set) []uint32 {
	var out []uint32
	s.Range(func(v uint32) bool {
		out = append(out, v)
		return true
	})
	return out
}

func TestRangeSortedAndEarlyStop(t *testing.T) {
	s := New()
	for _, v := range []uint32{9, 3, 7, 1} {
		s.Add(v)
	}
	got := collectAll(s)
	want := []uint32{1, 3, 7, 9}
	if len(got) != len(want) {
		t.Fatalf("range got %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("range got %v, want %v", got, want)
		}
	}
	n := 0
	s.Range(func(v uint32) bool {
		n++
		return false
	})
	if n != 1 {
		t.Errorf("early stop visited %d values", n)
	}
}

func distributions() map[string][]uint32 {
	rng := rand.New(rand.NewSource(7))
	sparse := make([]uint32, 0, 5000)
	seen := map[uint32]bool{}
	for len(sparse) < 5000 {
		v := uint32(rng.Intn(1 << 28))
		if !seen[v] {
			seen[v] = true
			sparse = append(sparse, v)
		}
	}
	var clustered []uint32
	for base := uint32(0); base < 100; base++ {
		for v := base * 50000; v < base*50000+200; v++ {
			clustered = append(clustered, v)
		}
	}
	var dense []uint32
	for v := uint32(0); v < 20000; v++ {
		dense = append(dense, v)
	}
	return map[string][]uint32{"sparse": sparse, "clustered": clustered, "dense": dense}
}

func TestMarshalRoundTrip(t *testing.T) {
	for name, vals := range distributions() {
		t.Run(name, func(t *testing.T) {
			s := New()
			for _, v := range vals {
				s.Add(v)
			}
			buf := s.Marshal()
			// Compression: delta+varint must beat raw 4-byte encoding.
			if len(buf) >= len(vals)*4 {
				t.Errorf("no compression: %d bytes for %d values", len(buf), len(vals))
			}
			s2 := New()
			if err := s2.Unmarshal(buf); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if !s.Equal(s2) {
				t.Fatalf("round trip mismatch: got %d values, want %d", s2.Len(), s.Len())
			}
		})
	}
}

func TestMarshalCompactionRatio(t *testing.T) {
	// Dense range: deltas are all 0 -> 1 byte per value vs 4 raw.
	s := New()
	for v := uint32(0); v < 10000; v++ {
		s.Add(v)
	}
	buf := s.Marshal()
	if len(buf) > 10000*2 {
		t.Errorf("dense marshal = %d bytes, want <= %d", len(buf), 10000*2)
	}
	t.Logf("dense 10000 values -> %d bytes (%.2f B/value)", len(buf), float64(len(buf))/10000)
}

func TestUnmarshalCorrupt(t *testing.T) {
	s := New()
	s.Add(1)
	good := s.Marshal()

	cases := map[string][]byte{
		"empty":           {},
		"truncated-count": {0x80},
		"truncated-value": {0x05, 0x01, 0x01},
		"trailing-junk":   append(append([]byte{}, good...), 0x00),
		"huge-count":      {0xff, 0xff, 0xff, 0xff, 0x0f},
		"value-overflow":  {0x02, 0xff, 0xff, 0xff, 0xff, 0x0f, 0x01}, // 2nd delta wraps u32
	}
	for name, buf := range cases {
		if err := New().Unmarshal(buf); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
	// Good buffer still decodes after the corrupt attempts.
	s2 := New()
	if err := s2.Unmarshal(good); err != nil || s2.Len() != 1 {
		t.Errorf("good buffer: err=%v len=%d", err, s2.Len())
	}
}

func TestCloneIndependence(t *testing.T) {
	for name, vals := range distributions() {
		t.Run(name, func(t *testing.T) {
			s := New()
			for _, v := range vals {
				s.Add(v)
			}
			c := s.Clone()
			if !s.Equal(c) {
				t.Fatal("clone not equal")
			}
			// Mutating the original must not affect the clone (no shared memory).
			s.Add(^uint32(0) - 7)
			if c.Contains(^uint32(0) - 7) {
				t.Fatal("clone shares memory with original")
			}
			if c.Len() != len(vals) {
				t.Fatalf("clone len = %d, want %d", c.Len(), len(vals))
			}
		})
	}
}

func TestMemoryStats(t *testing.T) {
	// Bitmap of 100k dense values: 100000 bits ~= 12.5KB, vs 400KB raw.
	s := New()
	for v := uint32(0); v < 100000; v++ {
		s.Add(v)
	}
	backend, bytes, card := s.Stats()
	if backend != BackendBitmap {
		t.Fatalf("backend = %s", backend)
	}
	if card != 100000 {
		t.Fatalf("card = %d", card)
	}
	if bytes > 100000/8+64 {
		t.Errorf("bitmap mem = %d bytes, expected ~%d", bytes, 100000/8)
	}
	t.Logf("100k dense values: %s using %d bytes (raw would be %d)", backend, bytes, card*4)
}

func TestConcurrentReadWrite(t *testing.T) {
	s := New()
	const workers = 8
	const perWorker = 2000
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			base := uint32(w * perWorker)
			for i := 0; i < perWorker; i++ {
				s.Add(base + uint32(i))
			}
		}(w)
	}
	// Concurrent readers while writers are active.
	stop := make(chan struct{})
	for r := 0; r < 2; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_ = s.Contains(12345)
					_ = s.Len()
					_ = s.Backend()
					s.Range(func(uint32) bool { return false })
					_ = s.Marshal()
				}
			}
		}()
	}
	// Wait for writers, then stop readers.
	done := make(chan struct{})
	go func() {
		// Wait only for writer goroutines: they finish first by construction.
		for i := 0; i < workers*perWorker; {
			n := s.Len()
			if n == workers*perWorker {
				break
			}
			runtime.Gosched()
			_ = i
		}
		close(done)
	}()
	<-done
	close(stop)
	wg.Wait()
	if s.Len() != workers*perWorker {
		t.Fatalf("len = %d, want %d", s.Len(), workers*perWorker)
	}
	for w := 0; w < workers; w++ {
		base := uint32(w * perWorker)
		if !s.Contains(base) || !s.Contains(base+perWorker-1) {
			t.Errorf("worker %d range incomplete", w)
		}
	}
}

func TestConcurrentCloneAndMarshal(t *testing.T) {
	s := New()
	for v := uint32(0); v < 1000; v++ {
		s.Add(v)
	}
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				c := s.Clone()
				if c.Len() != 1000 {
					t.Errorf("clone len = %d", c.Len())
					return
				}
				s2 := New()
				if err := s2.Unmarshal(s.Marshal()); err != nil {
					t.Errorf("unmarshal: %v", err)
					return
				}
				if !s2.Equal(s) {
					t.Error("marshal round trip mismatch under concurrency")
					return
				}
			}
		}()
	}
	wg.Wait()
}

// TestNoGoroutineLeak verifies the package spawns no background goroutines.
func TestNoGoroutineLeak(t *testing.T) {
	before := runtime.NumGoroutine()
	s := New()
	for v := uint32(0); v < 5000; v++ {
		s.Add(v)
	}
	_ = s.Marshal()
	_ = s.Clone()
	s.Range(func(uint32) bool { return true })
	runtime.GC()
	if after := runtime.NumGoroutine(); after > before {
		t.Errorf("goroutine leak: before=%d after=%d", before, after)
	}
}

// TestZeroAllocReads asserts read paths do not allocate (no creeping garbage).
func TestZeroAllocReads(t *testing.T) {
	s := New()
	for v := uint32(0); v < 10000; v++ {
		s.Add(v)
	}
	allocs := testing.AllocsPerRun(100, func() {
		_ = s.Contains(5000)
		_ = s.Len()
		_ = s.Backend()
	})
	if allocs != 0 {
		t.Errorf("read path allocated %f times per run", allocs)
	}
}
