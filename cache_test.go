package cache

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// barrierLoader blocks until released, letting tests control the order in
// which loads complete.
type barrierLoader struct {
	mu      sync.Mutex
	started chan string
	release map[string]chan struct{}
	values  map[string][]byte
	calls   int32
}

func newBarrierLoader() *barrierLoader {
	return &barrierLoader{
		started: make(chan string, 64),
		release: make(map[string]chan struct{}),
		values:  make(map[string][]byte),
	}
}

func (b *barrierLoader) arm(key string, val []byte) {
	b.mu.Lock()
	b.release[key] = make(chan struct{})
	b.values[key] = val
	b.mu.Unlock()
}

func (b *barrierLoader) load(ctx context.Context, key string, _ uint64) ([]byte, error) {
	atomic.AddInt32(&b.calls, 1)
	b.mu.Lock()
	release := b.release[key]
	val := b.values[key]
	b.mu.Unlock()
	b.started <- key
	select {
	case <-release:
		return val, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (b *barrierLoader) releaseKey(key string) {
	b.mu.Lock()
	close(b.release[key])
	b.mu.Unlock()
}

func waitFor(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for: %s", msg)
}

func TestConcurrentMissMerge(t *testing.T) {
	bl := newBarrierLoader()
	bl.arm("k", []byte("value"))
	c := New(1<<20, WithLoader(bl.load), WithLoadReservation(4))

	const n = 50
	var wg sync.WaitGroup
	results := make([][]byte, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			v, err := c.Get(context.Background(), "k", 1)
			results[i], errs[i] = v, err
		}(i)
	}
	<-bl.started // ensure the single flight is in flight
	// Release only after every waiter has joined the flight.
	waitFor(t, func() bool { return c.Stats().Misses == n }, "all waiters joined")
	bl.releaseKey("k")
	wg.Wait()

	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatalf("waiter %d: %v", i, errs[i])
		}
		if string(results[i]) != "value" {
			t.Fatalf("waiter %d got %q", i, results[i])
		}
	}
	s := c.Stats()
	if s.Loads != 1 {
		t.Fatalf("loads = %d, want 1", s.Loads)
	}
	if s.Misses != n {
		t.Fatalf("misses = %d, want %d", s.Misses, n)
	}
	if s.Reserved != 0 {
		t.Fatalf("reserved = %d, want 0", s.Reserved)
	}
	if s.Resident != 5 {
		t.Fatalf("resident = %d, want 5", s.Resident)
	}
	if got := atomic.LoadInt32(&bl.calls); got != 1 {
		t.Fatalf("loader invoked %d times, want 1", got)
	}
	// A second Get hits the resident entry.
	if _, err := c.Get(context.Background(), "k", 1); err != nil {
		t.Fatal(err)
	}
	if s := c.Stats(); s.Hits != 1 {
		t.Fatalf("hits = %d, want 1", s.Hits)
	}
}

func TestOversizedValue(t *testing.T) {
	bl := newBarrierLoader()
	bl.arm("big", make([]byte, 1000))
	c := New(64, WithLoader(bl.load), WithLoadReservation(8))

	done := make(chan error, 1)
	go func() {
		_, err := c.Get(context.Background(), "big", 1)
		done <- err
	}()
	<-bl.started
	bl.releaseKey("big")
	if err := <-done; err != ErrTooLarge {
		t.Fatalf("got %v, want ErrTooLarge", err)
	}
	s := c.Stats()
	if s.Reserved != 0 || s.Resident != 0 {
		t.Fatalf("reserved=%d resident=%d, want both 0", s.Reserved, s.Resident)
	}

	// Set with cost above the budget fails without touching occupancy.
	if err := c.Set("big", 1, make([]byte, 1000)); err != ErrTooLarge {
		t.Fatalf("set: got %v, want ErrTooLarge", err)
	}
	if s := c.Stats(); s.Resident != 0 {
		t.Fatalf("resident = %d after failed set, want 0", s.Resident)
	}
}

func TestReservationBlocksConcurrentLoads(t *testing.T) {
	bl := newBarrierLoader()
	bl.arm("a", []byte("aaaa"))
	bl.arm("b", []byte("bbbb"))
	c := New(10, WithLoader(bl.load), WithLoadReservation(6))

	first := make(chan error, 1)
	go func() {
		_, err := c.Get(context.Background(), "a", 1)
		first <- err
	}()
	<-bl.started // "a" now holds 6 of the 10 units

	// The second load cannot reserve 6 of the remaining 4.
	if _, err := c.Get(context.Background(), "b", 1); err != ErrQuota {
		t.Fatalf("got %v, want ErrQuota", err)
	}
	bl.releaseKey("a")
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	s := c.Stats()
	if s.Reserved != 0 || s.Resident != 4 {
		t.Fatalf("reserved=%d resident=%d, want 0 and 4", s.Reserved, s.Resident)
	}
	if s.Loads != 1 {
		t.Fatalf("loads = %d, want 1 (second load never started)", s.Loads)
	}
}

func TestStaleLoadDoesNotOverwrite(t *testing.T) {
	bl := newBarrierLoader()
	bl.arm("k", []byte("stale"))
	c := New(1<<20, WithLoader(bl.load), WithLoadReservation(1))

	loaded := make(chan []byte, 1)
	go func() {
		v, _ := c.Get(context.Background(), "k", 1)
		loaded <- v
	}()
	<-bl.started

	// Overwrite the key while the load is in flight.
	if err := c.Set("k", 1, []byte("fresh")); err != nil {
		t.Fatal(err)
	}
	bl.releaseKey("k")

	// The waiter still receives what was loaded...
	if v := <-loaded; string(v) != "stale" {
		t.Fatalf("waiter got %q, want the loaded value", v)
	}
	// ...but the stale result must never replace the newer value.
	v, err := c.Get(context.Background(), "k", 1)
	if err != nil {
		t.Fatal(err)
	}
	if string(v) != "fresh" {
		t.Fatalf("cache holds %q, want %q", v, "fresh")
	}
	s := c.Stats()
	if s.Resident != 5 || s.Reserved != 0 {
		t.Fatalf("resident=%d reserved=%d, want 5 and 0", s.Resident, s.Reserved)
	}
}

func TestAllWaitersCancel(t *testing.T) {
	started := make(chan struct{}, 1)
	loader := func(ctx context.Context, key string, _ uint64) ([]byte, error) {
		started <- struct{}{}
		<-ctx.Done() // observe cancellation of the underlying load
		return nil, ctx.Err()
	}
	c := New(64, WithLoader(loader), WithLoadReservation(8))

	ctx1, cancel1 := context.WithCancel(context.Background())
	ctx2, cancel2 := context.WithCancel(context.Background())
	errs := make(chan error, 2)
	go func() { _, err := c.Get(ctx1, "k", 1); errs <- err }()
	<-started
	go func() { _, err := c.Get(ctx2, "k", 1); errs <- err }()
	waitFor(t, func() bool { return c.Stats().Reserved == 8 }, "reservation visible")
	// Wait until waiter 2 has actually joined the flight.
	waitFor(t, func() bool { return c.Stats().Misses == 2 }, "second waiter joined")

	// One waiter leaving must not cancel the load for the other.
	cancel1()
	if err := <-errs; err != context.Canceled {
		t.Fatalf("waiter 1: got %v, want context.Canceled", err)
	}
	if s := c.Stats(); s.Reserved != 8 {
		t.Fatalf("reserved = %d after one cancel, want 8 (load still running)", s.Reserved)
	}

	// Once the last waiter leaves, the underlying load is cancelled.
	cancel2()
	if err := <-errs; err != context.Canceled {
		t.Fatalf("waiter 2: got %v, want context.Canceled", err)
	}
	waitFor(t, func() bool { return c.Stats().Reserved == 0 }, "reservation released")

	s := c.Stats()
	if s.Cancels != 2 {
		t.Fatalf("cancels = %d, want 2", s.Cancels)
	}
	if s.Loads != 1 || s.Misses != 2 {
		t.Fatalf("loads=%d misses=%d, want 1 and 2", s.Loads, s.Misses)
	}
	if s.Resident != 0 {
		t.Fatalf("resident = %d, want 0", s.Resident)
	}
}

func TestEvictionReleasesOnlyRemoved(t *testing.T) {
	c := New(10, WithShards(1)) // single shard: deterministic LRU
	for i := 0; i < 15; i++ {
		if err := c.Set(fmt.Sprintf("k%02d", i), 1, []byte{byte(i)}); err != nil {
			t.Fatal(err)
		}
	}
	s := c.Stats()
	if s.Evicts != 5 {
		t.Fatalf("evicts = %d, want 5", s.Evicts)
	}
	if s.Resident != 10 {
		t.Fatalf("resident = %d, want 10 (only removed entries released)", s.Resident)
	}
	// The five oldest entries are gone, the ten newest remain.
	for i := 0; i < 5; i++ {
		if _, err := c.Get(context.Background(), fmt.Sprintf("k%02d", i), 1); err != ErrNoLoader {
			t.Fatalf("k%02d: got %v, want miss", i, err)
		}
	}
	for i := 5; i < 15; i++ {
		v, err := c.Get(context.Background(), fmt.Sprintf("k%02d", i), 1)
		if err != nil {
			t.Fatalf("k%02d: %v", i, err)
		}
		if len(v) != 1 || int(v[0]) != i {
			t.Fatalf("k%02d: wrong value %v", i, v)
		}
	}
	if s := c.Stats(); s.Resident != 10 {
		t.Fatalf("resident = %d after reads, want 10", s.Resident)
	}
}

func TestIndependentLedger(t *testing.T) {
	bl := newBarrierLoader()
	c := New(1<<20, WithShards(8), WithLoader(bl.load), WithLoadReservation(4))

	var mu sync.Mutex
	ledger := make(map[string]int64) // keyver -> cost, recomputed independently

	const workers = 8
	const ops = 200
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < ops; i++ {
				key := fmt.Sprintf("w%d-k%d", w, i)
				if i%2 == 0 {
					val := make([]byte, i%13+1)
					if err := c.Set(key, 1, val); err != nil {
						t.Error(err)
						return
					}
					mu.Lock()
					ledger[key] = int64(len(val))
					mu.Unlock()
				} else {
					val := make([]byte, i%7+1)
					bl.arm(key, val)
					if _, err := c.Get(context.Background(), key, 1); err != nil {
						t.Error(err)
						return
					}
					mu.Lock()
					ledger[key] = int64(len(val))
					mu.Unlock()
				}
			}
		}(w)
	}
	// Release loads as they start so completion order is shuffled.
	go func() {
		for key := range bl.started {
			bl.releaseKey(key)
		}
	}()
	wg.Wait()

	var want int64
	mu.Lock()
	for _, cost := range ledger {
		want += cost
	}
	mu.Unlock()

	s := c.Stats()
	if s.Resident != want {
		t.Fatalf("resident = %d, ledger says %d", s.Resident, want)
	}
	if s.Reserved != 0 {
		t.Fatalf("reserved = %d, want 0", s.Reserved)
	}
	if s.Loads != uint64(workers*ops/2) {
		t.Fatalf("loads = %d, want %d", s.Loads, workers*ops/2)
	}
	if s.Evicts != 0 {
		t.Fatalf("evicts = %d, want 0", s.Evicts)
	}
}

func TestStatsConcurrentWithEviction(t *testing.T) {
	bl := newBarrierLoader()
	c := New(64, WithShards(4), WithLoader(bl.load), WithLoadReservation(4))

	stop := make(chan struct{})
	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			i := 0
			for {
				select {
				case <-stop:
					return
				default:
				}
				key := fmt.Sprintf("w%d-k%d", w, i%32)
				if i%3 == 0 {
					bl.arm(key, []byte{byte(i)})
					c.Get(context.Background(), key, 1) // nolint: errcheck
				} else {
					c.Set(key, 1, []byte{byte(i), byte(i >> 8)}) // nolint: errcheck
				}
				i++
			}
		}(w)
	}
	// Drain the barrier so loads never block the writers.
	go func() {
		for key := range bl.started {
			bl.releaseKey(key)
		}
	}()

	// Reader: every snapshot must satisfy the budget invariants.
	deadline := time.Now().Add(300 * time.Millisecond)
	for time.Now().Before(deadline) {
		s := c.Stats()
		if s.Resident < 0 || s.Reserved < 0 {
			t.Fatalf("negative gauge: %+v", s)
		}
		if s.Resident+s.Reserved > 64 {
			t.Fatalf("budget exceeded: %+v", s)
		}
	}
	close(stop)
	wg.Wait()
}

func TestImmutableCopies(t *testing.T) {
	c := New(1 << 10)
	in := []byte("abc")
	if err := c.Set("k", 1, in); err != nil {
		t.Fatal(err)
	}
	in[0] = 'X' // mutating the caller's slice must not affect the cache

	v1, err := c.Get(context.Background(), "k", 1)
	if err != nil {
		t.Fatal(err)
	}
	if string(v1) != "abc" {
		t.Fatalf("got %q, want %q", v1, "abc")
	}
	v1[0] = 'Y' // mutating a returned slice must not affect the cache
	v2, err := c.Get(context.Background(), "k", 1)
	if err != nil {
		t.Fatal(err)
	}
	if string(v2) != "abc" {
		t.Fatalf("got %q after mutation, want %q", v2, "abc")
	}
}
