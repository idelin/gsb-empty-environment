package ordered

import (
	"context"
	"errors"
	"math/rand"
	"sync/atomic"
	"testing"
	"time"
)

func feed(items ...int) <-chan int {
	in := make(chan int)
	go func() {
		defer close(in)
		for _, v := range items {
			in <- v
		}
	}()
	return in
}

func TestOrderPreserved(t *testing.T) {
	const n = 200
	in := make(chan int)
	go func() {
		defer close(in)
		for i := 0; i < n; i++ {
			in <- i
		}
	}()
	fn := func(ctx context.Context, v int) (int, error) {
		time.Sleep(time.Duration(rand.Intn(5)) * time.Millisecond)
		return v * 2, nil
	}
	out := Map(context.Background(), in, 8, OrderedFail, fn)
	i := 0
	for r := range out {
		if r.Err != nil {
			t.Fatalf("unexpected error at %d: %v", r.Index, r.Err)
		}
		if r.Index != i || r.Value != i*2 {
			t.Fatalf("out of order: got (index=%d value=%d), want index=%d", r.Index, r.Value, i)
		}
		i++
	}
	if i != n {
		t.Fatalf("got %d results, want %d", i, n)
	}
}

func TestWindowBoundsConcurrency(t *testing.T) {
	const window = 3
	var inflight, maxInflight atomic.Int64
	fn := func(ctx context.Context, v int) (int, error) {
		cur := inflight.Add(1)
		for {
			m := maxInflight.Load()
			if cur <= m || maxInflight.CompareAndSwap(m, cur) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		inflight.Add(-1)
		return v, nil
	}
	in := make(chan int)
	go func() {
		defer close(in)
		for i := 0; i < 30; i++ {
			in <- i
		}
	}()
	for range Map(context.Background(), in, window, OrderedFail, fn) {
	}
	if got := maxInflight.Load(); got > window {
		t.Fatalf("max in-flight = %d, exceeds window %d", got, window)
	}
	if got := maxInflight.Load(); got < 2 {
		t.Fatalf("max in-flight = %d, want real concurrency", got)
	}
}

func TestSlowHeadAppliesBackpressure(t *testing.T) {
	const window = 3
	var started atomic.Int64
	var startedWhenHeadDone int64
	fn := func(ctx context.Context, v int) (int, error) {
		started.Add(1)
		if v == 0 {
			// Slow head: while it runs, at most window tasks may be
			// started and later completions must not overtake it.
			time.Sleep(300 * time.Millisecond)
			startedWhenHeadDone = started.Load()
			return v, nil
		}
		time.Sleep(5 * time.Millisecond)
		return v, nil
	}
	in := make(chan int)
	go func() {
		defer close(in)
		for i := 0; i < 20; i++ {
			in <- i
		}
	}()
	out := Map(context.Background(), in, window, OrderedFail, fn)
	prev := -1
	for r := range out {
		if r.Index != prev+1 {
			t.Fatalf("delivery overtook head: got index %d after %d", r.Index, prev)
		}
		prev = r.Index
	}
	if prev != 19 {
		t.Fatalf("got %d results, want 20", prev+1)
	}
	if startedWhenHeadDone > window {
		t.Fatalf("started %d tasks while head blocked, window is %d", startedWhenHeadDone, window)
	}
}

func TestFailFast(t *testing.T) {
	errBoom := errors.New("boom")
	var cancelled atomic.Int64
	fn := func(ctx context.Context, v int) (int, error) {
		if v == 1 {
			time.Sleep(10 * time.Millisecond)
			return 0, errBoom
		}
		select {
		case <-time.After(5 * time.Second):
			return v, nil
		case <-ctx.Done():
			cancelled.Add(1)
			return 0, ctx.Err()
		}
	}
	in := make(chan int)
	go func() {
		defer close(in)
		for i := 0; i < 10; i++ {
			in <- i
		}
	}()
	start := time.Now()
	out := Map(context.Background(), in, 4, FailFast, fn)
	var got []Result[int]
	for r := range out {
		got = append(got, r)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("fail-fast took %v, want prompt abort", elapsed)
	}
	if len(got) != 1 {
		t.Fatalf("delivered %d results, want exactly the failing one: %v", len(got), got)
	}
	if !errors.Is(got[0].Err, errBoom) || got[0].Index != 1 {
		t.Fatalf("final result = %+v, want boom error at index 1", got[0])
	}
	if got := cancelled.Load(); got != 3 {
		t.Fatalf("cancelled %d in-flight tasks, want 3", got)
	}
}

func TestOrderedFail(t *testing.T) {
	errBoom := errors.New("boom")
	fn := func(ctx context.Context, v int) (int, error) {
		if v == 2 {
			return 0, errBoom // fails immediately, ahead of slower items
		}
		select {
		case <-time.After(30 * time.Millisecond):
			return v, nil
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}
	in := make(chan int)
	go func() {
		defer close(in)
		for i := 0; i < 8; i++ {
			in <- i
		}
	}()
	out := Map(context.Background(), in, 4, OrderedFail, fn)
	var got []Result[int]
	for r := range out {
		got = append(got, r)
	}
	if len(got) != 3 {
		t.Fatalf("delivered %d results, want 3 (0, 1, error): %v", len(got), got)
	}
	for i := 0; i < 2; i++ {
		if got[i].Index != i || got[i].Err != nil || got[i].Value != i {
			t.Fatalf("result %d = %+v, want ok value %d", i, got[i], i)
		}
	}
	if got[2].Index != 2 || !errors.Is(got[2].Err, errBoom) {
		t.Fatalf("final result = %+v, want boom error at index 2", got[2])
	}
}

func TestInfiniteInputBoundedPrefetch(t *testing.T) {
	const window = 4
	var produced atomic.Int64
	in := make(chan int)
	done := make(chan struct{})
	defer close(done)
	go func() {
		for i := 0; ; i++ {
			select {
			case in <- i:
				produced.Add(1)
			case <-done:
				return
			}
		}
	}()
	fn := func(ctx context.Context, v int) (int, error) {
		select {
		case <-time.After(50 * time.Millisecond):
			return v, nil
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out := Map(ctx, in, window, OrderedFail, fn)
	consumed := 0
	for range out {
		consumed++
		if consumed == 10 {
			cancel()
		}
	}
	if got := produced.Load(); got > int64(consumed+window+2) {
		t.Fatalf("produced %d items for %d consumed (window %d): unbounded prefetch", got, consumed, window)
	}
}

func TestEmptyInput(t *testing.T) {
	in := make(chan int)
	close(in)
	fn := func(ctx context.Context, v int) (int, error) { return v, nil }
	for range Map(context.Background(), in, 4, OrderedFail, fn) {
		t.Fatal("unexpected result for empty input")
	}
}

func TestWindowClampedToOne(t *testing.T) {
	out := Map(context.Background(), feed(1, 2, 3), 0, OrderedFail,
		func(ctx context.Context, v int) (int, error) { return v, nil })
	want := 1
	for r := range out {
		if r.Value != want {
			t.Fatalf("got %d, want %d", r.Value, want)
		}
		want++
	}
	if want != 4 {
		t.Fatalf("got %d results, want 3", want-1)
	}
}
