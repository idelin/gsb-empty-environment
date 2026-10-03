package scheduler

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

// fakeClock is a manually advanced clock for quota-window tests.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock { return &fakeClock{t: time.Unix(1700000000, 0)} }

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func mustAdd(t *testing.T, s *Scheduler, id string, weight int64, q Quota) {
	t.Helper()
	if err := s.AddTenant(id, weight, q); err != nil {
		t.Fatalf("AddTenant(%s): %v", id, err)
	}
}

func mustSubmit(t *testing.T, s *Scheduler, tenant string, cost int64) {
	t.Helper()
	if err := s.Submit(tenant, Request{Cost: cost}); err != nil {
		t.Fatalf("Submit(%s): %v", tenant, err)
	}
}

func TestDRRExactSequence(t *testing.T) {
	s := New(Config{Quantum: 1})
	mustAdd(t, s, "A", 2, Quota{})
	mustAdd(t, s, "B", 1, Quota{})
	for i := 0; i < 6; i++ {
		mustSubmit(t, s, "A", 1)
		mustSubmit(t, s, "B", 1)
	}
	var order []string
	for i := 0; i < 6; i++ {
		r, ok := s.Next()
		if !ok {
			t.Fatalf("dispatch %d: queue empty", i)
		}
		order = append(order, r.Tenant)
	}
	want := []string{"A", "A", "B", "A", "A", "B"}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("dispatch order = %v, want %v", order, want)
		}
	}
}

func TestDRRDeficitCarryover(t *testing.T) {
	// A single oversized request (cost 3, quantum 1) must wait three rounds
	// while smaller requests from the peer are served, then dispatch.
	s := New(Config{Quantum: 1})
	mustAdd(t, s, "big", 1, Quota{})
	mustAdd(t, s, "small", 1, Quota{})
	mustSubmit(t, s, "big", 3)
	mustSubmit(t, s, "small", 1)
	mustSubmit(t, s, "small", 1)
	mustSubmit(t, s, "small", 1)

	var order []string
	for i := 0; i < 4; i++ {
		r, ok := s.Next()
		if !ok {
			t.Fatalf("dispatch %d: queue empty", i)
		}
		order = append(order, r.Tenant)
	}
	// Round 1: big.deficit 1 < 3, small served. Round 2: big.deficit 2 < 3,
	// small served. Round 3: big.deficit 3 >= 3, big served; then small.
	want := []string{"small", "small", "big", "small"}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("order = %v, want %v", order, want)
		}
	}
	if _, ok := s.Next(); ok {
		t.Fatal("expected empty scheduler")
	}
}

func TestDRRWeightedFairnessRatio(t *testing.T) {
	const n = 3000
	s := New(Config{Quantum: 4, MaxQueue: 2 * n})
	mustAdd(t, s, "heavy", 3, Quota{})
	mustAdd(t, s, "light", 1, Quota{})
	for i := 0; i < n; i++ {
		mustSubmit(t, s, "heavy", 1)
		mustSubmit(t, s, "light", 1)
	}
	counts := map[string]int{}
	for _, r := range s.Drain() {
		counts[r.Tenant]++
	}
	if counts["heavy"] != n || counts["light"] != n {
		t.Fatalf("counts = %v, want %d each", counts, n)
	}
	// Long-run share: heavy must get exactly 3x the quantum credit. Verify
	// via a fresh scheduler that after k full rounds the dispatch counts
	// match the weight ratio exactly.
	s2 := New(Config{Quantum: 1})
	mustAdd(t, s2, "heavy", 3, Quota{})
	mustAdd(t, s2, "light", 1, Quota{})
	for i := 0; i < 400; i++ {
		mustSubmit(t, s2, "heavy", 1)
		mustSubmit(t, s2, "light", 1)
	}
	got := map[string]int{}
	for i := 0; i < 400; i++ { // 100 full rounds of 4 dispatches
		r, _ := s2.Next()
		got[r.Tenant]++
	}
	if got["heavy"] != 300 || got["light"] != 100 {
		t.Fatalf("fairness ratio = %v, want 300:100", got)
	}
}

func TestQuotaEnforcementAndWindowReset(t *testing.T) {
	clock := newFakeClock()
	s := New(Config{Now: clock.now})
	mustAdd(t, s, "t1", 1, Quota{Limit: 5, Window: time.Minute})

	mustSubmit(t, s, "t1", 2)
	mustSubmit(t, s, "t1", 3) // 2 + 3 = 5, exactly at the limit
	if err := s.Submit("t1", Request{Cost: 1}); err != ErrQuotaExceeded {
		t.Fatalf("over-quota submit = %v, want ErrQuotaExceeded", err)
	}
	st, _ := s.Stats("t1")
	if st.QuotaUsed != 5 {
		t.Fatalf("QuotaUsed = %d, want 5", st.QuotaUsed)
	}
	// Still inside the window: rejected.
	clock.advance(30 * time.Second)
	if err := s.Submit("t1", Request{Cost: 1}); err != ErrQuotaExceeded {
		t.Fatalf("in-window submit = %v, want ErrQuotaExceeded", err)
	}
	// Window rolls over: quota restored.
	clock.advance(31 * time.Second)
	mustSubmit(t, s, "t1", 1)
	st, _ = s.Stats("t1")
	if st.QuotaUsed != 1 {
		t.Fatalf("post-reset QuotaUsed = %d, want 1", st.QuotaUsed)
	}
}

func TestTenantIsolation(t *testing.T) {
	s := New(Config{MaxQueue: 2})
	mustAdd(t, s, "A", 1, Quota{Limit: 1, Window: time.Hour})
	mustAdd(t, s, "B", 1, Quota{})

	// Exhaust A's quota; B must be unaffected.
	mustSubmit(t, s, "A", 1)
	if err := s.Submit("A", Request{Cost: 1}); err != ErrQuotaExceeded {
		t.Fatalf("A over-quota = %v", err)
	}
	for i := 0; i < 2; i++ {
		mustSubmit(t, s, "B", 1)
	}
	// Fill B's bounded queue; A's state must be unaffected.
	if err := s.Submit("B", Request{Cost: 1}); err != ErrQueueFull {
		t.Fatalf("B queue overflow = %v, want ErrQueueFull", err)
	}
	stA, _ := s.Stats("A")
	if stA.QueueLen != 1 || stA.Rejected != 1 {
		t.Fatalf("A stats = %+v", stA)
	}
	// Remove A entirely: B keeps working, A's requests are dropped.
	s.RemoveTenant("A")
	if s.Tenants() != 1 {
		t.Fatalf("Tenants = %d, want 1", s.Tenants())
	}
	if _, ok := s.Stats("A"); ok {
		t.Fatal("removed tenant still has stats")
	}
	if err := s.Submit("A", Request{Cost: 1}); err != ErrUnknownTenant {
		t.Fatalf("submit to removed tenant = %v, want ErrUnknownTenant", err)
	}
	r, ok := s.Next()
	if !ok || r.Tenant != "B" {
		t.Fatalf("dispatch after removal = %v, %v", r, ok)
	}
	s.RemoveTenant("does-not-exist") // must be a no-op, not a panic
}

func TestSchedulerValidation(t *testing.T) {
	s := New(Config{})
	if err := s.AddTenant("x", 0, Quota{}); err == nil {
		t.Error("zero weight accepted")
	}
	if err := s.AddTenant("x", -3, Quota{}); err == nil {
		t.Error("negative weight accepted")
	}
	if err := s.AddTenant("x", 1, Quota{Limit: 10}); err == nil {
		t.Error("quota limit without window accepted")
	}
	mustAdd(t, s, "x", 1, Quota{})
	if err := s.AddTenant("x", 1, Quota{}); err != ErrTenantExists {
		t.Errorf("duplicate add = %v, want ErrTenantExists", err)
	}
	if err := s.Submit("ghost", Request{}); err != ErrUnknownTenant {
		t.Errorf("unknown submit = %v, want ErrUnknownTenant", err)
	}
	if _, ok := New(Config{}).Next(); ok {
		t.Error("empty scheduler dispatched")
	}
}

func TestCostNormalizationAndDeficitReset(t *testing.T) {
	s := New(Config{Quantum: 1})
	mustAdd(t, s, "t", 1, Quota{})
	// Cost 0 and negative costs normalize to 1.
	mustSubmit(t, s, "t", 0)
	mustSubmit(t, s, "t", -5)
	if _, ok := s.Next(); !ok {
		t.Fatal("first dispatch failed")
	}
	if _, ok := s.Next(); !ok {
		t.Fatal("second dispatch failed")
	}
	st, _ := s.Stats("t")
	if st.Deficit != 0 {
		t.Fatalf("idle tenant deficit = %d, want 0 (no hoarding)", st.Deficit)
	}
	if st.QueueLen != 0 || s.Pending() != 0 {
		t.Fatalf("queue not drained: %+v pending=%d", st, s.Pending())
	}
}

// TestConcurrentIsolationAndNoLeak hammers the scheduler from many
// goroutines and asserts exact accounting: every accepted request is
// eventually dispatched exactly once, queues drain to zero, and no tenant
// state leaks. Run with -race.
func TestConcurrentIsolationAndNoLeak(t *testing.T) {
	const tenants = 8
	const perTenant = 500
	s := New(Config{Quantum: 2, MaxQueue: 64})
	for i := 0; i < tenants; i++ {
		mustAdd(t, s, fmt.Sprintf("t%d", i), int64(i%3+1), Quota{})
	}

	var wg sync.WaitGroup
	// Producers: each tenant is fed by its own goroutine; a full queue
	// triggers a retry after a drain cycle, never a panic or cross-tenant
	// interference.
	accepted := make([][]Request, tenants)
	var accMu sync.Mutex
	for i := 0; i < tenants; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := fmt.Sprintf("t%d", i)
			for n := 0; n < perTenant; {
				r := Request{Payload: n, Cost: 1}
				err := s.Submit(id, r)
				if err == ErrQueueFull {
					time.Sleep(time.Microsecond)
					continue
				}
				if err != nil {
					t.Errorf("submit %s: %v", id, err)
					return
				}
				accMu.Lock()
				accepted[i] = append(accepted[i], r)
				accMu.Unlock()
				n++
			}
		}(i)
	}
	// Consumer: drains concurrently until all producers are done and the
	// scheduler is empty.
	dispatched := make(map[string]int)
	var dispMu sync.Mutex
	producersDone := make(chan struct{})
	go func() {
		wg.Wait()
		close(producersDone)
	}()
	consumerDone := make(chan struct{})
	go func() {
		defer close(consumerDone)
		for {
			r, ok := s.Next()
			if !ok {
				select {
				case <-producersDone:
					if s.Pending() == 0 {
						return
					}
				default:
				}
				time.Sleep(time.Microsecond)
				continue
			}
			dispMu.Lock()
			dispatched[r.Tenant]++
			dispMu.Unlock()
		}
	}()

	<-consumerDone
	// Final drain to be certain nothing lingers.
	rest := s.Drain()
	dispMu.Lock()
	for _, r := range rest {
		dispatched[r.Tenant]++
	}
	dispMu.Unlock()

	if p := s.Pending(); p != 0 {
		t.Fatalf("pending = %d after drain, want 0 (leak)", p)
	}
	var totalAccepted, totalDispatched int
	for i := 0; i < tenants; i++ {
		id := fmt.Sprintf("t%d", i)
		totalAccepted += len(accepted[i])
		totalDispatched += dispatched[id]
		st, ok := s.Stats(id)
		if !ok {
			t.Fatalf("tenant %s vanished", id)
		}
		if st.QueueLen != 0 {
			t.Fatalf("tenant %s queue len = %d (leak)", id, st.QueueLen)
		}
		if st.Enqueued != st.Dequeued {
			t.Fatalf("tenant %s enqueued=%d dequeued=%d (leak)", id, st.Enqueued, st.Dequeued)
		}
		if int(st.Dequeued) != dispatched[id] {
			t.Fatalf("tenant %s counter %d != observed %d", id, st.Dequeued, dispatched[id])
		}
	}
	if totalAccepted != tenants*perTenant {
		t.Fatalf("accepted = %d, want %d", totalAccepted, tenants*perTenant)
	}
	if totalDispatched != totalAccepted {
		t.Fatalf("dispatched = %d, accepted = %d (leak)", totalDispatched, totalAccepted)
	}
}

// TestConcurrentQuotaStress verifies quota accounting stays exact under
// concurrent submits: accepted cost never exceeds the window limit.
func TestConcurrentQuotaStress(t *testing.T) {
	clock := newFakeClock()
	s := New(Config{Now: clock.now})
	const limit = 1000
	mustAdd(t, s, "q", 1, Quota{Limit: limit, Window: time.Hour})

	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				_ = s.Submit("q", Request{Cost: 1})
			}
		}()
	}
	wg.Wait()
	st, _ := s.Stats("q")
	if st.Enqueued != limit {
		t.Fatalf("enqueued = %d, want exactly %d under concurrency", st.Enqueued, limit)
	}
	if st.Enqueued+st.Rejected != 16*200 {
		t.Fatalf("enqueued+rejected = %d, want %d", st.Enqueued+st.Rejected, 16*200)
	}
}
