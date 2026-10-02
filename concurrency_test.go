package roaring

import (
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

// Concurrent writers on disjoint ID spaces plus concurrent readers;
// run with -race to prove data-race freedom.
func TestConcurrentReadWrite(t *testing.T) {
	b := New()
	const writers = 4
	const perWriter = 5000
	var wwg sync.WaitGroup
	stop := make(chan struct{})
	var rwg sync.WaitGroup
	for i := 0; i < 4; i++ {
		rwg.Add(1)
		go func() {
			defer rwg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				_ = b.Cardinality()
				_ = b.Contains(12345)
				_ = b.Stats()
			}
		}()
	}
	for w := 0; w < writers; w++ {
		wwg.Add(1)
		go func(w int) {
			defer wwg.Done()
			base := uint64(w) << 32
			for i := 0; i < perWriter; i++ {
				b.Add(base + uint64(i))
			}
			b.AddRange(base+perWriter, base+perWriter+1000)
		}(w)
	}
	wwg.Wait()
	close(stop)
	rwg.Wait()
	if got, want := b.Cardinality(), uint64(writers*(perWriter+1000)); got != want {
		t.Fatalf("cardinality = %d, want %d", got, want)
	}
}

// Concurrent set operations on the same bitmap pair, in both lock orders.
func TestConcurrentSetOps(t *testing.T) {
	a, b := New(), New()
	for i := uint64(0); i < 20000; i++ {
		a.Add(i * 2)
		b.Add(i*2 + 1)
	}
	a.AddRange(100000, 200000)
	b.AddRange(150000, 250000)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				switch i % 4 {
				case 0:
					_ = a.And(b)
				case 1:
					_ = b.Or(a) // opposite lock order
				case 2:
					_ = a.Xor(b)
				case 3:
					_ = AndBatch(a, b, a.Clone())
				}
			}
		}(i)
	}
	wg.Wait()
}

// Concurrent Save/Load while the bitmap is being mutated.
func TestConcurrentSaveLoad(t *testing.T) {
	dir := t.TempDir()
	st := NewStorage(filepath.Join(dir, "bitmap.dat"), NewFaultInjector())
	b := sampleBitmap(1)
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		i := uint64(0)
		for {
			select {
			case <-stop:
				return
			default:
			}
			b.Add(i)
			i++
		}
	}()
	for i := 0; i < 10; i++ {
		if err := st.Save(b); err != nil {
			t.Fatalf("Save: %v", err)
		}
		if _, err := st.Load(); err != nil {
			t.Fatalf("Load: %v", err)
		}
	}
	close(stop)
	wg.Wait()
}

// Strict goroutine-leak assertion: after fault-injection saves, crash
// recoveries and lossy transfers, the goroutine count must return to
// the baseline.
func TestNoGoroutineLeak(t *testing.T) {
	baseline := runtime.NumGoroutine()

	// Exercise storage with faults.
	dir := t.TempDir()
	fi := NewFaultInjector()
	st := NewStorage(filepath.Join(dir, "bitmap.dat"), fi)
	v1 := sampleBitmap(1)
	if err := st.Save(v1); err != nil {
		t.Fatalf("Save: %v", err)
	}
	fi.CrashAfterBytes = 100
	_ = st.Save(sampleBitmap(2))
	fi.Reset()
	if _, err := st.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Exercise a lossy transfer (spawns sender/receiver goroutines).
	fi.PacketLossRate = 0.2
	got := runTransfer(t, fi, v1)
	if !got.Equal(v1) {
		t.Fatal("transfer mismatch")
	}

	// Allow the runtime to wind down, then assert no leak.
	deadline := time.Now().Add(3 * time.Second)
	for runtime.NumGoroutine() > baseline && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if after := runtime.NumGoroutine(); after > baseline {
		t.Fatalf("goroutine leak: baseline=%d after=%d", baseline, after)
	}
}
