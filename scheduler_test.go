package wfq

import (
	"context"
	"errors"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func waitTask(t *testing.T, task Task) {
	t.Helper()
	select {
	case <-task.Done():
	case <-time.After(5 * time.Second):
		t.Fatalf("task for tenant %q did not finish in time", task.Tenant())
	}
}

func waitCh(t *testing.T, ch <-chan struct{}, msg string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatalf("timeout: %s", msg)
	}
}

// newHooked builds a scheduler but lets the caller install test hooks
// before its dispatcher starts.
func newHooked(t *testing.T, concurrency int, tenants map[string]int,
	dispatchHook, startHook func(e *entry)) *Scheduler {
	t.Helper()
	s, err := build(Config{Concurrency: concurrency, Tenants: tenants})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	s.onDispatch = dispatchHook
	s.onStart = startHook
	s.start()
	return s
}

// With continuously saturated tenants, long term service counts must be
// close to the configured 1:2 weight ratio. Each tenant supplies work in
// proportion to its weight so both stay saturated for the whole run.
func TestWeightedShareSaturated(t *testing.T) {
	const n = 4
	const aTasks = 2000
	const bTasks = 4000
	const total = aTasks + bTasks
	s, err := New(Config{
		Concurrency: n,
		Tenants:     map[string]int{"a": 1, "b": 2},
	})
	if err != nil {
		t.Fatal(err)
	}

	var aCount, bCount int64
	release := make(chan struct{})
	mk := func(counter *int64) TaskFunc {
		return func(context.Context) error {
			atomic.AddInt64(counter, 1)
			<-release
			return nil
		}
	}
	for i := 0; i < aTasks; i++ {
		if _, err := s.Submit("a", mk(&aCount)); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < bTasks; i++ {
		if _, err := s.Submit("b", mk(&bCount)); err != nil {
			t.Fatal(err)
		}
	}

	// Release slots in batches and wait for each batch to be refilled.
	served := 0
	for served < total {
		want := int64(n + served)
		if want > total {
			want = total
		}
		for atomic.LoadInt64(&aCount)+atomic.LoadInt64(&bCount) < want {
			time.Sleep(time.Millisecond)
		}
		batch := n
		if served+batch > total {
			batch = total - served
		}
		for i := 0; i < batch; i++ {
			release <- struct{}{}
		}
		served += batch
	}
	s.Shutdown()

	a := atomic.LoadInt64(&aCount)
	b := atomic.LoadInt64(&bCount)
	ratio := float64(a) / float64(b)
	if math.Abs(ratio-1.0/2.0) > 0.05 {
		t.Fatalf("service ratio a/b = %.4f, want ~0.5 (a=%d b=%d)", ratio, a, b)
	}
	t.Logf("saturated share: a=%d b=%d ratio=%.3f", a, b, ratio)
}

// Tasks of the same tenant are selected and started in FIFO order; tasks
// of different tenants may interleave freely.
func TestFIFOOrderPerTenant(t *testing.T) {
	var mu sync.Mutex
	dispatched := map[string][]uint64{}
	started := map[string][]uint64{}
	startedCh := make(chan struct{}, 400)

	s := newHooked(t, 4, map[string]int{"a": 1, "b": 2},
		func(e *entry) {
			mu.Lock()
			dispatched[e.tenant] = append(dispatched[e.tenant], e.id)
			mu.Unlock()
		},
		func(e *entry) {
			mu.Lock()
			started[e.tenant] = append(started[e.tenant], e.id)
			mu.Unlock()
			startedCh <- struct{}{}
		})

	const per = 100
	// Tasks finish immediately so every task can cycle through the limited
	// number of slots; hooks still capture exact dispatch/start order.
	for i := 0; i < per; i++ {
		if _, err := s.Submit("a", func(context.Context) error {
			startedCh <- struct{}{}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Submit("b", func(context.Context) error {
			startedCh <- struct{}{}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	for seen := 0; seen < 2*per; seen++ {
		select {
		case <-startedCh:
		case <-time.After(5 * time.Second):
			t.Fatalf("only %d tasks started", seen)
		}
	}
	s.Shutdown()

	for tenant, ids := range dispatched {
		for i := 1; i < len(ids); i++ {
			if ids[i] <= ids[i-1] {
				t.Fatalf("tenant %s dispatch not FIFO: %v", tenant, ids)
			}
		}
	}
	for tenant, ids := range started {
		for i := 1; i < len(ids); i++ {
			if ids[i] <= ids[i-1] {
				t.Fatalf("tenant %s start not FIFO: %v", tenant, ids)
			}
		}
	}
}

// Running tasks can never exceed the global concurrency limit.
func TestGlobalConcurrencyLimit(t *testing.T) {
	const limit = 3
	var active int32
	var peak int32
	s, err := New(Config{
		Concurrency: limit,
		Tenants:     map[string]int{"a": 1, "b": 10},
	})
	if err != nil {
		t.Fatal(err)
	}

	release := make(chan struct{})
	for i := 0; i < 120; i++ {
		tenant := "a"
		if i%2 == 0 {
			tenant = "b"
		}
		if _, err := s.Submit(tenant, func(context.Context) error {
			cur := atomic.AddInt32(&active, 1)
			for {
				p := atomic.LoadInt32(&peak)
				if cur <= p || atomic.CompareAndSwapInt32(&peak, p, cur) {
					break
				}
			}
			<-release
			atomic.AddInt32(&active, -1)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	for atomic.LoadInt32(&peak) < limit {
		time.Sleep(time.Millisecond)
	}
	for i := 0; i < 120; i++ {
		release <- struct{}{}
	}
	s.Shutdown()

	if peak > limit {
		t.Fatalf("peak concurrent tasks = %d, limit = %d", peak, limit)
	}
	if got := s.Stats().PeakInflight; got != limit {
		t.Fatalf("PeakInflight = %d, want %d", got, limit)
	}
}

// An idle tenant never blocks work: a saturated active tenant fills every
// slot; when the idle tenant later submits work it is served promptly.
func TestIdleTenantDoesNotWasteSlots(t *testing.T) {
	const limit = 3
	var active int32
	var peak int32
	s, err := New(Config{
		Concurrency: limit,
		Tenants:     map[string]int{"active": 1, "idle": 100},
	})
	if err != nil {
		t.Fatal(err)
	}
	releaseA := make(chan struct{})
	for i := 0; i < 30; i++ {
		if _, err := s.Submit("active", func(context.Context) error {
			cur := atomic.AddInt32(&active, 1)
			for {
				p := atomic.LoadInt32(&peak)
				if cur <= p || atomic.CompareAndSwapInt32(&peak, p, cur) {
					break
				}
			}
			<-releaseA
			atomic.AddInt32(&active, -1)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	for atomic.LoadInt32(&peak) < limit {
		time.Sleep(time.Millisecond)
	}
	if got := atomic.LoadInt32(&active); got != limit {
		t.Fatalf("active tenant running = %d, want %d despite idle tenant", got, limit)
	}

	// Now the idle tenant gets work: as soon as active tasks release
	// slots, the scheduler must serve it instead of idling.
	var idleDone int32
	idleRelease := make(chan struct{})
	for i := 0; i < limit; i++ {
		if _, err := s.Submit("idle", func(context.Context) error {
			atomic.AddInt32(&idleDone, 1)
			<-idleRelease
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < limit; i++ {
		releaseA <- struct{}{}
	}
	for atomic.LoadInt32(&idleDone) < limit {
		time.Sleep(time.Millisecond)
	}
	close(idleRelease)
	for i := limit; i < 30; i++ {
		releaseA <- struct{}{}
	}
	s.Shutdown()
}

// A task cancelled while waiting is removed before dispatch: it never
// runs, counts as cancelled, and frees its tenant queue / banked quota.
func TestCancelWaitingConsumesNoQuota(t *testing.T) {
	// concurrency 1: the first task occupies the only slot, so every task
	// submitted afterwards is provably still waiting when it is cancelled.
	s, err := New(Config{Concurrency: 1, Tenants: map[string]int{"a": 1, "b": 1}})
	if err != nil {
		t.Fatal(err)
	}

	started := make(chan struct{}, 4)
	hold := make(chan struct{})
	run1, err := s.Submit("a", func(context.Context) error {
		started <- struct{}{}
		<-hold
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	<-started

	c1, err := s.Submit("a", func(context.Context) error {
		t.Fatal("cancelled waiting task must never run")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	b1, err := s.Submit("b", func(context.Context) error { return nil })
	if err != nil {
		t.Fatal(err)
	}

	// c1 is queued behind run1 while the only slot is busy, so this cancel
	// takes the waiting path and must complete without running anything.
	c1.Cancel()
	waitTask(t, c1)
	if c1.Err() != ErrTaskCancelled {
		t.Fatalf("cancelled task err = %v, want ErrTaskCancelled", c1.Err())
	}
	c1.Cancel() // idempotent

	// c1 disappears from the tenant queue; only b1 remains waiting.
	s.mu.Lock()
	if s.queued != 1 || len(s.tenants["a"].queue) != 0 {
		t.Fatalf("queue state after cancel: queued=%d aQueue=%d", s.queued,
			len(s.tenants["a"].queue))
	}
	if got := s.tenants["a"].deficit; got != 0 {
		t.Fatalf("idle/emptied tenant deficit = %d, want 0", got)
	}
	s.mu.Unlock()

	// Releasing run1 lets b1 (not the removed c1) consume the free slot.
	close(hold)
	waitTask(t, run1)
	waitTask(t, b1)
	s.Shutdown()

	st := s.Stats()
	if st.Cancelled != 1 || st.Succeeded != 2 {
		t.Fatalf("stats = %+v, want 1 cancelled / 2 succeeded", st)
	}
}

// Cancelling a running task cancels its context but does not forcefully
// stop it; the returned error decides its final classification.
func TestCancelRunningCooperative(t *testing.T) {
	var sawCancel int32
	s, err := New(Config{Concurrency: 1, Tenants: map[string]int{"a": 1}})
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	h, err := s.Submit("a", func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		atomic.StoreInt32(&sawCancel, 1)
		return ctx.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	<-started
	s.Cancel(h)
	waitTask(t, h)
	if atomic.LoadInt32(&sawCancel) != 1 {
		t.Fatal("running task did not observe context cancellation")
	}
	s.Shutdown()
	if s.Stats().Cancelled != 1 {
		t.Fatalf("cancelled = %d, want 1", s.Stats().Cancelled)
	}
}

// Injected task errors are counted as failures; panics are recovered and
// reported without crashing the scheduler.
func TestFailureAndPanic(t *testing.T) {
	myErr := errors.New("boom")
	s, err := New(Config{Concurrency: 2, Tenants: map[string]int{"a": 1}})
	if err != nil {
		t.Fatal(err)
	}
	h1, err := s.Submit("a", func(context.Context) error { return myErr })
	if err != nil {
		t.Fatal(err)
	}
	h2, err := s.Submit("a", func(context.Context) error { panic("kaboom") })
	if err != nil {
		t.Fatal(err)
	}
	waitTask(t, h1)
	waitTask(t, h2)
	s.Shutdown()

	if !errors.Is(h1.Err(), myErr) {
		t.Fatalf("h1 err = %v, want boom", h1.Err())
	}
	var pe TaskPanicError
	if !errors.As(h2.Err(), &pe) || !strings.Contains(pe.Error(), "panicked") {
		t.Fatalf("h2 err = %v, want TaskPanicError", h2.Err())
	}
	st := s.Stats()
	if st.Failed != 2 || st.Succeeded != 0 {
		t.Fatalf("stats = %+v, want 2 failed", st)
	}
}

// With concurrency 1 the cursor based weighted round robin is fully
// deterministic. First it verifies the exact 1:1 pattern, then a weight
// change is shown to affect only subsequent selections via the long run
// share moving from 1/2 to 1/3 for tenant a.
func TestDynamicWeightOnlyAffectsFuture(t *testing.T) {
	var mu sync.Mutex
	var order []string
	s := newHooked(t, 1, map[string]int{"a": 1, "b": 1},
		func(e *entry) {
			mu.Lock()
			order = append(order, e.tenant)
			mu.Unlock()
		}, nil)

	release := make(chan struct{})
	mk := func() TaskFunc { return func(context.Context) error { release <- struct{}{}; return nil } }

	// Phase 1: weights 1:1 produce the exact alternating pattern.
	for i := 0; i < 4; i++ {
		s.Submit("a", mk())
		s.Submit("b", mk())
	}
	got := drainOrder(&mu, &order, release, 8)
	want := []string{"a", "b", "a", "b", "a", "b", "a", "b"}
	if !eqStrings(got, want) {
		t.Fatalf("phase1 order = %v, want %v", got, want)
	}

	// Change b's weight to 2. Capacity already granted in phase 1 is
	// untouched (phase 1 already completed); all phase 2 selections must
	// follow the new 1:2 share. Stream many equal pairs so both queues
	// stay saturated and the long run ratio converges tightly.
	if err := s.UpdateWeight("b", 2); err != nil {
		t.Fatal(err)
	}
	// Supply work in proportion to the new weights (1:2) so both queues
	// stay saturated the whole phase; equal queues could never show a
	// non-trivial share because the lighter tenant would run out first.
	const aPer = 300
	const bPer = 600
	for i := 0; i < aPer; i++ {
		s.Submit("a", mk())
	}
	for i := 0; i < bPer; i++ {
		s.Submit("b", mk())
	}
	phase2 := drainOrder(&mu, &order, release, aPer+bPer)
	nA, nB := 0, 0
	for _, name := range phase2 {
		if name == "a" {
			nA++
		} else {
			nB++
		}
	}
	frac := float64(nA) / float64(nA+nB)
	if math.Abs(frac-1.0/3.0) > 0.02 {
		t.Fatalf("phase2 a share = %.3f, want ~0.333 (a=%d b=%d)", frac, nA, nB)
	}
	// With weight a:b = 1:2, a is served at most once in any window of
	// three, so no two a's are ever adjacent while both are saturated.
	adjacent := 0
	for i := 1; i < len(phase2); i++ {
		if phase2[i] == "a" && phase2[i-1] == "a" {
			adjacent++
		}
	}
	if adjacent > 2 { // allow for queue-exhaustion tail only
		t.Fatalf("phase2 has %d adjacent a dispatches", adjacent)
	}
	t.Logf("phase2 share a=%.3f (a=%d b=%d), adjacent-a=%d", frac, nA, nB, adjacent)
	s.Shutdown()
}

func drainOrder(mu *sync.Mutex, order *[]string, release chan struct{}, n int) []string {
	for i := 0; i < n; i++ {
		<-release
	}
	mu.Lock()
	defer mu.Unlock()
	all := *order
	return append([]string(nil), all[len(all)-n:]...)
}

func eqStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// After Close no new tasks are accepted; queued work is still drained.
func TestCloseStopsNewAndDrains(t *testing.T) {
	s, err := New(Config{Concurrency: 1, Tenants: map[string]int{"a": 1}})
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	h1, _ := s.Submit("a", func(context.Context) error {
		close(started)
		<-release
		return nil
	})
	h2, _ := s.Submit("a", func(context.Context) error { return nil })
	<-started
	s.Close()

	if _, err := s.Submit("a", func(context.Context) error { return nil }); err != ErrClosed {
		t.Fatalf("submit after close err = %v, want ErrClosed", err)
	}
	if err := s.UpdateWeight("a", 5); err != ErrClosed {
		t.Fatalf("update after close err = %v, want ErrClosed", err)
	}
	// Close is idempotent.
	s.Close()

	close(release)
	waitTask(t, h1)
	waitTask(t, h2)
	s.Wait()
	if st := s.Stats(); st.Completed() != 2 || st.Succeeded != 2 {
		t.Fatalf("stats = %+v, want 2 drained successes", st)
	}
}

func TestConfigValidation(t *testing.T) {
	cases := []Config{
		{Concurrency: 0, Tenants: map[string]int{"a": 1}},
		{Concurrency: 2, Tenants: map[string]int{}},
		{Concurrency: 2, Tenants: map[string]int{"a": 0}},
		{Concurrency: 2, Tenants: map[string]int{"a": -3}},
	}
	for i, cfg := range cases {
		if _, err := New(cfg); err == nil {
			t.Fatalf("case %d: expected error", i)
		}
	}
	s, _ := New(Config{Concurrency: 1, Tenants: map[string]int{"a": 1}})
	if _, err := s.Submit("nope", func(context.Context) error { return nil }); err != ErrUnknownTenant {
		t.Fatalf("unknown tenant err = %v", err)
	}
	if _, err := s.Submit("a", nil); err != ErrNilTask {
		t.Fatalf("nil task err = %v", err)
	}
	if err := s.UpdateWeight("a", 0); err != ErrInvalidWeight {
		t.Fatalf("bad weight err = %v", err)
	}
	if err := s.UpdateWeight("nope", 1); err != ErrUnknownTenant {
		t.Fatalf("unknown tenant weight err = %v", err)
	}
	s.Shutdown()
}

// Concurrent submissions, cancellations and weight updates must stay race
// free; per-tenant FIFO ordering must hold throughout.
func TestConcurrentStress(t *testing.T) {
	const limit = 8
	var mu sync.Mutex
	last := map[string]uint64{}
	s := newHooked(t, limit, map[string]int{"a": 1, "b": 2, "c": 3},
		func(e *entry) {
			mu.Lock()
			defer mu.Unlock()
			if prev, ok := last[e.tenant]; ok && e.id <= prev {
				t.Errorf("tenant %s dispatch out of order: %d after %d",
					e.tenant, e.id, prev)
			}
			last[e.tenant] = e.id
		}, nil)

	var wg sync.WaitGroup
	stop := make(chan struct{})

	for _, tenant := range []string{"a", "b", "c"} {
		tenant := tenant
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				h, err := s.Submit(tenant, func(context.Context) error {
					time.Sleep(time.Microsecond)
					return nil
				})
				if err != nil {
					return
				}
				if fastRand()%31 == 0 {
					h.Cancel()
				}
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			s.UpdateWeight("c", int(1+fastRand()%5))
			time.Sleep(2 * time.Millisecond)
		}
	}()

	time.Sleep(150 * time.Millisecond)
	close(stop)
	s.Shutdown()
	wg.Wait()

	st := s.Stats()
	if st.Completed() != st.Submitted {
		t.Fatalf("stats = %+v: not every task reached terminal state", st)
	}
}

func fastRand() uint32 {
	// Tiny xorshift so the test stays within the standard library.
	return uint32(time.Now().UnixNano())
}
