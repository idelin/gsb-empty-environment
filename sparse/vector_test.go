package sparse

import (
	"runtime"
	"sync"
	"testing"
	"time"
)

func TestBasicSetGetDelete(t *testing.T) {
	v := New()
	if v.Get(42) != 0 {
		t.Fatal("unset index must read as 0")
	}
	v.Set(42, 100)
	v.Set(7, -5)
	if v.Get(42) != 100 || v.Get(7) != -5 {
		t.Fatal("Get returned wrong values")
	}
	if v.Len() != 2 {
		t.Fatalf("Len = %d, want 2", v.Len())
	}
	v.Set(42, 0) // delete
	if v.Get(42) != 0 || v.Len() != 1 {
		t.Fatal("Set(_, 0) must delete the entry")
	}
	v.Set(42, 0) // deleting again is a no-op
	if v.Len() != 1 {
		t.Fatal("double delete changed Len")
	}
}

func TestSparseStaysSparseForHugeSpan(t *testing.T) {
	v := New()
	const n = 100000
	for i := 0; i < n; i++ {
		v.Set(uint32(i)*40000, int64(i)) // span ~4e9, only 1e5 entries
	}
	if v.IsDense() {
		t.Fatal("ultra-sparse data must not convert to dense")
	}
	if mem := v.MemEstimate(); mem > n*sparseEntryBytes {
		t.Fatalf("MemEstimate = %d, want <= %d", mem, n*sparseEntryBytes)
	}
	// Dense representation would need ~80 GB; we must be far below 1 GB.
	if mem := v.MemEstimate(); mem > 1<<30 {
		t.Fatalf("memory blowup: %d bytes", mem)
	}
	for i := 0; i < n; i += 997 {
		if got := v.Get(uint32(i) * 40000); got != int64(i) {
			t.Fatalf("Get(%d) = %d, want %d", i*40000, got, i)
		}
	}
}

func TestConvertsToDenseWhenDense(t *testing.T) {
	v := New()
	const n = 20000
	for i := 0; i < n; i++ {
		v.Set(uint32(i), int64(i*2))
	}
	if !v.IsDense() {
		t.Fatal("fully dense data should convert to dense representation")
	}
	// Dense memory must be ~ n*8, far less than sparse n*32.
	if mem := v.MemEstimate(); mem > n*denseValueBytes*2 {
		t.Fatalf("dense MemEstimate = %d, want <= %d", mem, n*denseValueBytes*2)
	}
	for i := 0; i < n; i += 499 {
		if got := v.Get(uint32(i)); got != int64(i*2) {
			t.Fatalf("Get(%d) = %d, want %d", i, got, i*2)
		}
	}
}

func TestHysteresisNoThrashing(t *testing.T) {
	v := New()
	const n = 10000
	for i := 0; i < n; i++ {
		v.Set(uint32(i), int64(i+1))
	}
	if !v.IsDense() {
		t.Fatal("expected dense")
	}
	// Delete 25%: sparse would be 10000*0.75*32 = 240k vs dense 80k.
	// Still cheaper dense -> must stay dense.
	for i := 0; i < n/4; i++ {
		v.Set(uint32(i*4), 0)
	}
	if !v.IsDense() {
		t.Fatal("25% deletion must not downgrade to sparse (hysteresis)")
	}
	// Delete down to 1% density: sparse 100*32=3.2k << dense 80k -> convert.
	kept := 0
	for i := 0; i < n; i++ {
		if v.Get(uint32(i)) != 0 {
			if kept%100 == 0 {
				kept++
				continue
			}
			v.Set(uint32(i), 0)
			kept++
		}
	}
	if v.IsDense() {
		t.Fatal("1% density must downgrade to sparse")
	}
	if v.Len() == 0 {
		t.Fatal("lost all entries during conversion")
	}
}

func TestOutlierIndexForcesSparse(t *testing.T) {
	v := New()
	for i := 0; i < 5000; i++ {
		v.Set(uint32(i), int64(i)+1)
	}
	if !v.IsDense() {
		t.Fatal("expected dense before outlier")
	}
	v.Set(1<<30, 999) // 4 billion span: dense window impossible
	if v.IsDense() {
		t.Fatal("outlier beyond maxDenseSpan must force sparse mode")
	}
	if v.Get(1<<30) != 999 || v.Get(123) != 124 {
		t.Fatal("data lost during outlier fallback")
	}
	if v.Len() != 5001 {
		t.Fatalf("Len = %d, want 5001", v.Len())
	}
}

func collectAll(v *Vector) map[uint32]int64 {
	out := make(map[uint32]int64)
	v.Range(func(idx uint32, val int64) bool {
		out[idx] = val
		return true
	})
	return out
}

func TestCompressRoundTripSparse(t *testing.T) {
	v := New()
	want := map[uint32]int64{}
	for i := 0; i < 5000; i++ {
		idx := uint32(i * 7919)
		val := int64(i*3 - 2000)
		v.Set(idx, val)
		want[idx] = val
	}
	got := mustDecompress(t, v.Compress())
	if len(got) != len(want) {
		t.Fatalf("round trip lost entries: %d vs %d", len(got), len(want))
	}
	for idx, val := range want {
		if got[idx] != val {
			t.Fatalf("round trip mismatch at %d: %d vs %d", idx, got[idx], val)
		}
	}
}

func TestCompressRoundTripDense(t *testing.T) {
	v := New()
	for i := 0; i < 8000; i++ {
		v.Set(uint32(i), int64(i%7+1))
	}
	if !v.IsDense() {
		t.Fatal("expected dense")
	}
	got := mustDecompress(t, v.Compress())
	if len(got) != 8000 {
		t.Fatalf("round trip lost entries: %d", len(got))
	}
	for i := 0; i < 8000; i++ {
		if got[uint32(i)] != int64(i%7+1) {
			t.Fatalf("mismatch at %d", i)
		}
	}
}

func TestRLECompressesRuns(t *testing.T) {
	v := New()
	const n = 200000
	for i := 0; i < n; i++ {
		v.Set(uint32(i), 42) // one giant run of equal values
	}
	comp := v.Compress()
	if comp[0] != encRLE {
		t.Fatalf("constant data should pick RLE, got encoding %d", comp[0])
	}
	if len(comp) > 64 {
		t.Fatalf("RLE of %d equal values took %d bytes, want <= 64", n, len(comp))
	}
	got := mustDecompress(t, comp)
	if len(got) != n {
		t.Fatalf("RLE round trip lost entries: %d", len(got))
	}
	for i := 0; i < n; i += 13337 {
		if got[uint32(i)] != 42 {
			t.Fatalf("RLE round trip mismatch at %d", i)
		}
	}
}

func TestCompressBoundForMassiveSparse(t *testing.T) {
	v := New()
	const n = 1000000
	for i := 0; i < n; i++ {
		v.Set(uint32(i*4), int64(i+1)) // 1M entries, delta=4 -> 1 byte varints
	}
	comp := v.Compress()
	// delta-varint: ~1 byte index + ~1-5 bytes value per entry.
	if len(comp) > n*8 {
		t.Fatalf("compressed %d entries into %d bytes, want <= %d", n, len(comp), n*8)
	}
	got := mustDecompress(t, comp)
	if len(got) != n {
		t.Fatalf("round trip lost entries: %d vs %d", len(got), n)
	}
}

func mustDecompress(t *testing.T, data []byte) map[uint32]int64 {
	v, err := Decompress(data)
	if err != nil {
		t.Fatalf("Decompress: %v", err)
	}
	return collectAll(v)
}

func TestDecompressRejectsGarbage(t *testing.T) {
	cases := [][]byte{
		{},
		{0xFF},
		{encSparseVarint},
		{encSparseVarint, 0x05}, // count but no data
		{encRLE},                // missing header
		{encRLE, 0x00, 0x01},    // span 1 but no runs
		{encSparseVarint, 0x01, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0x01, 0x00}, // idx overflow
	}
	for i, c := range cases {
		if _, err := Decompress(c); err != ErrBadEncoding {
			t.Fatalf("case %d: err = %v, want ErrBadEncoding", i, err)
		}
	}
}

func TestRangeOrderAndEarlyStop(t *testing.T) {
	v := New()
	v.Set(9, 90)
	v.Set(1, 10)
	v.Set(5, 50)
	var order []uint32
	v.Range(func(idx uint32, val int64) bool {
		order = append(order, idx)
		return true
	})
	if len(order) != 3 || order[0] != 1 || order[1] != 5 || order[2] != 9 {
		t.Fatalf("Range order = %v, want [1 5 9]", order)
	}
	count := 0
	v.Range(func(idx uint32, val int64) bool {
		count++
		return false // stop after first
	})
	if count != 1 {
		t.Fatalf("early stop visited %d entries", count)
	}
}

func TestConcurrentReadWrite(t *testing.T) {
	v := New()
	const workers = 8
	const perWorker = 5000
	var wg sync.WaitGroup
	for g := 0; g < workers; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			base := uint32(g * perWorker)
			for i := 0; i < perWorker; i++ {
				v.Set(base+uint32(i), int64(g*perWorker+i)+1)
				v.Get(base + uint32(i))
				if i%100 == 0 {
					v.Len()
					v.MemEstimate()
				}
			}
		}(g)
	}
	// Concurrent compressors/readers on shared state.
	stop := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				v.Compress()
				v.Range(func(uint32, int64) bool { return true })
			}
		}
	}()
	// Wait for writers, then stop the reader.
	done := make(chan struct{})
	go func() {
		for g := 0; g < workers; g++ {
			_ = g
		}
		close(done)
	}()
	_ = done
	wg2 := &sync.WaitGroup{}
	_ = wg2
	// Simple: wait on first wg group via a separate WaitGroup trick is messy;
	// instead just wait for all including reader after closing stop.
	// (writers finish quickly)
	time.Sleep(50 * time.Millisecond)
	close(stop)
	wg.Wait()

	// Every disjoint entry must be present exactly once.
	if v.Len() != workers*perWorker {
		t.Fatalf("Len = %d, want %d", v.Len(), workers*perWorker)
	}
	for g := 0; g < workers; g++ {
		base := uint32(g * perWorker)
		for i := 0; i < perWorker; i += 101 {
			if got := v.Get(base + uint32(i)); got != int64(g*perWorker+i)+1 {
				t.Fatalf("Get(%d) = %d, want %d", base+uint32(i), got, g*perWorker+i+1)
			}
		}
	}
}

func TestNoGoroutineLeak(t *testing.T) {
	before := runtime.NumGoroutine()
	for round := 0; round < 3; round++ {
		v := New()
		var wg sync.WaitGroup
		for g := 0; g < 16; g++ {
			wg.Add(1)
			go func(g int) {
				defer wg.Done()
				for i := 0; i < 1000; i++ {
					v.Set(uint32(g*1000+i), int64(i))
				}
			}(g)
		}
		wg.Wait()
		v.Compress()
	}
	// Allow the runtime a moment to reap finished goroutines.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if runtime.NumGoroutine() <= before {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("goroutine leak: before=%d after=%d", before, runtime.NumGoroutine())
}

func TestNoMemoryLeakAfterRelease(t *testing.T) {
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	for round := 0; round < 5; round++ {
		v := New()
		for i := 0; i < 200000; i++ {
			v.Set(uint32(i), int64(i))
		}
		_ = v.Compress()
	}
	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	// All vectors are garbage: retained heap must not grow by more than a
	// generous constant (10 MB) across rounds.
	growth := int64(after.HeapAlloc) - int64(before.HeapAlloc)
	if growth > 10<<20 {
		t.Fatalf("heap grew by %d bytes after release, possible leak", growth)
	}
}
