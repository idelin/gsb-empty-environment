package batcher

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

// fakeClock is a manually advanced Clock injected by the tests.
type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []*fakeTimer
}

type fakeTimer struct {
	at      time.Time
	ch      chan time.Time
	fired   bool
	stopped bool
}

func newFakeClock() *fakeClock { return &fakeClock{now: time.Unix(1000, 0)} }

func (c *fakeClock) NewTimer(d time.Duration) (<-chan time.Time, func() bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &fakeTimer{at: c.now.Add(d), ch: make(chan time.Time, 1)}
	c.timers = append(c.timers, t)
	return t.ch, func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		if t.fired || t.stopped {
			return false
		}
		t.stopped = true
		return true
	}
}

// Advance moves the clock forward and fires every timer whose deadline has
// been reached. Firing happens outside the lock, like real timer delivery.
func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	var fire []*fakeTimer
	for _, t := range c.timers {
		if !t.fired && !t.stopped && !t.at.After(c.now) {
			t.fired = true
			fire = append(fire, t)
		}
	}
	now := c.now
	c.mu.Unlock()
	for _, t := range fire {
		t.ch <- now
	}
}

// execSpy records every batch the execute function receives.
type execSpy struct {
	mu    sync.Mutex
	calls [][]int
	err   error         // injected failure, returned to every batch
	gate  chan struct{} // if non-nil, each call blocks until it is closed/receives
}

func (s *execSpy) exec(_ context.Context, items []int) ([]int, error) {
	s.mu.Lock()
	s.calls = append(s.calls, append([]int(nil), items...))
	err := s.err
	s.mu.Unlock()
	if s.gate != nil {
		<-s.gate
	}
	if err != nil {
		return nil, err
	}
	out := make([]int, len(items))
	for i, v := range items {
		out[i] = v * 10
	}
	return out, nil
}

func (s *execSpy) batches() [][]int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([][]int(nil), s.calls...)
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

type result[O any] struct {
	val O
	err error
}

type submitResult = result[int]

func submitAsync[I, O any](b *Batcher[I, O], ctx context.Context, item I) <-chan result[O] {
	ch := make(chan result[O], 1)
	go func() {
		v, err := b.Submit(ctx, item)
		ch <- result[O]{v, err}
	}()
	return ch
}

func pendingLen[I, O any](b *Batcher[I, O]) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.pending == nil {
		return 0
	}
	return len(b.pending.reqs)
}

func TestFlushOnMaxSize(t *testing.T) {
	clock := newFakeClock()
	spy := &execSpy{}
	b := New(3, time.Hour, spy.exec, clock)
	defer b.Close()

	rs := []<-chan submitResult{
		submitAsync(b, context.Background(), 1),
		submitAsync(b, context.Background(), 2),
		submitAsync(b, context.Background(), 3),
	}
	for i, r := range rs {
		got := <-r
		if got.err != nil {
			t.Fatalf("submit %d: unexpected error: %v", i, got.err)
		}
		if want := (i + 1) * 10; got.val != want {
			t.Fatalf("submit %d: got %d, want %d", i, got.val, want)
		}
	}
	calls := spy.batches()
	if len(calls) != 1 || len(calls[0]) != 3 {
		t.Fatalf("expected exactly one batch of 3 items, got %v", calls)
	}
	// Size trigger must have stopped the wait timer: advancing the clock
	// must not produce a second execution.
	clock.Advance(2 * time.Hour)
	waitFor(t, "no extra flush", func() bool { return len(spy.batches()) == 1 })
}

func TestFlushOnTimeoutWhenNotFull(t *testing.T) {
	clock := newFakeClock()
	spy := &execSpy{}
	b := New(10, 50*time.Millisecond, spy.exec, clock)
	defer b.Close()

	r1 := submitAsync(b, context.Background(), 7)
	waitFor(t, "1 pending item", func() bool { return pendingLen(b) == 1 })
	r2 := submitAsync(b, context.Background(), 8)
	waitFor(t, "2 pending items", func() bool { return pendingLen(b) == 2 })

	// Batch is not full, but the wait deadline of the first item passes.
	clock.Advance(50 * time.Millisecond)

	for i, r := range []<-chan submitResult{r1, r2} {
		got := <-r
		if got.err != nil {
			t.Fatalf("submit %d: unexpected error: %v", i, got.err)
		}
	}
	calls := spy.batches()
	if len(calls) != 1 || fmt.Sprint(calls[0]) != "[7 8]" {
		t.Fatalf("expected one batch [7 8], got %v", calls)
	}
}

func TestCancelledItemRemovedFromBatch(t *testing.T) {
	clock := newFakeClock()
	spy := &execSpy{}
	b := New(2, time.Hour, spy.exec, clock)
	defer b.Close()

	ctx, cancel := context.WithCancel(context.Background())
	r1 := submitAsync(b, ctx, 1)
	waitFor(t, "1 pending item", func() bool { return pendingLen(b) == 1 })
	cancel()
	if got := <-r1; !errors.Is(got.err, context.Canceled) {
		t.Fatalf("cancelled submit: got %v, want context.Canceled", got.err)
	}
	waitFor(t, "cancelled item removed", func() bool { return pendingLen(b) == 0 })

	// The cancelled slot must not count toward the batch: item 2 alone
	// does not reach maxSize, so only the timer flushes it.
	r2 := submitAsync(b, context.Background(), 2)
	waitFor(t, "item 2 pending", func() bool { return pendingLen(b) == 1 })
	clock.Advance(time.Hour)
	if got := <-r2; got.err != nil || got.val != 20 {
		t.Fatalf("submit 2: got (%d, %v), want (20, nil)", got.val, got.err)
	}

	calls := spy.batches()
	if len(calls) != 1 || fmt.Sprint(calls[0]) != "[2]" {
		t.Fatalf("cancelled item must not reach exec: got batches %v", calls)
	}
}

func TestAllCancelledNeverCallsExec(t *testing.T) {
	clock := newFakeClock()
	spy := &execSpy{}
	b := New(4, time.Minute, spy.exec, clock)
	defer b.Close()

	ctx1, cancel1 := context.WithCancel(context.Background())
	ctx2, cancel2 := context.WithCancel(context.Background())
	r1 := submitAsync(b, ctx1, 1)
	r2 := submitAsync(b, ctx2, 2)
	waitFor(t, "2 pending items", func() bool { return pendingLen(b) == 2 })
	cancel1()
	cancel2()
	<-r1
	<-r2

	clock.Advance(2 * time.Minute)
	waitFor(t, "batch to drain", func() bool { return pendingLen(b) == 0 })
	// Give a (notional) flush goroutine a chance to run, then assert.
	b.Close()
	if calls := spy.batches(); len(calls) != 0 {
		t.Fatalf("exec must not run for an all-cancelled batch, got %v", calls)
	}
}

func TestExecErrorMapsToEverySurvivor(t *testing.T) {
	clock := newFakeClock()
	boom := errors.New("boom")
	spy := &execSpy{err: boom}
	b := New(3, time.Hour, spy.exec, clock)
	defer b.Close()

	ctx, cancel := context.WithCancel(context.Background())
	r1 := submitAsync(b, ctx, 1) // will be cancelled, must not see the error
	waitFor(t, "1 pending item", func() bool { return pendingLen(b) == 1 })
	cancel()
	if got := <-r1; !errors.Is(got.err, context.Canceled) {
		t.Fatalf("cancelled submit: got %v, want context.Canceled", got.err)
	}
	waitFor(t, "cancelled item removed", func() bool { return pendingLen(b) == 0 })

	r2 := submitAsync(b, context.Background(), 2)
	waitFor(t, "item 2 pending", func() bool { return pendingLen(b) == 1 })
	r3 := submitAsync(b, context.Background(), 3)
	waitFor(t, "2 pending items", func() bool { return pendingLen(b) == 2 })
	clock.Advance(time.Hour)
	for i, r := range []<-chan submitResult{r2, r3} {
		if got := <-r; !errors.Is(got.err, boom) {
			t.Fatalf("survivor %d: got %v, want boom", i, got.err)
		}
	}
	if calls := spy.batches(); len(calls) != 1 || fmt.Sprint(calls[0]) != "[2 3]" {
		t.Fatalf("exec should run once with survivors [2 3], got %v", calls)
	}
}

func TestResultCountMismatchMapsToEveryItem(t *testing.T) {
	clock := newFakeClock()
	exec := func(_ context.Context, items []int) ([]int, error) {
		return []int{1}, nil // wrong length
	}
	b := New(2, time.Hour, exec, clock)
	defer b.Close()

	r1 := submitAsync(b, context.Background(), 1)
	r2 := submitAsync(b, context.Background(), 2)
	for i, r := range []<-chan submitResult{r1, r2} {
		if got := <-r; !errors.Is(got.err, ErrResultCountMismatch) {
			t.Fatalf("submit %d: got %v, want ErrResultCountMismatch", i, got.err)
		}
	}
}

func TestNewItemsNeverJoinSealedBatch(t *testing.T) {
	clock := newFakeClock()
	spy := &execSpy{gate: make(chan struct{})}
	b := New(2, time.Minute, spy.exec, clock)
	defer b.Close()

	// Items 1 and 2 fill and seal the first batch; its exec blocks on gate.
	r1 := submitAsync(b, context.Background(), 1)
	waitFor(t, "1 pending item", func() bool { return pendingLen(b) == 1 })
	r2 := submitAsync(b, context.Background(), 2)
	waitFor(t, "first batch to enter exec", func() bool {
		spy.mu.Lock()
		defer spy.mu.Unlock()
		return len(spy.calls) == 1
	})

	// Submitted while the first batch is executing: must start a new batch.
	r3 := submitAsync(b, context.Background(), 3)
	waitFor(t, "item 3 pending in a fresh batch", func() bool { return pendingLen(b) == 1 })

	close(spy.gate) // release first batch
	if got := <-r1; got.err != nil || got.val != 10 {
		t.Fatalf("submit 1: got (%d, %v)", got.val, got.err)
	}
	if got := <-r2; got.err != nil || got.val != 20 {
		t.Fatalf("submit 2: got (%d, %v)", got.val, got.err)
	}

	// The sealed batch must contain exactly [1 2]; item 3 flushes later
	// via its own batch's timer.
	calls := spy.batches()
	if fmt.Sprint(calls[0]) != "[1 2]" {
		t.Fatalf("sealed batch contaminated: got %v", calls[0])
	}
	clock.Advance(time.Minute)
	if got := <-r3; got.err != nil || got.val != 30 {
		t.Fatalf("submit 3: got (%d, %v)", got.val, got.err)
	}
	if calls := spy.batches(); len(calls) != 2 || fmt.Sprint(calls[1]) != "[3]" {
		t.Fatalf("second batch should be [3], got %v", calls)
	}
}

func TestCloseFlushesPendingAndRejectsNew(t *testing.T) {
	clock := newFakeClock()
	spy := &execSpy{}
	b := New(10, time.Hour, spy.exec, clock)

	r := submitAsync(b, context.Background(), 42)
	waitFor(t, "1 pending item", func() bool { return pendingLen(b) == 1 })
	b.Close() // must flush without waiting for size or timer

	if got := <-r; got.err != nil || got.val != 420 {
		t.Fatalf("pending submit after Close: got (%d, %v)", got.val, got.err)
	}
	if _, err := b.Submit(context.Background(), 1); !errors.Is(err, ErrClosed) {
		t.Fatalf("submit after Close: got %v, want ErrClosed", err)
	}
}

func TestConcurrentSubmitsRaceStress(t *testing.T) {
	clock := newFakeClock()
	spy := &execSpy{}
	b := New(8, 10*time.Millisecond, spy.exec, clock)

	const n = 200
	results := make([]<-chan submitResult, n)
	for i := 0; i < n; i++ {
		results[i] = submitAsync(b, context.Background(), i)
		if i%16 == 0 {
			clock.Advance(10 * time.Millisecond) // interleave timer flushes
		}
	}
	// Submit goroutines enqueue asynchronously, so new batches (and their
	// timers) may appear at any time: keep advancing the clock until every
	// result has arrived.
	for i := 0; i < n; i++ {
		var got submitResult
		for {
			select {
			case got = <-results[i]:
			case <-time.After(time.Millisecond):
				clock.Advance(10 * time.Millisecond)
				continue
			}
			break
		}
		if got.err != nil {
			t.Fatalf("submit %d: unexpected error: %v", i, got.err)
		}
		if got.val != i*10 {
			t.Fatalf("submit %d: got %d, want %d", i, got.val, i*10)
		}
	}
	b.Close()

	total := 0
	for _, c := range spy.batches() {
		if len(c) > 8 {
			t.Fatalf("batch exceeded maxSize: %v", c)
		}
		total += len(c)
	}
	if total != n {
		t.Fatalf("exec saw %d items, want %d", total, n)
	}
}
