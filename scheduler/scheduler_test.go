package scheduler

import (
	"context"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func waitFor(t *testing.T, what string, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for: %s", what)
}

func completedOf(s *Scheduler, id string) int64 {
	st, _ := s.Stats(id)
	return st.Completed
}

// TestWeightedFairness verifies DRR gives service proportional to
// weights: two saturated tenants with weights 1:3 must complete tasks
// in roughly a 1:3 ratio.
func TestWeightedFairness(t *testing.T) {
	s := New(4, 1)
	defer s.Stop()
	s.RegisterTenant("light", TenantConfig{Weight: 1, MaxConcurrent: 16, MaxQueue: 20000})
	s.RegisterTenant("heavy", TenantConfig{Weight: 3, MaxConcurrent: 16, MaxQueue: 20000})

	var lightDone, heavyDone int64
	mk := func(counter *int64) func(context.Context) {
		return func(ctx context.Context) {
			atomic.AddInt64(counter, 1)
			time.Sleep(200 * time.Microsecond)
		}
	}
	const n = 6000
	for i := 0; i < n; i++ {
		if err := s.Submit(Task{Tenant: "light", Run: mk(&lightDone)}); err != nil {
			t.Fatalf("submit light: %v", err)
		}
		if err := s.Submit(Task{Tenant: "heavy", Run: mk(&heavyDone)}); err != nil {
			t.Fatalf("submit heavy: %v", err)
		}
	}

	// Sample the ratio mid-stream while both tenants are saturated.
	waitFor(t, "2000 completions", 30*time.Second, func() bool {
		return atomic.LoadInt64(&lightDone)+atomic.LoadInt64(&heavyDone) >= 2000
	})
	l := float64(atomic.LoadInt64(&lightDone))
	h := float64(atomic.LoadInt64(&heavyDone))
	ratio := h / l
	t.Logf("mid-stream completions: light=%.0f heavy=%.0f ratio=%.2f (want ~3.0)", l, h, ratio)
	if ratio < 1.8 || ratio > 5.0 {
		t.Fatalf("unfair scheduling: heavy/light ratio = %.2f, want ~3.0", ratio)
	}

	waitFor(t, "all tasks complete", 60*time.Second, func() bool {
		return atomic.LoadInt64(&lightDone) == n && atomic.LoadInt64(&heavyDone) == n
	})
}

// TestNoStarvation: a tenant flooding the scheduler must not prevent
// a small tenant's tasks from completing promptly.
func TestNoStarvation(t *testing.T) {
	s := New(2, 1)
	defer s.Stop()
	s.RegisterTenant("flood", TenantConfig{Weight: 8, MaxConcurrent: 8, MaxQueue: 100000})
	s.RegisterTenant("small", TenantConfig{Weight: 1, MaxConcurrent: 8, MaxQueue: 1000})

	stopFlood := make(chan struct{})
	var floodCount int64
	go func() {
		for {
			select {
			case <-stopFlood:
				return
			default:
			}
			if err := s.Submit(Task{Tenant: "flood", Run: func(ctx context.Context) {
				atomic.AddInt64(&floodCount, 1)
				time.Sleep(time.Millisecond)
			}}); err != nil {
				time.Sleep(time.Millisecond)
			}
		}
	}()
	defer close(stopFlood)

	// The small tenant's tasks must all complete despite the flood.
	var smallDone int64
	const smallTasks = 20
	for i := 0; i < smallTasks; i++ {
		if err := s.Submit(Task{Tenant: "small", Run: func(ctx context.Context) {
			atomic.AddInt64(&smallDone, 1)
		}}); err != nil {
			t.Fatalf("submit small: %v", err)
		}
	}
	waitFor(t, "small tenant tasks complete under flood", 30*time.Second, func() bool {
		return atomic.LoadInt64(&smallDone) == smallTasks
	})
}

// TestQueueQuota verifies backpressure: submissions beyond MaxQueue
// are rejected with ErrQuotaExceeded and counted.
func TestQueueQuota(t *testing.T) {
	s := New(1, 1)
	defer s.Stop()
	const maxQueue = 5
	s.RegisterTenant("t", TenantConfig{Weight: 1, MaxConcurrent: 1, MaxQueue: maxQueue})

	block := make(chan struct{})
	started := make(chan struct{}, 1)
	blocking := func(ctx context.Context) {
		started <- struct{}{}
		<-block // hold the single worker slot
	}
	if err := s.Submit(Task{Tenant: "t", Run: blocking}); err != nil {
		t.Fatalf("submit blocking task: %v", err)
	}
	<-started

	var rejected int
	for i := 0; i < maxQueue+3; i++ {
		err := s.Submit(Task{Tenant: "t", Run: func(ctx context.Context) { <-block }})
		if err == ErrQuotaExceeded {
			rejected++
		} else if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if rejected != 3 {
		t.Fatalf("rejected = %d, want 3", rejected)
	}
	st, _ := s.Stats("t")
	if st.Rejected != 3 {
		t.Fatalf("stats rejected = %d, want 3", st.Rejected)
	}
	if st.Queued != maxQueue {
		t.Fatalf("queued = %d, want %d", st.Queued, maxQueue)
	}
	close(block)
	waitFor(t, "drain", 30*time.Second, func() bool {
		st, _ := s.Stats("t")
		return st.Completed == int64(maxQueue)+1
	})
}

// TestMaxConcurrentIsolation verifies a tenant's concurrency cap is
// never exceeded and a blocked tenant does not block others.
func TestMaxConcurrentIsolation(t *testing.T) {
	s := New(8, 1)
	defer s.Stop()
	s.RegisterTenant("stuck", TenantConfig{Weight: 1, MaxConcurrent: 1, MaxQueue: 100})
	s.RegisterTenant("free", TenantConfig{Weight: 1, MaxConcurrent: 4, MaxQueue: 1000})

	var cur, maxSeen int64
	release := make(chan struct{})
	stuckTask := func(ctx context.Context) {
		c := atomic.AddInt64(&cur, 1)
		for {
			m := atomic.LoadInt64(&maxSeen)
			if c <= m || atomic.CompareAndSwapInt64(&maxSeen, m, c) {
				break
			}
		}
		<-release
		atomic.AddInt64(&cur, -1)
	}
	for i := 0; i < 3; i++ {
		if err := s.Submit(Task{Tenant: "stuck", Run: stuckTask}); err != nil {
			t.Fatalf("submit stuck: %v", err)
		}
	}

	// While "stuck" is blocked, "free" tasks must still run.
	var freeDone int64
	const freeTasks = 30
	for i := 0; i < freeTasks; i++ {
		if err := s.Submit(Task{Tenant: "free", Run: func(ctx context.Context) {
			atomic.AddInt64(&freeDone, 1)
		}}); err != nil {
			t.Fatalf("submit free: %v", err)
		}
	}
	waitFor(t, "free tenant completes while stuck tenant blocked", 30*time.Second, func() bool {
		return atomic.LoadInt64(&freeDone) == freeTasks
	})
	if m := atomic.LoadInt64(&maxSeen); m > 1 {
		t.Fatalf("stuck tenant concurrency reached %d, cap is 1", m)
	}
	close(release)
	waitFor(t, "stuck tenant drains", 30*time.Second, func() bool {
		st, _ := s.Stats("stuck")
		return st.Completed == 3
	})
}

// TestConcurrentSubmit hammers the scheduler from many goroutines and
// verifies no task is lost (run with -race).
func TestConcurrentSubmit(t *testing.T) {
	s := New(4, 1)
	defer s.Stop()
	tenants := []string{"a", "b", "c", "d"}
	for _, id := range tenants {
		s.RegisterTenant(id, TenantConfig{Weight: 1, MaxConcurrent: 8, MaxQueue: 100000})
	}

	var done int64
	const writers = 16
	const perWriter = 500
	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				task := Task{
					Tenant: tenants[(w+i)%len(tenants)],
					Run:    func(ctx context.Context) { atomic.AddInt64(&done, 1) },
				}
				if err := s.Submit(task); err != nil {
					t.Errorf("submit: %v", err)
					return
				}
			}
		}(w)
	}
	wg.Wait()
	total := int64(writers * perWriter)
	waitFor(t, "all concurrent submissions complete", 60*time.Second, func() bool {
		return atomic.LoadInt64(&done) == total
	})
	for _, id := range tenants {
		st, _ := s.Stats(id)
		if st.Completed != st.Submitted {
			t.Fatalf("tenant %s: completed %d != submitted %d", id, st.Completed, st.Submitted)
		}
		if st.Inflight != 0 || st.Queued != 0 {
			t.Fatalf("tenant %s: inflight=%d queued=%d after drain", id, st.Inflight, st.Queued)
		}
	}
}

// TestCostAwareness: tasks with higher cost consume more deficit, so a
// tenant submitting expensive tasks gets proportionally fewer through.
// With one worker the completion order equals the dispatch order, so
// the first completions must be dominated by cheap tasks.
func TestCostAwareness(t *testing.T) {
	s := New(1, 10)
	defer s.Stop()
	s.RegisterTenant("cheap", TenantConfig{Weight: 1, MaxConcurrent: 4, MaxQueue: 10000})
	s.RegisterTenant("pricey", TenantConfig{Weight: 1, MaxConcurrent: 4, MaxQueue: 10000})

	var mu sync.Mutex
	var order []string
	record := func(id string) func(context.Context) {
		return func(ctx context.Context) {
			mu.Lock()
			order = append(order, id)
			mu.Unlock()
		}
	}
	for i := 0; i < 100; i++ {
		if err := s.Submit(Task{Tenant: "cheap", Cost: 1, Run: record("cheap")}); err != nil {
			t.Fatal(err)
		}
		if err := s.Submit(Task{Tenant: "pricey", Cost: 10, Run: record("pricey")}); err != nil {
			t.Fatal(err)
		}
	}
	waitFor(t, "22 completions", 30*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(order) >= 22
	})
	mu.Lock()
	first := append([]string(nil), order[:22]...)
	mu.Unlock()
	cheap, pricey := 0, 0
	for _, id := range first {
		if id == "cheap" {
			cheap++
		} else {
			pricey++
		}
	}
	t.Logf("first 22 completions: cheap=%d pricey=%d (quantum=10, costs 1 vs 10)", cheap, pricey)
	if cheap < 15 {
		t.Fatalf("cheap tasks should dominate early completions: cheap=%d pricey=%d", cheap, pricey)
	}
}

// TestSubmitErrors covers unknown tenant and post-stop submission.
func TestSubmitErrors(t *testing.T) {
	s := New(1, 1)
	s.RegisterTenant("x", TenantConfig{})
	if err := s.Submit(Task{Tenant: "ghost"}); err != ErrUnknownTenant {
		t.Fatalf("want ErrUnknownTenant, got %v", err)
	}
	s.Stop()
	if err := s.Submit(Task{Tenant: "x"}); err != ErrStopped {
		t.Fatalf("want ErrStopped, got %v", err)
	}
	// Double stop must not panic or hang.
	s.Stop()
}

// TestTaskPanicIsolated: a panicking tenant task must not kill workers
// or lose accounting.
func TestTaskPanicIsolated(t *testing.T) {
	s := New(2, 1)
	defer s.Stop()
	s.RegisterTenant("bad", TenantConfig{MaxQueue: 10})
	s.RegisterTenant("good", TenantConfig{MaxQueue: 10})

	if err := s.Submit(Task{Tenant: "bad", Run: func(ctx context.Context) {
		panic("boom")
	}}); err != nil {
		t.Fatal(err)
	}
	var goodDone int64
	for i := 0; i < 10; i++ {
		if err := s.Submit(Task{Tenant: "good", Run: func(ctx context.Context) {
			atomic.AddInt64(&goodDone, 1)
		}}); err != nil {
			t.Fatal(err)
		}
	}
	waitFor(t, "good tasks survive tenant panic", 30*time.Second, func() bool {
		return atomic.LoadInt64(&goodDone) == 10
	})
	waitFor(t, "bad tenant accounting settled", 30*time.Second, func() bool {
		st, _ := s.Stats("bad")
		return st.Completed == 1 && st.Inflight == 0
	})
}

// TestNoGoroutineLeak asserts the scheduler leaves no goroutines or
// timers behind after Stop.
func TestNoGoroutineLeak(t *testing.T) {
	before := runtime.NumGoroutine()
	for round := 0; round < 3; round++ {
		s := New(4, 1)
		s.RegisterTenant("t", TenantConfig{MaxQueue: 1000})
		var done int64
		for i := 0; i < 200; i++ {
			if err := s.Submit(Task{Tenant: "t", Run: func(ctx context.Context) {
				atomic.AddInt64(&done, 1)
			}}); err != nil {
				t.Fatal(err)
			}
		}
		waitFor(t, "round drain", 30*time.Second, func() bool {
			return atomic.LoadInt64(&done) == 200
		})
		s.Stop()
	}
	waitFor(t, "goroutines to settle", 30*time.Second, func() bool {
		return runtime.NumGoroutine() <= before
	})
}

// TestStopDrainsQueue: Stop must finish already-queued work rather
// than dropping it.
func TestStopDrainsQueue(t *testing.T) {
	s := New(2, 1)
	s.RegisterTenant("t", TenantConfig{MaxQueue: 10000})
	var done int64
	const n = 500
	for i := 0; i < n; i++ {
		if err := s.Submit(Task{Tenant: "t", Run: func(ctx context.Context) {
			atomic.AddInt64(&done, 1)
		}}); err != nil {
			t.Fatal(err)
		}
	}
	s.Stop()
	if got := atomic.LoadInt64(&done); got != n {
		t.Fatalf("completed = %d after Stop, want %d (queued work must drain)", got, n)
	}
	st, _ := s.Stats("t")
	if st.Inflight != 0 || st.Queued != 0 {
		t.Fatalf("after Stop: inflight=%d queued=%d", st.Inflight, st.Queued)
	}
}
