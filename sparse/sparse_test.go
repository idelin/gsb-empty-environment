package sparse

import (
	"math/rand"
	"runtime"
	"sync"
	"testing"
	"time"
)

func TestEmpty(t *testing.T) {
	v := New()
	if v.Count() != 0 {
		t.Fatal("empty vector count != 0")
	}
	if _, ok := v.Get(0); ok {
		t.Fatal("get on empty vector")
	}
	if _, ok := v.Get(^uint64(0)); ok {
		t.Fatal("get on empty vector")
	}
	if v.Density() != 0 {
		t.Fatal("empty density != 0")
	}
	if v.Mode() != "sparse" {
		t.Fatalf("mode = %s", v.Mode())
	}
	v.Delete(42) // no-op, must not panic
	calls := 0
	v.ForEach(func(uint64, int64) bool { calls++; return true })
	if calls != 0 {
		t.Fatal("ForEach on empty vector")
	}
	v.Compact() // must not panic on empty
	if v.Count() != 0 {
		t.Fatal("count after compact")
	}
}

func TestBasicSetGetDelete(t *testing.T) {
	v := New()
	v.Set(10, 100)
	v.Set(20, 200)
	v.Set(30, -300)
	if got, ok := v.Get(10); !ok || got != 100 {
		t.Fatalf("Get(10) = %d,%v", got, ok)
	}
	if got, ok := v.Get(30); !ok || got != -300 {
		t.Fatalf("Get(30) = %d,%v", got, ok)
	}
	if _, ok := v.Get(15); ok {
		t.Fatal("Get(15) should miss")
	}
	v.Set(10, 111) // overwrite
	if got, _ := v.Get(10); got != 111 {
		t.Fatalf("overwrite: got %d", got)
	}
	if v.Count() != 3 {
		t.Fatalf("count = %d", v.Count())
	}
	v.Delete(20)
	if _, ok := v.Get(20); ok {
		t.Fatal("delete failed")
	}
	if v.Count() != 2 {
		t.Fatalf("count after delete = %d", v.Count())
	}
	v.Delete(20) // double delete is a no-op
	if v.Count() != 2 {
		t.Fatal("double delete changed count")
	}
}

func TestExtremeIndices(t *testing.T) {
	v := New()
	const maxU = ^uint64(0)
	v.Set(0, 1)
	v.Set(maxU, -1)
	v.Set(maxU/2, 7)
	if got, ok := v.Get(maxU); !ok || got != -1 {
		t.Fatalf("Get(max) = %d,%v", got, ok)
	}
	if got, ok := v.Get(0); !ok || got != 1 {
		t.Fatalf("Get(0) = %d,%v", got, ok)
	}
	// Span covers almost the whole uint64 space: must stay sparse and
	// must never attempt a dense conversion (would not fit in memory).
	if v.Mode() != "sparse" {
		t.Fatalf("mode = %s, want sparse", v.Mode())
	}
	if d := v.Density(); d <= 0 || d > 1e-18 {
		t.Fatalf("density = %g", d)
	}
	keys := v.SortKeys()
	if len(keys) != 3 || keys[0] != 0 || keys[2] != maxU {
		t.Fatalf("keys = %v", keys)
	}
}

func TestDenseConversion(t *testing.T) {
	v := New()
	for i := uint64(0); i < 1000; i++ {
		v.Set(i, int64(i)*3)
	}
	if v.Mode() != "dense" {
		t.Fatalf("mode = %s, want dense", v.Mode())
	}
	if d := v.Density(); d != 1 {
		t.Fatalf("density = %g, want 1", d)
	}
	for i := uint64(0); i < 1000; i++ {
		if got, ok := v.Get(i); !ok || got != int64(i)*3 {
			t.Fatalf("Get(%d) = %d,%v", i, got, ok)
		}
	}
}

func TestSparseConversionOnDelete(t *testing.T) {
	v := New()
	for i := uint64(0); i < 1000; i++ {
		v.Set(i, int64(i))
	}
	if v.Mode() != "dense" {
		t.Fatalf("mode = %s, want dense", v.Mode())
	}
	// Delete 90% of the keys but keep them spread across the whole span
	// (every 10th survives): sparse representation becomes far cheaper.
	for i := uint64(0); i < 1000; i++ {
		if i%10 != 0 {
			v.Delete(i)
		}
	}
	if v.Mode() != "sparse" {
		t.Fatalf("mode = %s, want sparse", v.Mode())
	}
	for i := uint64(0); i < 1000; i += 10 {
		if got, ok := v.Get(i); !ok || got != int64(i) {
			t.Fatalf("Get(%d) = %d,%v after conversion", i, got, ok)
		}
	}
	if v.Count() != 100 {
		t.Fatalf("count = %d", v.Count())
	}
}

func TestDenseContainerGrowth(t *testing.T) {
	// White-box: exercise dense grow in both directions incl. bitmap shifts.
	d := &denseContainer{}
	for i := uint64(5); i <= 10; i++ {
		d.set(i, int64(i))
	}
	d.set(2, 22)   // prepend (unaligned shift)
	d.set(70, 770) // append across a word boundary
	d.set(0, 11)   // prepend to zero
	for i := uint64(5); i <= 10; i++ {
		if got, ok := d.get(i); !ok || got != int64(i) {
			t.Fatalf("get(%d) = %d,%v", i, got, ok)
		}
	}
	if got, ok := d.get(2); !ok || got != 22 {
		t.Fatalf("get(2) = %d,%v", got, ok)
	}
	if got, ok := d.get(70); !ok || got != 770 {
		t.Fatalf("get(70) = %d,%v", got, ok)
	}
	if got, ok := d.get(0); !ok || got != 11 {
		t.Fatalf("get(0) = %d,%v", got, ok)
	}
	if d.count() != 9 {
		t.Fatalf("count = %d, want 9", d.count())
	}
	d.del(70)
	if _, ok := d.get(70); ok {
		t.Fatal("del(70) failed")
	}
	lo, hi, ok := d.span()
	if !ok || lo != 0 || hi != 10 {
		t.Fatalf("span = %d,%d,%v", lo, hi, ok)
	}
}

func TestCompressRoundtrip(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 50; trial++ {
		n := rng.Intn(500)
		var keys []uint64
		var vals []int64
		cur := uint64(rng.Intn(1000))
		for i := 0; i < n; i++ {
			cur += uint64(rng.Intn(1 << 20))
			keys = append(keys, cur)
			vals = append(vals, int64(rng.Int63()-1<<62))
		}
		packed := compress(keys, vals)
		gk, gv := decompress(packed)
		if len(gk) != len(keys) || len(gv) != len(vals) {
			t.Fatalf("trial %d: roundtrip length mismatch", trial)
		}
		for i := range keys {
			if gk[i] != keys[i] || gv[i] != vals[i] {
				t.Fatalf("trial %d entry %d: got (%d,%d) want (%d,%d)",
					trial, i, gk[i], gv[i], keys[i], vals[i])
			}
		}
		// Spot-check packed accessors.
		if len(keys) > 0 {
			lo, hi, ok := packedSpan(packed)
			if !ok || lo != keys[0] || hi != keys[len(keys)-1] {
				t.Fatalf("packedSpan = %d,%d,%v", lo, hi, ok)
			}
			pick := keys[rng.Intn(len(keys))]
			if _, found := packedGet(packed, pick); !found {
				t.Fatalf("packedGet(%d) missed", pick)
			}
			if _, found := packedGet(packed, pick+1<<40); found {
				t.Fatal("packedGet found nonexistent key")
			}
		}
	}
	// Empty stream edge cases.
	if k, v := decompress(nil); k != nil || v != nil {
		t.Fatal("decompress(nil) should be empty")
	}
	if _, _, ok := packedSpan(nil); ok {
		t.Fatal("packedSpan(nil)")
	}
	if _, ok := packedGet(nil, 7); ok {
		t.Fatal("packedGet(nil)")
	}
}

func TestCompactReducesMemory(t *testing.T) {
	v := New()
	rng := rand.New(rand.NewSource(7))
	const n = 50000
	const span = uint64(1e9)
	for i := 0; i < n; i++ {
		v.Set(uint64(rng.Int63n(int64(span))), int64(i))
	}
	if v.Mode() == "dense" {
		t.Fatal("100k entries over 1e9 span must stay sparse")
	}
	before := v.Bytes()
	// A dense array over this span would need ~8 GB; sparse must be tiny.
	if before > 32<<20 {
		t.Fatalf("sparse footprint %d bytes exceeds 32 MiB", before)
	}
	v.Compact()
	if v.Mode() != "sparse-packed" {
		t.Fatalf("mode = %s, want sparse-packed", v.Mode())
	}
	packed := v.Bytes()
	if packed >= before {
		t.Fatalf("packed %d >= unpacked %d", packed, before)
	}
	if packed > 4<<20 {
		t.Fatalf("packed footprint %d exceeds 4 MiB", packed)
	}
	// Data intact after compaction: verify a sample of inserted keys
	// (packedGet is an O(n) scan by design; packed is cold storage).
	rng2 := rand.New(rand.NewSource(7))
	checked := 0
	for i := 0; i < n; i++ {
		k := uint64(rng2.Int63n(int64(span)))
		if i%100 != 0 {
			continue
		}
		if _, ok := v.Get(k); !ok {
			t.Fatalf("key %d lost after compaction", k)
		}
		checked++
	}
	if checked != n/100 {
		t.Fatalf("checked %d samples", checked)
	}
	// Exhaustive verification via ForEach (streaming, no per-key scan).
	if c := v.Count(); c <= 0 {
		t.Fatal("count after compact")
	}
	streamed := 0
	v.ForEach(func(k uint64, val int64) bool { streamed++; return true })
	if streamed != v.Count() {
		t.Fatalf("streamed %d, count %d", streamed, v.Count())
	}
	// Mutation after compaction transparently unpacks.
	v.Set(5, -5)
	if got, ok := v.Get(5); !ok || got != -5 {
		t.Fatalf("post-compact set: %d,%v", got, ok)
	}
	if v.Mode() != "sparse" {
		t.Fatalf("mode after mutation = %s", v.Mode())
	}
}

func TestForEachOrder(t *testing.T) {
	v := New()
	keys := []uint64{50, 3, 17, 999, 1, 42}
	for _, k := range keys {
		v.Set(k, int64(k))
	}
	var got []uint64
	v.ForEach(func(k uint64, val int64) bool {
		got = append(got, k)
		if int64(k) != val {
			t.Errorf("value mismatch at %d", k)
		}
		return true
	})
	want := []uint64{1, 3, 17, 42, 50, 999}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
	// Early stop.
	count := 0
	v.ForEach(func(uint64, int64) bool { count++; return false })
	if count != 1 {
		t.Fatalf("early stop: %d iterations", count)
	}
}

func TestConcurrentMixed(t *testing.T) {
	v := New()
	const writers = 8
	const perWriter = 5000
	var wg sync.WaitGroup

	// Writers on disjoint key ranges.
	for g := 0; g < writers; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			base := uint64(g) * 1e6
			for k := 0; k < perWriter; k++ {
				v.Set(base+uint64(k), int64(g*perWriter+k))
			}
		}(g)
	}
	// Concurrent readers + compactors.
	stop := make(chan struct{})
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				v.Get(uint64(writers/2) * 1e6)
				v.Count()
				v.Bytes()
				v.Density()
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 5; i++ {
			v.Compact()
			time.Sleep(time.Millisecond)
		}
	}()

	// Wait for writers, then stop readers.
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	// The first `writers` goroutines finish; then close stop.
	// (wg includes readers, so poll Count instead.)
	for v.Count() < writers*perWriter {
		time.Sleep(time.Millisecond)
	}
	close(stop)
	<-done

	if v.Count() != writers*perWriter {
		t.Fatalf("count = %d, want %d", v.Count(), writers*perWriter)
	}
	for g := 0; g < writers; g++ {
		base := uint64(g) * 1e6
		for k := 0; k < perWriter; k += 499 {
			want := int64(g*perWriter + k)
			if got, ok := v.Get(base + uint64(k)); !ok || got != want {
				t.Fatalf("Get(%d) = %d,%v want %d", base+uint64(k), got, ok, want)
			}
		}
	}
}

func TestConcurrentDeleteConsistency(t *testing.T) {
	v := New()
	const n = 20000
	for i := 0; i < n; i++ {
		v.Set(uint64(i), int64(i))
	}
	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := g; i < n; i += 4 {
				v.Delete(uint64(i))
			}
		}(g)
	}
	wg.Wait()
	if v.Count() != 0 {
		t.Fatalf("count = %d, want 0", v.Count())
	}
	if v.Mode() != "sparse" {
		t.Fatalf("mode = %s, want sparse after draining", v.Mode())
	}
}

func TestNoGoroutineLeak(t *testing.T) {
	before := runtime.NumGoroutine()
	func() {
		v := New()
		for i := 0; i < 10000; i++ {
			v.Set(uint64(i)*97, int64(i))
		}
		v.Compact()
		for i := 0; i < 10000; i += 2 {
			v.Delete(uint64(i) * 97)
		}
	}()
	runtime.GC()
	time.Sleep(20 * time.Millisecond)
	if after := runtime.NumGoroutine(); after > before {
		t.Fatalf("goroutine leak: before=%d after=%d", before, after)
	}
}
