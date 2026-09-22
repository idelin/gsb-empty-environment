package keyq

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// recorder collects completion order and detects concurrent execution of
// same-key tasks. Task bodies, timing and failures are injected by tests.
type recorder struct {
	mu        sync.Mutex
	order     []int
	inFlight  atomic.Int32
	violated  atomic.Bool
	execCount atomic.Int32
}

func (r *recorder) task(i int, extra func(ctx context.Context) error) TaskFunc {
	return func(ctx context.Context) error {
		if r.inFlight.Add(1) != 1 {
			r.violated.Store(true)
		}
		defer r.inFlight.Add(-1)
		var err error
		if extra != nil {
			err = extra(ctx)
		}
		r.mu.Lock()
		r.order = append(r.order, i)
		r.mu.Unlock()
		r.execCount.Add(1)
		return err
	}
}

func (r *recorder) assertOrder(t *testing.T, want []int) {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.order) != len(want) {
		t.Fatalf("completion order %v, want %v", r.order, want)
	}
	for i := range want {
		if r.order[i] != want[i] {
			t.Fatalf("completion order %v, want %v", r.order, want)
		}
	}
}

func shutdown(t *testing.T, s *Scheduler) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
}

// hashByFirstByte maps keys deterministically so tests control placement.
func hashByFirstByte(key string) uint32 {
	if key == "" {
		return 0
	}
	return uint32(key[0])
}

func TestSameKeyStrictOrder(t *testing.T) {
	s := New(WithShards(4))
	defer shutdown(t, s)

	r := &recorder{}
	const n = 200
	handles := make([]*Handle, 0, n)
	for i := 0; i < n; i++ {
		handles = append(handles, s.Submit("hot", r.task(i, nil)))
	}
	for _, h := range handles {
		if err := h.Wait(); err != nil {
			t.Fatalf("task failed: %v", err)
		}
	}
	if r.violated.Load() {
		t.Fatal("same-key tasks ran concurrently")
	}
	want := make([]int, n)
	for i := range want {
		want[i] = i
	}
	r.assertOrder(t, want)
}

func TestDifferentKeysRunInParallel(t *testing.T) {
	s := New(WithShards(2), WithHashFunc(hashByFirstByte))
	defer shutdown(t, s)

	aStarted := make(chan struct{})
	bStarted := make(chan struct{})
	// "a" -> shard 0, "b" -> shard 1. a blocks until b starts; if keys were
	// serialized globally this would deadlock.
	ha := s.Submit("a", func(ctx context.Context) error {
		close(aStarted)
		select {
		case <-bStarted:
			return nil
		case <-time.After(5 * time.Second):
			return errors.New("timed out waiting for b: keys not parallel")
		}
	})
	hb := s.Submit("b", func(ctx context.Context) error {
		close(bStarted)
		return nil
	})
	if err := ha.Wait(); err != nil {
		t.Fatal(err)
	}
	if err := hb.Wait(); err != nil {
		t.Fatal(err)
	}
}

func TestRescalePreservesOrderExpandAndShrink(t *testing.T) {
	s := New(WithShards(2))
	defer shutdown(t, s)

	r := &recorder{}
	gate := make(chan struct{})
	firstStarted := make(chan struct{})
	var once sync.Once

	mk := func(i int) TaskFunc {
		return r.task(i, func(ctx context.Context) error {
			if i == 0 {
				once.Do(func() { close(firstStarted) })
				<-gate // hold task 0 so the rescale lands mid-flight
			}
			return nil
		})
	}

	var handles []*Handle
	for i := 0; i < 10; i++ {
		handles = append(handles, s.Submit("hot", mk(i)))
	}
	<-firstStarted

	// Expand while task 0 is in flight and 1..9 are queued.
	s.Rescale(8)
	if got := s.ShardCount(); got != 8 {
		t.Fatalf("ShardCount = %d, want 8", got)
	}
	// Submit more after the expansion: same key, must still be ordered.
	for i := 10; i < 20; i++ {
		handles = append(handles, s.Submit("hot", mk(i)))
	}
	// Shrink again mid-stream.
	s.Rescale(1)
	for i := 20; i < 30; i++ {
		handles = append(handles, s.Submit("hot", mk(i)))
	}

	close(gate)
	for _, h := range handles {
		if err := h.Wait(); err != nil {
			t.Fatalf("task failed: %v", err)
		}
	}
	if r.violated.Load() {
		t.Fatal("same-key tasks ran concurrently across rescale")
	}
	want := make([]int, 30)
	for i := range want {
		want[i] = i
	}
	r.assertOrder(t, want)
}

func TestMigrationWaitsForInflightHandoff(t *testing.T) {
	s := New(WithShards(2))
	defer shutdown(t, s)

	release := make(chan struct{})
	firstStarted := make(chan struct{})
	h0 := s.Submit("k", func(ctx context.Context) error {
		close(firstStarted)
		<-release
		return nil
	})
	<-firstStarted

	secondStarted := make(chan struct{})
	h1 := s.Submit("k", func(ctx context.Context) error {
		close(secondStarted)
		return nil
	})

	// Rescale while h0 is in flight: the old shard must hand off safely,
	// i.e. h1 may not start until h0 has finished.
	s.Rescale(6)
	select {
	case <-secondStarted:
		t.Fatal("queued task started before in-flight task finished: unsafe handoff")
	case <-time.After(150 * time.Millisecond):
	}

	close(release)
	select {
	case <-secondStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("queued task never started after handoff")
	}
	if err := h0.Wait(); err != nil {
		t.Fatal(err)
	}
	if err := h1.Wait(); err != nil {
		t.Fatal(err)
	}
}

func TestCancelDoesNotReorderRemaining(t *testing.T) {
	s := New(WithShards(1))
	defer shutdown(t, s)

	r := &recorder{}
	gate := make(chan struct{})
	firstStarted := make(chan struct{})
	h0 := s.Submit("k", func(ctx context.Context) error {
		close(firstStarted)
		<-gate
		return nil
	})
	<-firstStarted

	h1 := s.Submit("k", r.task(1, nil))
	h2 := s.Submit("k", r.task(2, nil))
	h3 := s.Submit("k", r.task(3, nil))

	if !h2.Cancel() {
		t.Fatal("expected queued task to be cancellable")
	}
	close(gate)

	for _, h := range []*Handle{h0, h1, h3} {
		if err := h.Wait(); err != nil {
			t.Fatalf("unexpected failure: %v", err)
		}
	}
	if err := h2.Wait(); !errors.Is(err, ErrTaskCancelled) {
		t.Fatalf("cancelled handle error = %v, want ErrTaskCancelled", err)
	}
	r.assertOrder(t, []int{1, 3})
}

func TestCancelRunningTaskIsCooperative(t *testing.T) {
	s := New(WithShards(1))
	defer shutdown(t, s)

	started := make(chan struct{})
	h := s.Submit("k", func(ctx context.Context) error {
		close(started)
		<-ctx.Done() // honor cooperative cancellation
		return ctx.Err()
	})
	<-started
	if h.Cancel() {
		t.Fatal("running task should not be removable, only context-cancelled")
	}
	if err := h.Wait(); !errors.Is(err, context.Canceled) {
		t.Fatalf("running cancelled task error = %v, want context.Canceled", err)
	}
}

func TestEmptyQueueReclaimDoesNotLoseTasks(t *testing.T) {
	s := New(WithShards(2))
	defer shutdown(t, s)

	// Repeatedly enqueue/wait from several goroutines on the same key so
	// the key's state is constantly reclaimed and recreated. Run under
	// -race; every submitted task must execute exactly once.
	var count atomic.Int32
	const goroutines = 4
	const perGoroutine = 300
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				err := s.Submit("key", func(ctx context.Context) error {
					count.Add(1)
					return nil
				}).Wait()
				if err != nil {
					t.Errorf("task failed: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()
	if got, want := count.Load(), int32(goroutines*perGoroutine); got != want {
		t.Fatalf("executed %d tasks, want %d (lost tasks during reclaim)", got, want)
	}
}

func TestHotKeyDoesNotBlockOtherShards(t *testing.T) {
	s := New(WithShards(2), WithHashFunc(hashByFirstByte))
	defer shutdown(t, s)

	release := make(chan struct{})
	hotStarted := make(chan struct{})
	hot := s.Submit("a-hot", func(ctx context.Context) error {
		close(hotStarted)
		<-release
		return nil
	})
	<-hotStarted

	// Keys hashing to the other shard must complete while the hot key's
	// task is still running.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 50; i++ {
			if err := s.Submit("b-cold", func(ctx context.Context) error { return nil }).Wait(); err != nil {
				t.Errorf("cold task failed: %v", err)
				return
			}
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("hot key blocked tasks on another shard")
	}
	close(release)
	if err := hot.Wait(); err != nil {
		t.Fatal(err)
	}
}

func TestInjectedFailureDoesNotStopQueue(t *testing.T) {
	s := New(WithShards(2))
	defer shutdown(t, s)

	boom := errors.New("boom")
	h1 := s.Submit("k", func(ctx context.Context) error { return boom })
	ran := make(chan struct{})
	h2 := s.Submit("k", func(ctx context.Context) error {
		close(ran)
		return nil
	})
	if err := h1.Wait(); !errors.Is(err, boom) {
		t.Fatalf("h1 error = %v, want boom", err)
	}
	if err := h2.Wait(); err != nil {
		t.Fatalf("h2 error = %v, want nil", err)
	}
	<-ran
}

func TestSubmitAfterShutdownRejected(t *testing.T) {
	s := New(WithShards(1))
	shutdown(t, s)
	if err := s.Submit("k", func(ctx context.Context) error { return nil }).Wait(); !errors.Is(err, ErrSchedulerClosed) {
		t.Fatalf("error = %v, want ErrSchedulerClosed", err)
	}
}

func TestStressMixedKeysUnderRescale(t *testing.T) {
	s := New(WithShards(2))
	defer shutdown(t, s)

	// Per-key order checkers under concurrent rescales.
	const keys = 8
	const perKey = 100
	var wg sync.WaitGroup
	errs := make(chan error, keys)
	for k := 0; k < keys; k++ {
		wg.Add(1)
		go func(k int) {
			defer wg.Done()
			key := fmt.Sprintf("key-%d", k)
			var mu sync.Mutex
			last := -1
			bad := false
			handles := make([]*Handle, 0, perKey)
			for i := 0; i < perKey; i++ {
				i := i
				handles = append(handles, s.Submit(key, func(ctx context.Context) error {
					mu.Lock()
					if i != last+1 {
						bad = true
					}
					last = i
					mu.Unlock()
					return nil
				}))
			}
			for _, h := range handles {
				if err := h.Wait(); err != nil {
					errs <- fmt.Errorf("key %s: %w", key, err)
					return
				}
			}
			mu.Lock()
			if bad {
				errs <- fmt.Errorf("key %s: out-of-order execution", key)
			}
			mu.Unlock()
		}(k)
	}

	// Hammer rescales while submissions are in flight.
	for i := 0; i < 20; i++ {
		s.Rescale(1 + i%6)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}
