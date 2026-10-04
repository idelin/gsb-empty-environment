package batcher

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"
)

var t0 = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

const delay = 10 * time.Second

// recorder collects every batch handed to the write function.
type recorder struct {
	mu      sync.Mutex
	batches [][]any
	err     error // error returned by every write, if set
}

func (r *recorder) write(batch []any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := append([]any(nil), batch...)
	r.batches = append(r.batches, cp)
	return r.err
}

func (r *recorder) snapshot() [][]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([][]any(nil), r.batches...)
}

func (r *recorder) flattened() []any {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []any
	for _, b := range r.batches {
		out = append(out, b...)
	}
	return out
}

func mustResult(t *testing.T, req *Request) error {
	t.Helper()
	select {
	case err := <-req.Result():
		return err
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for request result")
		return nil
	}
}

func TestFlushOnSize(t *testing.T) {
	rec := &recorder{}
	b := NewBatcher(3, delay, NewManualClock(t0), rec.write)

	r1 := b.Enqueue("a")
	r2 := b.Enqueue("b")
	if got := rec.snapshot(); len(got) != 0 {
		t.Fatalf("flushed early: %v", got)
	}
	r3 := b.Enqueue("c")

	want := [][]any{{"a", "b", "c"}}
	if got := rec.snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("batches = %v, want %v", got, want)
	}
	for _, req := range []*Request{r1, r2, r3} {
		if err := mustResult(t, req); err != nil {
			t.Fatalf("result = %v, want nil", err)
		}
	}
}

func TestFlushOnOldestExpiry(t *testing.T) {
	rec := &recorder{}
	clock := NewManualClock(t0)
	b := NewBatcher(100, delay, clock, rec.write)

	b.Enqueue("a")
	clock.Advance(delay - time.Second)
	if got := rec.snapshot(); len(got) != 0 {
		t.Fatalf("flushed before deadline: %v", got)
	}
	clock.Advance(time.Second) // reach the deadline exactly
	want := [][]any{{"a"}}
	if got := rec.snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("batches = %v, want %v", got, want)
	}
}

func TestNewRequestsDoNotDelayOldOnes(t *testing.T) {
	rec := &recorder{}
	clock := NewManualClock(t0)
	b := NewBatcher(100, delay, clock, rec.write)

	b.Enqueue("old")
	// Keep feeding new requests right up to (but not past) the
	// original deadline; none of them may push the flush out.
	for i := 0; i < 9; i++ {
		clock.Advance(time.Second)
		b.Enqueue(fmt.Sprintf("new-%d", i))
		if got := rec.snapshot(); len(got) != 0 {
			t.Fatalf("flushed before oldest deadline: %v", got)
		}
	}
	clock.Advance(time.Second) // t0 + delay: "old" expires
	got := rec.snapshot()
	if len(got) != 1 || len(got[0]) != 10 {
		t.Fatalf("batches = %v, want one batch of 10", got)
	}
	// No phantom second flush.
	clock.Advance(10 * delay)
	if got := rec.snapshot(); len(got) != 1 {
		t.Fatalf("unexpected extra flush: %v", got)
	}
}

func TestCancelRemovesOnlyUncommitted(t *testing.T) {
	rec := &recorder{}
	clock := NewManualClock(t0)
	b := NewBatcher(2, delay, clock, rec.write)

	r1 := b.Enqueue("a")
	if !r1.Cancel() {
		t.Fatal("cancel of uncommitted request failed")
	}
	if err := mustResult(t, r1); !errors.Is(err, ErrCanceled) {
		t.Fatalf("canceled result = %v, want ErrCanceled", err)
	}
	clock.Advance(delay)
	if got := rec.snapshot(); len(got) != 0 {
		t.Fatalf("canceled request was written: %v", got)
	}

	// Committed requests ignore cancellation and get the batch outcome.
	r2 := b.Enqueue("b")
	r3 := b.Enqueue("c") // fills the batch -> committed
	if r2.Cancel() || r3.Cancel() {
		t.Fatal("cancel of committed request succeeded")
	}
	for _, req := range []*Request{r2, r3} {
		if err := mustResult(t, req); err != nil {
			t.Fatalf("committed result = %v, want nil", err)
		}
	}
	if got := rec.snapshot(); !reflect.DeepEqual(got, [][]any{{"b", "c"}}) {
		t.Fatalf("batches = %v", got)
	}
}

func TestCancelOldestRearmsTimer(t *testing.T) {
	rec := &recorder{}
	clock := NewManualClock(t0)
	b := NewBatcher(100, delay, clock, rec.write)

	old := b.Enqueue("old")
	clock.Advance(time.Second)
	b.Enqueue("young") // deadline is t0 + 1s + delay
	if !old.Cancel() {
		t.Fatal("cancel failed")
	}
	// Advance to the canceled request's original deadline (t0+delay):
	// nothing may flush, because the timer was re-armed for "young".
	clock.Advance(delay - time.Second)
	if got := rec.snapshot(); len(got) != 0 {
		t.Fatalf("flush used canceled request's deadline: %v", got)
	}
	clock.Advance(time.Second) // reaches "young"'s deadline (t0+1s+delay)
	if got := rec.snapshot(); !reflect.DeepEqual(got, [][]any{{"young"}}) {
		t.Fatalf("batches = %v", got)
	}
}

func TestBatchFailureDeliveredToEachMember(t *testing.T) {
	boom := errors.New("disk on fire")
	rec := &recorder{err: boom}
	b := NewBatcher(3, delay, NewManualClock(t0), rec.write)

	reqs := []*Request{b.Enqueue("a"), b.Enqueue("b"), b.Enqueue("c")}
	for i, req := range reqs {
		if err := mustResult(t, req); !errors.Is(err, boom) {
			t.Fatalf("member %d result = %v, want %v", i, err, boom)
		}
	}
}

// TestSameInstantFlushThenEnqueue: at the exact tick the age timer
// fires, a new enqueue lands just after the flush. The new request must
// start a fresh batch with a fresh deadline.
func TestSameInstantFlushThenEnqueue(t *testing.T) {
	rec := &recorder{}
	clock := NewManualClock(t0)
	b := NewBatcher(100, delay, clock, rec.write)

	b.Enqueue("a")
	clock.Advance(delay) // fires at t0+delay; clock now sits on that tick
	b.Enqueue("b")       // enqueued at the very same instant

	want := [][]any{{"a"}}
	if got := rec.snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("after same-instant enqueue: batches = %v, want %v", got, want)
	}
	clock.Advance(delay)
	want = [][]any{{"a"}, {"b"}}
	if got := rec.snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("batches = %v, want %v", got, want)
	}
}

// TestSameInstantEnqueueThenFlush: both requests are queued before the
// shared deadline tick, so they must flush together in one batch.
func TestSameInstantEnqueueThenFlush(t *testing.T) {
	rec := &recorder{}
	clock := NewManualClock(t0)
	b := NewBatcher(100, delay, clock, rec.write)

	b.Enqueue("a")
	b.Enqueue("b") // same instant, before any time passes
	clock.Advance(delay)

	want := [][]any{{"a", "b"}}
	if got := rec.snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("batches = %v, want %v", got, want)
	}
}

// TestSameInstantRaceStability hammers the boundary where a flush and
// an enqueue collide at the same timestamp, from two goroutines. Every
// item must be written exactly once, and the partition must be one of
// the two serializable outcomes: {a,b} or {a},{b}.
func TestSameInstantRaceStability(t *testing.T) {
	for i := 0; i < 200; i++ {
		rec := &recorder{}
		clock := NewManualClock(t0)
		b := NewBatcher(100, delay, clock, rec.write)

		ra := b.Enqueue("a")
		var rb *Request
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); clock.Advance(delay) }()
		go func() { defer wg.Done(); rb = b.Enqueue("b") }()
		wg.Wait()

		// Let any surviving timer fire as well.
		clock.Advance(delay)

		flat := rec.flattened()
		counts := map[any]int{}
		for _, v := range flat {
			counts[v]++
		}
		if counts["a"] != 1 || counts["b"] != 1 || len(flat) != 2 {
			t.Fatalf("iter %d: writes = %v, want a and b exactly once each", i, flat)
		}
		got := rec.snapshot()
		ok := reflect.DeepEqual(got, [][]any{{"a", "b"}}) ||
			reflect.DeepEqual(got, [][]any{{"a"}, {"b"}}) ||
			reflect.DeepEqual(got, [][]any{{"b"}, {"a"}})
		if !ok {
			t.Fatalf("iter %d: unstable partition %v", i, got)
		}
		if err := mustResult(t, ra); err != nil {
			t.Fatalf("iter %d: a result = %v", i, err)
		}
		if err := mustResult(t, rb); err != nil {
			t.Fatalf("iter %d: b result = %v", i, err)
		}
	}
}

// TestConcurrentMixed exercises enqueue/cancel/flush concurrently under
// the race detector; the only invariant checked is that every
// committed request is written exactly once and every request gets
// exactly one result.
func TestConcurrentMixed(t *testing.T) {
	rec := &recorder{}
	b := NewBatcher(4, time.Millisecond, RealClock{}, rec.write)

	const n = 200
	reqs := make([]*Request, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			reqs[i] = b.Enqueue(i)
			if i%3 == 0 {
				reqs[i].Cancel()
			}
		}(i)
	}
	wg.Wait()
	// Drain anything left queued.
	time.Sleep(20 * time.Millisecond)

	for i, req := range reqs {
		select {
		case <-req.Result():
		case <-time.After(2 * time.Second):
			t.Fatalf("request %d never got a result", i)
		}
	}
	seen := map[any]int{}
	for _, v := range rec.flattened() {
		seen[v]++
	}
	for v, c := range seen {
		if c != 1 {
			t.Fatalf("value %v written %d times", v, c)
		}
	}
}
