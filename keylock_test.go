package keylock

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// activeSet is a test checker that records every key currently inside the
// critical section and counts entries that overlap on the same key.
type activeSet struct {
	mu         sync.Mutex
	held       map[string]struct{}
	violations atomic.Int64
}

func newActiveSet() *activeSet {
	return &activeSet{held: make(map[string]struct{})}
}

func (s *activeSet) enter(key string) {
	s.mu.Lock()
	if _, ok := s.held[key]; ok {
		s.violations.Add(1)
	}
	s.held[key] = struct{}{}
	s.mu.Unlock()
}

func (s *activeSet) exit(key string) {
	s.mu.Lock()
	delete(s.held, key)
	s.mu.Unlock()
}

// barrier holds goroutines at a start line until open is called.
type barrier struct {
	ch chan struct{}
}

func newBarrier() *barrier { return &barrier{ch: make(chan struct{})} }
func (b *barrier) wait()   { <-b.ch }
func (b *barrier) open()   { close(b.ch) }

// concurrency tracks how many critical sections are simultaneously active.
type concurrency struct {
	cur atomic.Int64
	max atomic.Int64
}

func (c *concurrency) enter() {
	n := c.cur.Add(1)
	for {
		m := c.max.Load()
		if n <= m || c.max.CompareAndSwap(m, n) {
			return
		}
	}
}

func (c *concurrency) exit() { c.cur.Add(-1) }

// TestCrossGenerationMutualExclusion is the deterministic core case: a
// holder from an old generation is still active while the shard count
// expands; a new acquirer for the same key must block until the old holder
// releases, and both tokens bind to the same owning generation.
func TestCrossGenerationMutualExclusion(t *testing.T) {
	l := New(2)
	ctx := context.Background()

	t1, err := l.Acquire(ctx, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	if got := t1.Generation(); got != 0 {
		t.Fatalf("t1 bound to generation %d, want 0", got)
	}

	l.Expand(4)
	l.Expand(8) // consecutive expansions while t1 is held

	acquired := make(chan *Token, 1)
	go func() {
		tok, err := l.Acquire(ctx, "alpha")
		if err != nil {
			t.Error(err)
			return
		}
		acquired <- tok
	}()

	select {
	case <-acquired:
		t.Fatal("same key entered while old-generation holder was active")
	case <-time.After(50 * time.Millisecond):
	}

	t1.Release()

	select {
	case t2 := <-acquired:
		if got := t2.Generation(); got != 0 {
			t.Fatalf("t2 bound to generation %d, want 0 (entry owned by old generation)", got)
		}
		t2.Release()
	case <-time.After(2 * time.Second):
		t.Fatal("acquirer did not proceed after old holder released")
	}
}

// TestSameKeyUnderExpansion hammers one key from many goroutines while the
// shard count expands repeatedly; the active-set checker must see no
// overlap.
func TestSameKeyUnderExpansion(t *testing.T) {
	l := New(2)
	set := newActiveSet()
	bar := newBarrier()

	const workers = 16
	const rounds = 200

	var done sync.WaitGroup
	for w := 0; w < workers; w++ {
		done.Add(1)
		go func() {
			defer done.Done()
			bar.wait()
			for i := 0; i < rounds; i++ {
				tok, err := l.Acquire(context.Background(), "shared")
				if err != nil {
					t.Error(err)
					return
				}
				set.enter("shared")
				set.exit("shared")
				tok.Release()
			}
		}()
	}

	bar.open()
	for shards := 4; shards <= 64; shards *= 2 {
		l.Expand(shards)
	}
	done.Wait()

	if v := set.violations.Load(); v != 0 {
		t.Fatalf("%d mutual-exclusion violations on the same key", v)
	}
}

// TestDifferentKeysParallel checks that distinct keys proceed concurrently
// even while expansions happen.
func TestDifferentKeysParallel(t *testing.T) {
	l := New(2)
	set := newActiveSet()
	bar := newBarrier()
	var conc concurrency

	const workers = 32
	const hold = 5 * time.Millisecond

	var done sync.WaitGroup
	for w := 0; w < workers; w++ {
		key := fmt.Sprintf("key-%d", w)
		done.Add(1)
		go func() {
			defer done.Done()
			bar.wait()
			for i := 0; i < 20; i++ {
				tok, err := l.Acquire(context.Background(), key)
				if err != nil {
					t.Error(err)
					return
				}
				set.enter(key)
				conc.enter()
				time.Sleep(hold)
				conc.exit()
				set.exit(key)
				tok.Release()
			}
		}()
	}

	bar.open()
	l.Expand(16)
	l.Expand(64)
	done.Wait()

	if v := set.violations.Load(); v != 0 {
		t.Fatalf("%d mutual-exclusion violations across keys", v)
	}
	if m := conc.max.Load(); m < 2 {
		t.Fatalf("different keys did not run in parallel, max concurrency = %d", m)
	}
	t.Logf("max concurrency with %d workers: %d", workers, conc.max.Load())
}

// TestCancelOnlyAffectsWaiting verifies that a cancelled context aborts a
// waiter but never a holder, and that a cancelled waiter fully unregisters.
func TestCancelOnlyAffectsWaiting(t *testing.T) {
	l := New(2)

	holder, err := l.Acquire(context.Background(), "k")
	if err != nil {
		t.Fatal(err)
	}

	// A waiter cancelled while blocked gets the context error.
	ctx, cancel := context.WithCancel(context.Background())
	waitErr := make(chan error, 1)
	go func() {
		_, err := l.Acquire(ctx, "k")
		waitErr <- err
	}()
	time.Sleep(20 * time.Millisecond) // let the waiter block on the semaphore
	cancel()
	if err := <-waitErr; err == nil {
		t.Fatal("cancelled waiter returned nil error")
	}

	// The holder is unaffected by the waiter's cancellation and by its own
	// context being cancelled after acquisition.
	hctx, hcancel := context.WithCancel(context.Background())
	h2, err := l.Acquire(hctx, "other")
	if err != nil {
		t.Fatal(err)
	}
	hcancel()
	blocked := make(chan struct{}, 1)
	go func() {
		tok, err := l.Acquire(context.Background(), "other")
		if err != nil {
			t.Error(err)
			return
		}
		blocked <- struct{}{}
		tok.Release()
	}()
	select {
	case <-blocked:
		t.Fatal("cancellation revoked an already-held lock")
	case <-time.After(50 * time.Millisecond):
	}
	h2.Release()
	<-blocked

	// After the holder releases, the key is free again (the cancelled waiter
	// left no residue).
	holder.Release()
	tok, err := l.Acquire(context.Background(), "k")
	if err != nil {
		t.Fatal(err)
	}
	tok.Release()

	if n := len(l.cur.Load().shards[hashKey("k")%2].entries); n != 0 {
		t.Fatalf("entry table not empty after all releases, %d entries left", n)
	}
}

// TestReleaseBoundToAcquisitionGeneration releases tokens through old
// generations after one and several consecutive expansions, and checks the
// entries are cleaned up in the generation that owns them.
func TestReleaseBoundToAcquisitionGeneration(t *testing.T) {
	l := New(2)

	t1, err := l.Acquire(context.Background(), "beta")
	if err != nil {
		t.Fatal(err)
	}
	l.Expand(4)

	t2, err := l.Acquire(context.Background(), "gamma")
	if err != nil {
		t.Fatal(err)
	}
	l.Expand(8)
	l.Expand(16) // consecutive expansions with live tokens in gen 0 and 1

	if got := t1.Generation(); got != 0 {
		t.Fatalf("t1 bound to generation %d, want 0", got)
	}
	if got := t2.Generation(); got != 1 {
		t.Fatalf("t2 bound to generation %d, want 1", got)
	}

	// Releasing through the old bindings must work and must clean up the
	// owning generations' tables, not the current one.
	t1.Release()
	t2.Release()

	for g := l.cur.Load(); g != nil; g = g.prev {
		for si := range g.shards {
			if n := len(g.shards[si].entries); n != 0 {
				t.Fatalf("generation %d shard %d still has %d entries", g.id, si, n)
			}
		}
	}

	// A fresh acquisition creates its entry in the current generation.
	t3, err := l.Acquire(context.Background(), "beta")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := t3.Generation(), l.Generation(); got != want {
		t.Fatalf("t3 bound to generation %d, want current generation %d", got, want)
	}
	t3.Release()
}

// TestConsecutiveExpansionsUnderLoad runs a mixed-key workload across many
// back-to-back expansions; the checker must see no overlap and every token
// must release cleanly through its bound generation.
func TestConsecutiveExpansionsUnderLoad(t *testing.T) {
	l := New(2)
	set := newActiveSet()
	bar := newBarrier()

	const workers = 24
	const keys = 6
	const rounds = 100

	var done sync.WaitGroup
	for w := 0; w < workers; w++ {
		done.Add(1)
		go func(id int) {
			defer done.Done()
			bar.wait()
			for i := 0; i < rounds; i++ {
				key := fmt.Sprintf("k%d", (id+i)%keys)
				tok, err := l.Acquire(context.Background(), key)
				if err != nil {
					t.Error(err)
					return
				}
				set.enter(key)
				set.exit(key)
				tok.Release()
			}
		}(w)
	}

	bar.open()
	for shards := 3; shards <= 96; shards += 3 {
		l.Expand(shards)
	}
	done.Wait()

	if v := set.violations.Load(); v != 0 {
		t.Fatalf("%d mutual-exclusion violations under consecutive expansions", v)
	}
	for g := l.cur.Load(); g != nil; g = g.prev {
		for si := range g.shards {
			if n := len(g.shards[si].entries); n != 0 {
				t.Fatalf("generation %d shard %d leaked %d entries", g.id, si, n)
			}
		}
	}
}

// TestDoubleReleasePanics guards the token's single-use contract.
func TestDoubleReleasePanics(t *testing.T) {
	l := New(2)
	tok, err := l.Acquire(context.Background(), "x")
	if err != nil {
		t.Fatal(err)
	}
	tok.Release()
	defer func() {
		if recover() == nil {
			t.Fatal("second Release did not panic")
		}
	}()
	tok.Release()
}
