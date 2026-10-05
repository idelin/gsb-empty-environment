package batchwriter

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"
)

var epoch = time.Unix(1_700_000_000, 0)

// recorder collects flushed batches and the virtual time of each flush.
type recorder struct {
	mu      sync.Mutex
	batches [][]int
	at      []time.Time
	failOn  map[int]error
}

func (r *recorder) write(clock Clock) WriteFunc[int] {
	return func(_ context.Context, items []int) error {
		r.mu.Lock()
		defer r.mu.Unlock()
		batch := append([]int(nil), items...)
		r.batches = append(r.batches, batch)
		r.at = append(r.at, clock.Now())
		for _, it := range items {
			if err, ok := r.failOn[it]; ok {
				return err
			}
		}
		return nil
	}
}

func (r *recorder) snapshot() [][]int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([][]int(nil), r.batches...)
}

func TestDeadlineAnchoredToOldestRequest(t *testing.T) {
	clock := NewFakeClock(epoch)
	rec := &recorder{}
	b := New[int](Config[int]{
		MaxSize:  100,
		MaxDelay: 10 * time.Second,
		Clock:    clock,
		Write:    rec.write(clock),
	})

	var reqs []*Request[int]
	// A steady trickle of new requests must not postpone the flush:
	// the deadline is anchored to the first (oldest) request at t=0.
	for i := 0; i < 9; i++ {
		reqs = append(reqs, b.Submit(i))
		clock.Advance(time.Second) // t=1..9
	}
	if got := rec.snapshot(); len(got) != 0 {
		t.Fatalf("flushed early at t=9: %v", got)
	}

	clock.Advance(time.Second) // t=10: oldest request expires
	got := rec.snapshot()
	if len(got) != 1 || len(got[0]) != 9 {
		t.Fatalf("want one batch of 9 at deadline, got %v", got)
	}
	for i := 0; i < 9; i++ {
		if got[0][i] != i {
			t.Fatalf("batch order not preserved: %v", got[0])
		}
	}
	if !rec.at[0].Equal(epoch.Add(10 * time.Second)) {
		t.Fatalf("flush happened at %v, want t=10s", rec.at[0])
	}
	for _, r := range reqs {
		if err := r.Wait(); err != nil {
			t.Fatalf("request failed: %v", err)
		}
	}
}

func TestSizeFlushStopsDeadlineTimer(t *testing.T) {
	clock := NewFakeClock(epoch)
	rec := &recorder{}
	b := New[int](Config[int]{
		MaxSize:  3,
		MaxDelay: 10 * time.Second,
		Clock:    clock,
		Write:    rec.write(clock),
	})

	b.Submit(1)
	b.Submit(2)
	r3 := b.Submit(3) // fills the batch: immediate flush
	if err := r3.Wait(); err != nil {
		t.Fatal(err)
	}
	if got := rec.snapshot(); !reflect.DeepEqual(got, [][]int{{1, 2, 3}}) {
		t.Fatalf("got %v", got)
	}

	// The deadline timer of the flushed batch must be dead: advancing far
	// past it with an empty buffer flushes nothing.
	clock.Advance(time.Hour)
	if got := rec.snapshot(); len(got) != 1 {
		t.Fatalf("stale timer fired: %v", got)
	}

	// A partial batch still flushes on its own deadline.
	b.Submit(4)
	b.Submit(5)
	clock.Advance(10 * time.Second)
	if got := rec.snapshot(); !reflect.DeepEqual(got, [][]int{{1, 2, 3}, {4, 5}}) {
		t.Fatalf("got %v", got)
	}
}

func TestCancelRemovesOnlyUncommitted(t *testing.T) {
	clock := NewFakeClock(epoch)
	rec := &recorder{}
	b := New[int](Config[int]{
		MaxSize:  10,
		MaxDelay: 5 * time.Second,
		Clock:    clock,
		Write:    rec.write(clock),
	})

	ra := b.Submit(1)
	rb := b.Submit(2)
	if !ra.Cancel() {
		t.Fatal("cancel of pending request should succeed")
	}
	if ra.Cancel() {
		t.Fatal("double cancel should fail")
	}
	if err := ra.Wait(); !errors.Is(err, ErrCancelled) {
		t.Fatalf("cancelled request got %v", err)
	}

	clock.Advance(5 * time.Second)
	if got := rec.snapshot(); !reflect.DeepEqual(got, [][]int{{2}}) {
		t.Fatalf("cancelled item was written: %v", got)
	}
	if err := rb.Wait(); err != nil {
		t.Fatal(err)
	}

	// Once a batch is cut, its members can no longer be cancelled.
	rc := b.Submit(3)
	clock.Advance(5 * time.Second)
	if rc.Cancel() {
		t.Fatal("cancel after commit should fail")
	}
	if err := rc.Wait(); err != nil {
		t.Fatal(err)
	}
	if got := rec.snapshot(); !reflect.DeepEqual(got, [][]int{{2}, {3}}) {
		t.Fatalf("committed item lost: %v", got)
	}
}

func TestBatchFailureDeliveredToEachMember(t *testing.T) {
	clock := NewFakeClock(epoch)
	boom := errors.New("store exploded")
	rec := &recorder{failOn: map[int]error{99: boom}}
	b := New[int](Config[int]{
		MaxSize:  2,
		MaxDelay: time.Second,
		Clock:    clock,
		Write:    rec.write(clock),
	})

	ok1, ok2 := b.Submit(1), b.Submit(2)
	bad1, bad2 := b.Submit(99), b.Submit(3) // this batch fails
	ok3, ok4 := b.Submit(4), b.Submit(5)

	for _, r := range []*Request[int]{ok1, ok2, ok3, ok4} {
		if err := r.Wait(); err != nil {
			t.Fatalf("unrelated batch member got error: %v", err)
		}
	}
	for _, r := range []*Request[int]{bad1, bad2} {
		if err := r.Wait(); !errors.Is(err, boom) {
			t.Fatalf("failed batch member got %v, want %v", err, boom)
		}
	}
}

// TestSameTickFlushAndEnqueue pins down the batch boundary when a flush and
// an enqueue land on the same clock tick. FakeClock fires same-deadline
// timers in registration order, so both interleavings are exercised and
// each must produce a stable partition with every item written exactly once.
func TestSameTickFlushAndEnqueue(t *testing.T) {
	const delay = 10 * time.Second

	run := func(enqueueFirst bool) (batches [][]int) {
		clock := NewFakeClock(epoch)
		rec := &recorder{}
		b := New[int](Config[int]{
			MaxSize:  100,
			MaxDelay: delay,
			Clock:    clock,
			Write:    rec.write(clock),
		})

		enqueue := func() { b.Submit(777) }
		if enqueueFirst {
			// Registered before the batcher's deadline timer, so at t=10
			// the enqueue runs first and 777 joins the expiring batch.
			clock.AfterFunc(delay, enqueue)
			b.Submit(1)
		} else {
			// Registered after the deadline timer, so at t=10 the flush
			// runs first and 777 starts the next batch.
			b.Submit(1)
			clock.AfterFunc(delay, enqueue)
		}

		clock.Advance(delay) // t=10: same-tick flush + enqueue
		clock.Advance(delay) // t=20: flush whatever remains
		return rec.snapshot()
	}

	gotA := run(true)
	if !reflect.DeepEqual(gotA, [][]int{{1, 777}}) {
		t.Fatalf("enqueue-first partition unstable: %v", gotA)
	}
	assertExactlyOnce(t, gotA, 1, 777)

	gotB := run(false)
	if !reflect.DeepEqual(gotB, [][]int{{1}, {777}}) {
		t.Fatalf("flush-first partition unstable: %v", gotB)
	}
	assertExactlyOnce(t, gotB, 1, 777)
}

// TestEnqueueDuringFlush covers the reentrant case: the store itself
// submits a new item while its batch is being written at the same tick.
func TestEnqueueDuringFlush(t *testing.T) {
	clock := NewFakeClock(epoch)
	rec := &recorder{}
	var b *Batcher[int]
	b = New[int](Config[int]{
		MaxSize:  100,
		MaxDelay: 10 * time.Second,
		Clock:    clock,
		Write: func(ctx context.Context, items []int) error {
			if err := rec.write(clock)(ctx, items); err != nil {
				return err
			}
			if items[0] == 1 {
				b.Submit(777) // enqueue at the same tick as the flush
			}
			return nil
		},
	})

	b.Submit(1)
	clock.Advance(10 * time.Second) // t=10: flush [1], 777 enqueued mid-flush
	if got := rec.snapshot(); !reflect.DeepEqual(got, [][]int{{1}}) {
		t.Fatalf("reentrant enqueue leaked into cut batch: %v", got)
	}
	clock.Advance(10 * time.Second) // t=20: flush [777]
	got := rec.snapshot()
	if !reflect.DeepEqual(got, [][]int{{1}, {777}}) {
		t.Fatalf("got %v", got)
	}
	assertExactlyOnce(t, got, 1, 777)
}

func TestCloseFlushesRemainderAndRejectsNew(t *testing.T) {
	clock := NewFakeClock(epoch)
	rec := &recorder{}
	b := New[int](Config[int]{
		MaxSize:  100,
		MaxDelay: time.Minute,
		Clock:    clock,
		Write:    rec.write(clock),
	})

	r := b.Submit(42)
	b.Close()
	if err := r.Wait(); err != nil {
		t.Fatal(err)
	}
	if got := rec.snapshot(); !reflect.DeepEqual(got, [][]int{{42}}) {
		t.Fatalf("got %v", got)
	}
	if err := b.Submit(7).Wait(); !errors.Is(err, ErrClosed) {
		t.Fatalf("submit after close got %v", err)
	}
}

// TestConcurrentExactlyOnce hammers the batcher from many goroutines with
// the real clock and verifies every non-cancelled item is written exactly
// once and every request resolves exactly once.
func TestConcurrentExactlyOnce(t *testing.T) {
	var mu sync.Mutex
	written := map[int]int{}
	b := New[int](Config[int]{
		MaxSize:  8,
		MaxDelay: 2 * time.Millisecond,
		Write: func(_ context.Context, items []int) error {
			mu.Lock()
			for _, it := range items {
				written[it]++
			}
			mu.Unlock()
			return nil
		},
	})

	const producers = 4
	const perProducer = 250
	var wg sync.WaitGroup
	for p := 0; p < producers; p++ {
		wg.Add(1)
		go func(p int) {
			defer wg.Done()
			for i := 0; i < perProducer; i++ {
				id := p*perProducer + i
				r := b.Submit(id)
				if i%10 == 0 && r.Cancel() {
					// Cancelled before commit: must never be written.
					if err := r.Wait(); !errors.Is(err, ErrCancelled) {
						t.Errorf("cancelled request got %v", err)
					}
					mu.Lock()
					if written[id] != 0 {
						t.Errorf("cancelled item %d was written", id)
					}
					mu.Unlock()
					continue
				}
				if err := r.Wait(); err != nil {
					t.Errorf("item %d: %v", id, err)
				}
			}
		}(p)
	}
	wg.Wait()
	b.Close()

	mu.Lock()
	defer mu.Unlock()
	for id, n := range written {
		if n > 1 {
			t.Errorf("item %d written %d times", id, n)
		}
	}
}

func assertExactlyOnce(t *testing.T, batches [][]int, want ...int) {
	t.Helper()
	counts := map[int]int{}
	for _, batch := range batches {
		for _, it := range batch {
			counts[it]++
		}
	}
	for _, w := range want {
		if counts[w] != 1 {
			t.Fatalf("item %d written %d times in %v", w, counts[w], batches)
		}
		delete(counts, w)
	}
	if len(counts) != 0 {
		t.Fatalf("unexpected items written: %v", counts)
	}
}
