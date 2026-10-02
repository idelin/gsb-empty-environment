package roaring

import (
	"math/rand"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestConcurrentReadOps hammers shared bitmaps from many goroutines.
// Run with -race to validate data-race freedom.
func TestConcurrentReadOps(t *testing.T) {
	a := buildMixed(randSource(17), 60)
	b := buildMixed(randSource(23), 60)
	buf := a.Serialize()
	restored, err := Deserialize(buf)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for g := 0; g < 32; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 300; i++ {
				switch (g + i) % 6 {
				case 0:
					_ = a.And(b).Cardinality()
				case 1:
					_ = a.Or(b).Cardinality()
				case 2:
					_ = a.Xor(b).Cardinality()
				case 3:
					_ = a.Serialize()
				case 4:
					_ = restored.Contains(uint32(i * 7919))
				default:
					_ = AndAll(a, b, restored).Cardinality()
				}
			}
		}(g)
	}
	wg.Wait()
}

// TestNoLeak asserts bitmaps are fully garbage-collected (no lingering
// references, no goroutine leaks).
func TestNoLeak(t *testing.T) {
	const n = 64
	var finalized int32
	base := runtime.NumGoroutine()
	for i := 0; i < n; i++ {
		bm := buildMixed(randSource(i), 5)
		_ = bm.Serialize()
		runtime.SetFinalizer(bm, func(*Bitmap) { atomic.AddInt32(&finalized, 1) })
	}
	deadline := time.Now().Add(5 * time.Second)
	for atomic.LoadInt32(&finalized) < n && time.Now().Before(deadline) {
		runtime.GC()
		time.Sleep(10 * time.Millisecond)
	}
	if got := atomic.LoadInt32(&finalized); got != n {
		t.Fatalf("only %d/%d bitmaps finalized: possible memory leak", got, n)
	}
	if g := runtime.NumGoroutine(); g > base+2 {
		t.Fatalf("goroutine count grew from %d to %d: possible leak", base, g)
	}
}

func randSource(seed int) *rand.Rand { return rand.New(rand.NewSource(int64(seed))) }
