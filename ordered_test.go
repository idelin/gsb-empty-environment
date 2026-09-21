package ordered

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

// feed sends items on a channel and closes it; returns the channel and a
// counter of how many items were actually consumed from it.
func feed(items ...int) (<-chan int, *atomic.Int64, func()) {
	in := make(chan int)
	var sent atomic.Int64
	done := make(chan struct{})
	go func() {
		defer close(in)
		for _, v := range items {
			select {
			case in <- v:
				sent.Add(1)
			case <-done:
				return
			}
		}
	}()
	return in, &sent, func() { close(done) }
}

func collect[O any](out <-chan Result[O]) []Result[O] {
	var rs []Result[O]
	for r := range out {
		rs = append(rs, r)
	}
	return rs
}

// Later items finish first but must not overtake the slow head.
func TestStrictOrderSlowHead(t *testing.T) {
	const n = 50
	xs := make([]int, n)
	for i := range xs {
		xs[i] = i
	}
	in, _, stop := feed(xs...)
	defer stop()

	head := make(chan struct{})
	fn := func(ctx context.Context, i int) (int, error) {
		if i == 0 {
			select {
			case <-head:
			case <-ctx.Done():
				return 0, ctx.Err()
			}
		} else {
			time.Sleep(time.Duration(n-i) * time.Millisecond) // later items finish earlier
		}
		return i * 10, nil
	}
	out := Process(context.Background(), in, fn, Options{Workers: 16, MaxBuffered: 4, Policy: FailInOrder})

	// While the head is blocked, nothing may be delivered.
	select {
	case r := <-out:
		t.Fatalf("delivered %v before head completed", r)
	case <-time.After(150 * time.Millisecond):
	}
	close(head)

	rs := collect(out)
	if len(rs) != n {
		t.Fatalf("got %d results, want %d", len(rs), n)
	}
	for i, r := range rs {
		if r.Err != nil || r.Value != i*10 {
			t.Fatalf("position %d: got %+v, want value %d", i, r, i*10)
		}
	}
}

// A slow head must create backpressure: completed-undelivered results are
// bounded and input consumption stalls.
func TestBackpressureBoundsBufferAndPrefetch(t *testing.T) {
	const n = 100
	const workers = 8
	const maxBuf = 2
	xs := make([]int, n)
	for i := range xs {
		xs[i] = i
	}
	in, sent, stop := feed(xs...)
	defer stop()

	head := make(chan struct{})
	var completed atomic.Int64
	fn := func(ctx context.Context, i int) (int, error) {
		if i == 0 {
			<-head
		}
		completed.Add(1)
		return i, nil
	}
	out := Process(context.Background(), in, fn, Options{Workers: workers, MaxBuffered: maxBuf, Policy: FailInOrder})

	time.Sleep(200 * time.Millisecond) // let the pipeline settle against the blocked head
	if got := completed.Load(); got > workers {
		t.Fatalf("completed tasks = %d, exceeds Workers=%d while head blocked", got, workers)
	}
	if got, want := sent.Load(), int64(workers+maxBuf+1); got > want {
		t.Fatalf("prefetched %d input items, exceeds Workers+MaxBuffered+1=%d", got, want)
	}
	select {
	case r := <-out:
		t.Fatalf("delivered %v while head blocked", r)
	default:
	}

	close(head)
	rs := collect(out)
	if len(rs) != n {
		t.Fatalf("got %d results, want %d", len(rs), n)
	}
	for i, r := range rs {
		if r.Value != i || r.Err != nil {
			t.Fatalf("position %d: got %+v", i, r)
		}
	}
}

// FailFast: the error is delivered immediately, remaining work is cancelled,
// undelivered results are recycled.
func TestFailFast(t *testing.T) {
	boom := errors.New("boom")
	xs := make([]int, 10)
	for i := range xs {
		xs[i] = i
	}
	in, _, stop := feed(xs...)
	defer stop()

	var cancelled atomic.Int64
	fn := func(ctx context.Context, i int) (int, error) {
		if i == 3 {
			time.Sleep(10 * time.Millisecond)
			return 0, boom
		}
		select {
		case <-time.After(5 * time.Second):
			return i, nil
		case <-ctx.Done():
			cancelled.Add(1)
			return 0, ctx.Err()
		}
	}
	out := Process(context.Background(), in, fn, Options{Workers: 8, MaxBuffered: 4, Policy: FailFast})

	start := time.Now()
	rs := collect(out)
	elapsed := time.Since(start)

	if len(rs) != 1 || !errors.Is(rs[0].Err, boom) {
		t.Fatalf("got %+v, want a single boom error", rs)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("took %v; failure was not immediate", elapsed)
	}
	if cancelled.Load() == 0 {
		t.Fatal("slow tasks were not cancelled")
	}
}

// FailInOrder: results before the failing item are delivered in order, then
// the error; later undelivered results are recycled.
func TestFailInOrder(t *testing.T) {
	boom := errors.New("boom")
	in, _, stop := feed(0, 1, 2, 3, 4, 5)
	defer stop()

	fn := func(ctx context.Context, i int) (int, error) {
		if i == 2 {
			return 0, boom
		}
		return i * 10, nil
	}
	out := Process(context.Background(), in, fn, Options{Workers: 6, MaxBuffered: 6, Policy: FailInOrder})
	rs := collect(out)

	if len(rs) != 3 {
		t.Fatalf("got %d results %+v, want 3", len(rs), rs)
	}
	for i := 0; i < 2; i++ {
		if rs[i].Err != nil || rs[i].Value != i*10 {
			t.Fatalf("position %d: got %+v, want value %d", i, rs[i], i*10)
		}
	}
	if !errors.Is(rs[2].Err, boom) {
		t.Fatalf("position 2: got %+v, want boom", rs[2])
	}
}

// An infinite input stream must not be prefetched without bound.
func TestInfiniteInputBoundedPrefetch(t *testing.T) {
	in := make(chan int)
	var sent atomic.Int64
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		for i := 0; ; i++ {
			select {
			case in <- i:
				sent.Add(1)
			case <-stop:
				return
			}
		}
	}()

	const workers = 4
	ctx, cancel := context.WithCancel(context.Background())
	out := Process(ctx, in, func(ctx context.Context, i int) (int, error) { return i, nil },
		Options{Workers: workers, MaxBuffered: 2, Policy: FailInOrder})

	const want = 20
	for i := 0; i < want; i++ {
		r := <-out
		if r.Err != nil || r.Value != i {
			t.Fatalf("position %d: got %+v", i, r)
		}
	}
	cancel()
	for range out { // drain until closed
	}
	time.Sleep(50 * time.Millisecond) // let the dispatcher observe cancellation
	if got, limit := sent.Load(), int64(want+workers+2+1); got > limit {
		t.Fatalf("prefetched %d items for %d delivered, exceeds limit %d", got, want, limit)
	}
}

func TestEmptyInput(t *testing.T) {
	in := make(chan int)
	close(in)
	out := Process(context.Background(), in, func(ctx context.Context, i int) (int, error) { return i, nil },
		Options{Workers: 2, MaxBuffered: 1, Policy: FailFast})
	if rs := collect(out); len(rs) != 0 {
		t.Fatalf("got %+v, want no results", rs)
	}
}

// Cancelling the context terminates the stream and reclaims in-flight work.
func TestContextCancel(t *testing.T) {
	in, _, stop := feed(0, 1, 2, 3)
	defer stop()
	ctx, cancel := context.WithCancel(context.Background())
	var started atomic.Int64
	out := Process(ctx, in, func(ctx context.Context, i int) (int, error) {
		started.Add(1)
		<-ctx.Done()
		return 0, ctx.Err()
	}, Options{Workers: 2, MaxBuffered: 1, Policy: FailInOrder})
	time.Sleep(50 * time.Millisecond)
	cancel()
	rs := collect(out)
	for _, r := range rs {
		if r.Err == nil {
			t.Fatalf("unexpected successful result after cancel: %+v", r)
		}
	}
}

// Example of ordered concurrent mapping.
func ExampleProcess() {
	ctx := context.Background()
	in := make(chan int)
	go func() {
		defer close(in)
		for i := 1; i <= 5; i++ {
			in <- i
		}
	}()
	double := func(_ context.Context, i int) (int, error) { return i * 2, nil }
	for r := range Process(ctx, in, double, Options{Workers: 3, MaxBuffered: 2, Policy: FailInOrder}) {
		fmt.Println(r.Value)
	}
	// Output:
	// 2
	// 4
	// 6
	// 8
	// 10
}
