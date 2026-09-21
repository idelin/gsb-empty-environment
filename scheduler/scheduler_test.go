package scheduler

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// waitErr waits for a future with a generous timeout so a broken scheduler
// fails the test instead of hanging it forever.
func waitErr(t *testing.T, f *Future) error {
	t.Helper()
	select {
	case err := <-f.Done():
		return err
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for task")
		return nil
	}
}

// keysOnShards returns n keys that map to n distinct shards of the
// scheduler's current generation.
func keysOnShards(t *testing.T, s *Scheduler, n int) []string {
	t.Helper()
	seen := map[int]bool{}
	var keys []string
	for i := 0; len(keys) < n && i < 100000; i++ {
		k := fmt.Sprintf("key-%d", i)
		sh := s.ShardIndex(k)
		if !seen[sh] {
			seen[sh] = true
			keys = append(keys, k)
		}
	}
	if len(keys) < n {
		t.Fatalf("could not find %d keys on distinct shards", n)
	}
	return keys
}

// sameShardPairPlusOther returns two keys mapping to the same shard and a
// third key mapping to a different shard.
func sameShardPairPlusOther(t *testing.T, s *Scheduler) (a, b, other string) {
	t.Helper()
	byShard := map[int][]string{}
	for i := 0; i < 100000; i++ {
		k := fmt.Sprintf("cand-%d", i)
		sh := s.ShardIndex(k)
		byShard[sh] = append(byShard[sh], k)
		if len(byShard) < 2 {
			continue
		}
		for sh, keys := range byShard {
			if len(keys) < 2 {
				continue
			}
			for sh2, keys2 := range byShard {
				if sh2 != sh && len(keys2) > 0 {
					return keys[0], keys[1], keys2[0]
				}
			}
		}
	}
	t.Fatal("could not find suitable keys")
	return "", "", ""
}

// TestSameKeyStrictlySerial: same-key tasks run one at a time, in exact
// submission order.
func TestSameKeyStrictlySerial(t *testing.T) {
	s := New(WithShards(4), WithWorkersPerShard(4))
	defer s.Close()

	const n = 200
	var order []int
	var running, maxRunning int32

	var futures []*Future
	for i := 0; i < n; i++ {
		i := i
		f, err := s.Submit("same-key", func(ctx context.Context) error {
			cur := atomic.AddInt32(&running, 1)
			for {
				old := atomic.LoadInt32(&maxRunning)
				if cur <= old || atomic.CompareAndSwapInt32(&maxRunning, old, cur) {
					break
				}
			}
			time.Sleep(time.Millisecond) // injected timing
			order = append(order, i)
			atomic.AddInt32(&running, -1)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		futures = append(futures, f)
	}
	for _, f := range futures {
		if err := waitErr(t, f); err != nil {
			t.Fatal(err)
		}
	}
	if got := atomic.LoadInt32(&maxRunning); got != 1 {
		t.Fatalf("same-key concurrency = %d, want 1", got)
	}
	if len(order) != n {
		t.Fatalf("executed %d tasks, want %d", len(order), n)
	}
	for i := range order {
		if order[i] != i {
			t.Fatalf("order violated at position %d: got %d", i, order[i])
		}
	}
}

// TestDifferentKeysParallel: keys on different shards run concurrently; a
// single slow key cannot starve the others.
func TestDifferentKeysParallel(t *testing.T) {
	s := New(WithShards(4), WithWorkersPerShard(1)) // one worker per shard
	defer s.Close()

	keys := keysOnShards(t, s, 4)
	started := make(chan string, len(keys))
	release := make(chan struct{})

	for _, k := range keys {
		k := k
		if _, err := s.Submit(k, func(ctx context.Context) error {
			started <- k
			<-release // block until every key's task has started
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.After(10 * time.Second)
	for range keys {
		select {
		case <-started:
		case <-deadline:
			t.Fatal("keys on different shards did not run in parallel")
		}
	}
	close(release)
}

// TestReshardPreservesOrderAcrossGenerations: tasks submitted before AND
// after scale-up/scale-down for the same key still complete in submission
// order.
func TestReshardPreservesOrderAcrossGenerations(t *testing.T) {
	s := New(WithShards(2), WithWorkersPerShard(2))
	defer s.Close()

	const n = 60
	gate := make(chan struct{}) // holds the head task so queues build up
	var order []int
	var futures []*Future
	submit := func(i int) {
		f, err := s.Submit("migrating-key", func(ctx context.Context) error {
			<-gate
			order = append(order, i)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		futures = append(futures, f)
	}

	for i := 0; i < n/3; i++ { // first third on generation 0 (2 shards)
		submit(i)
	}
	s.Reshard(8)                     // scale up
	for i := n / 3; i < 2*n/3; i++ { // second third on generation 1
		submit(i)
	}
	s.Reshard(1)                     // scale down
	s.Reshard(16)                    // and up again
	for i := 2 * n / 3; i < n; i++ { // last third on generation 3
		submit(i)
	}
	close(gate) // release the chain; it now drains across generations

	for _, f := range futures {
		if err := waitErr(t, f); err != nil {
			t.Fatal(err)
		}
	}
	if len(order) != n {
		t.Fatalf("executed %d tasks, want %d", len(order), n)
	}
	for i := range order {
		if order[i] != i {
			t.Fatalf("order violated at position %d: got %d (reshard broke per-key order)", i, order[i])
		}
	}
	if got := s.ShardCount(); got != 16 {
		t.Fatalf("shard count = %d, want 16", got)
	}
}

// TestReshardWaitsForHandoff: Reshard does not return until the old shard
// generation has handed off all queued-but-unstarted work.
func TestReshardWaitsForHandoff(t *testing.T) {
	s := New(WithShards(2), WithWorkersPerShard(1))
	defer s.Close()

	// a and b share a shard; c lives on another shard. With one worker per
	// shard, each shard's queue holds one dispatched-but-unstarted unit
	// once the heads are running.
	a, b, c := sameShardPairPlusOther(t, s)
	block := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(block) }) }
	defer release() // never leave Close waiting on a blocked task

	headStarted := make(chan string, 2)
	blocking := func(name string) TaskFunc {
		return func(ctx context.Context) error {
			headStarted <- name
			<-block
			return nil
		}
	}
	headA, err := s.Submit(a, blocking("a"))
	if err != nil {
		t.Fatal(err)
	}
	headC, err := s.Submit(c, blocking("c"))
	if err != nil {
		t.Fatal(err)
	}
	<-headStarted
	<-headStarted

	// These are dispatched onto the old generation but cannot start: the
	// single workers are occupied. They sit in the old shard queues.
	queuedB, err := s.Submit(b, func(ctx context.Context) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	queuedC, err := s.Submit(c, func(ctx context.Context) error { return nil })
	if err != nil {
		t.Fatal(err)
	}

	resharded := make(chan struct{})
	go func() {
		s.Reshard(6)
		close(resharded)
	}()

	select {
	case <-resharded:
		t.Fatal("Reshard returned while old shards still held queued work")
	case <-time.After(100 * time.Millisecond):
	}

	release() // let the old generation drain

	select {
	case <-resharded:
	case <-time.After(10 * time.Second):
		t.Fatal("Reshard did not return after old shards drained")
	}
	if got := s.Epoch(); got != 1 {
		t.Fatalf("epoch = %d, want 1", got)
	}
	// Everything submitted before the reshard completes; nothing is lost
	// in the handoff.
	for _, f := range []*Future{headA, headC, queuedB, queuedC} {
		if err := waitErr(t, f); err != nil {
			t.Fatal(err)
		}
	}
}

// TestCancelPendingKeepsOrder: canceling pending tasks neither reorders nor
// drops the remaining ones.
func TestCancelPendingKeepsOrder(t *testing.T) {
	s := New(WithShards(4), WithWorkersPerShard(2))
	defer s.Close()

	const n = 20
	gate := make(chan struct{})
	var order []int
	var futures []*Future
	for i := 0; i < n; i++ {
		i := i
		f, err := s.Submit("cancel-key", func(ctx context.Context) error {
			<-gate
			order = append(order, i)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		futures = append(futures, f)
	}

	// Cancel every third task while the head is still gated, i.e. all of
	// them are still pending (except the dispatched head).
	canceled := map[int]bool{}
	for i := 2; i < n; i += 3 {
		if !futures[i].Cancel() {
			t.Fatalf("cancel of pending task %d reported false", i)
		}
		canceled[i] = true
	}
	close(gate)

	for i, f := range futures {
		err := waitErr(t, f)
		if canceled[i] {
			if !errors.Is(err, ErrCanceled) {
				t.Fatalf("task %d: got %v, want ErrCanceled", i, err)
			}
		} else if err != nil {
			t.Fatalf("task %d: unexpected error %v", i, err)
		}
	}
	want := 0
	for _, got := range order {
		for canceled[want] {
			want++
		}
		if got != want {
			t.Fatalf("remaining order violated: got %d, want %d", got, want)
		}
		want++
	}
	if len(order)+len(canceled) != n {
		t.Fatalf("executed %d + canceled %d != %d", len(order), len(canceled), n)
	}
}

// TestCancelRunningTask: canceling the running task cancels its context and
// reports ErrCanceled; the key's chain continues in order.
func TestCancelRunningTask(t *testing.T) {
	s := New(WithShards(2), WithWorkersPerShard(1))
	defer s.Close()

	headStarted := make(chan struct{})
	f1, err := s.Submit("k", func(ctx context.Context) error {
		close(headStarted)
		<-ctx.Done() // observe cancellation
		return ctx.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	<-headStarted
	if !f1.Cancel() {
		t.Fatal("cancel of running task reported false")
	}
	if err := waitErr(t, f1); !errors.Is(err, ErrCanceled) {
		t.Fatalf("running canceled task: got %v, want ErrCanceled", err)
	}

	ran := make(chan struct{})
	f2, err := s.Submit("k", func(ctx context.Context) error {
		close(ran)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := waitErr(t, f2); err != nil {
		t.Fatalf("successor of canceled task failed: %v", err)
	}
	<-ran
}

// TestEmptyQueueReclaimRace: empty key-queue reclamation must not race with
// fresh enqueues for the same key — no task may be lost.
func TestEmptyQueueReclaimRace(t *testing.T) {
	s := New(WithShards(4), WithWorkersPerShard(4))
	defer s.Close()

	const writers = 8
	const perWriter = 300
	var executed int64

	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				f, err := s.Submit("recycled-key", func(ctx context.Context) error {
					atomic.AddInt64(&executed, 1)
					return nil
				})
				if err != nil {
					t.Error(err)
					return
				}
				if err := f.Wait(); err != nil { // queue fully drains between submits
					t.Error(err)
					return
				}
			}
		}()
	}
	wg.Wait()

	if got := atomic.LoadInt64(&executed); got != writers*perWriter {
		t.Fatalf("executed %d tasks, want %d (reclaim raced with enqueue)", got, writers*perWriter)
	}
	deadline := time.Now().Add(5 * time.Second)
	for s.KeyCount() != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := s.KeyCount(); got != 0 {
		t.Fatalf("KeyCount = %d after drain, want 0 (queue leaked)", got)
	}
}

// TestHotKeyDoesNotBlockOtherShards: one hot key monopolizing its shard's
// workers must not delay keys living on other shards.
func TestHotKeyDoesNotBlockOtherShards(t *testing.T) {
	s := New(WithShards(4), WithWorkersPerShard(1))
	defer s.Close()

	keys := keysOnShards(t, s, 4)
	hot, others := keys[0], keys[1:]

	hotRunning := make(chan struct{})
	releaseHot := make(chan struct{})
	if _, err := s.Submit(hot, func(ctx context.Context) error {
		close(hotRunning)
		<-releaseHot // hot key parks on its shard
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	<-hotRunning

	done := make(chan struct{}, len(others))
	for _, k := range others {
		k := k
		if _, err := s.Submit(k, func(ctx context.Context) error {
			done <- struct{}{}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.After(10 * time.Second)
	for range others {
		select {
		case <-done:
		case <-deadline:
			t.Fatal("hot key blocked tasks on other shards")
		}
	}
	close(releaseHot)
}

// TestFailureInjection: a failing task delivers its error to its own future
// only; the key's chain continues in order.
func TestFailureInjection(t *testing.T) {
	s := New(WithShards(2), WithWorkersPerShard(2))
	defer s.Close()

	boom := errors.New("injected failure")
	var order []int
	var futures []*Future
	for i := 0; i < 10; i++ {
		i := i
		f, err := s.Submit("flaky-key", func(ctx context.Context) error {
			order = append(order, i)
			if i == 4 {
				return boom
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		futures = append(futures, f)
	}
	for i, f := range futures {
		err := waitErr(t, f)
		if i == 4 {
			if !errors.Is(err, boom) {
				t.Fatalf("task 4: got %v, want injected failure", err)
			}
		} else if err != nil {
			t.Fatalf("task %d: unexpected error %v", i, err)
		}
	}
	for i := range order {
		if order[i] != i {
			t.Fatalf("order violated after failure at position %d", i)
		}
	}
}

// TestCloseDrains: Close waits for queued and running work, then rejects
// new submissions.
func TestCloseDrains(t *testing.T) {
	s := New(WithShards(2), WithWorkersPerShard(2))

	const n = 50
	var executed int64
	var futures []*Future
	for i := 0; i < n; i++ {
		f, err := s.Submit(fmt.Sprintf("k%d", i%5), func(ctx context.Context) error {
			time.Sleep(time.Millisecond)
			atomic.AddInt64(&executed, 1)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		futures = append(futures, f)
	}
	s.Close()
	for _, f := range futures {
		if err := waitErr(t, f); err != nil {
			t.Fatal(err)
		}
	}
	if got := atomic.LoadInt64(&executed); got != n {
		t.Fatalf("Close returned with %d/%d tasks executed", got, n)
	}
	if _, err := s.Submit("late", func(ctx context.Context) error { return nil }); !errors.Is(err, ErrClosed) {
		t.Fatalf("Submit after Close: got %v, want ErrClosed", err)
	}
}
