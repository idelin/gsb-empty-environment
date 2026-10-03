package scheduler

import (
	"runtime"
	"sync"
	"testing"
	"time"
)

func mustAdd(t *testing.T, s *Scheduler, name string, weight int64, quota, maxQueue int) {
	t.Helper()
	if err := s.AddTenant(name, weight, quota, maxQueue); err != nil {
		t.Fatalf("AddTenant(%s): %v", name, err)
	}
}

func drain(s *Scheduler) map[string]int {
	counts := make(map[string]int)
	for {
		name, _, ok := s.Dispatch()
		if !ok {
			return counts
		}
		counts[name]++
		if err := s.Done(name); err != nil {
			panic(err)
		}
	}
}

// dispatchWindow pulls n jobs and counts them per tenant.
func dispatchWindow(s *Scheduler, n int) map[string]int {
	counts := make(map[string]int)
	for i := 0; i < n; i++ {
		name, _, ok := s.Dispatch()
		if !ok {
			break
		}
		counts[name]++
		if err := s.Done(name); err != nil {
			panic(err)
		}
	}
	return counts
}

// TestDRRWeightedFairness: while both tenants are backlogged, throughput
// converges to the weight ratio (2:1).
func TestDRRWeightedFairness(t *testing.T) {
	s := New(1)
	mustAdd(t, s, "heavy", 2, 100000, 100000)
	mustAdd(t, s, "light", 1, 100000, 100000)
	const jobs = 100000 // deep backlog: neither queue empties in the window
	for i := 0; i < jobs; i++ {
		if err := s.Submit("heavy", Job{ID: int64(i)}); err != nil {
			t.Fatal(err)
		}
		if err := s.Submit("light", Job{ID: int64(i)}); err != nil {
			t.Fatal(err)
		}
	}
	counts := dispatchWindow(s, 30000)
	got := float64(counts["heavy"]) / float64(counts["light"])
	if got < 1.95 || got > 2.05 {
		t.Fatalf("fairness ratio heavy/light = %.3f, want ~2.0 (counts %v)", got, counts)
	}
	if counts["heavy"]+counts["light"] != 30000 {
		t.Fatalf("lost jobs in window: %v", counts)
	}
}

// TestDRRThreeWayWeights: 3:2:1 weights converge to the ratio.
func TestDRRThreeWayWeights(t *testing.T) {
	s := New(1)
	mustAdd(t, s, "a", 3, 100000, 100000)
	mustAdd(t, s, "b", 2, 100000, 100000)
	mustAdd(t, s, "c", 1, 100000, 100000)
	const jobs = 100000
	for i := 0; i < jobs; i++ {
		for _, name := range []string{"a", "b", "c"} {
			if err := s.Submit(name, Job{}); err != nil {
				t.Fatal(err)
			}
		}
	}
	counts := dispatchWindow(s, 60000)
	total := counts["a"] + counts["b"] + counts["c"]
	share := func(name string) float64 { return float64(counts[name]) / float64(total) }
	if s := share("a"); s < 0.48 || s > 0.52 {
		t.Fatalf("a share %.3f, want ~0.5 (%v)", s, counts)
	}
	if s := share("b"); s < 0.31 || s > 0.36 {
		t.Fatalf("b share %.3f, want ~0.333 (%v)", s, counts)
	}
	if s := share("c"); s < 0.14 || s > 0.19 {
		t.Fatalf("c share %.3f, want ~0.167 (%v)", s, counts)
	}
}

// TestQuotaIsolation: a tenant may never exceed its in-flight quota, and a
// tenant that exhausted its quota must not block others.
func TestQuotaIsolation(t *testing.T) {
	s := New(1)
	mustAdd(t, s, "capped", 100, 2, 1000) // huge weight, tiny quota
	mustAdd(t, s, "roomy", 1, 1000, 1000)
	for i := 0; i < 20; i++ {
		if err := s.Submit("capped", Job{}); err != nil {
			t.Fatal(err)
		}
		if err := s.Submit("roomy", Job{}); err != nil {
			t.Fatal(err)
		}
	}
	// Despite its weight, "capped" stalls at 2 in-flight; "roomy" drains.
	dispatched := 0
	for {
		name, _, ok := s.Dispatch()
		if !ok {
			break
		}
		dispatched++
		_, inflight, err := s.TenantStats("capped")
		if err != nil {
			t.Fatal(err)
		}
		if inflight > 2 {
			t.Fatalf("quota violated: capped inflight=%d", inflight)
		}
		if name == "roomy" {
			if err := s.Done("roomy"); err != nil {
				t.Fatal(err)
			}
		}
	}
	// capped: 2 in-flight (never Done), roomy: all 20 done.
	st := s.Stats()
	if st.InFlight != 2 || st.Queued != 18 {
		t.Fatalf("stats %+v, want InFlight=2 Queued=18", st)
	}
	// Completing capped jobs releases quota for exactly the freed slots.
	if err := s.Done("capped"); err != nil {
		t.Fatal(err)
	}
	name, _, ok := s.Dispatch()
	if !ok || name != "capped" {
		t.Fatalf("after Done, dispatch = %s,%v; want capped,true", name, ok)
	}
	if _, inflight, _ := s.TenantStats("capped"); inflight != 2 {
		t.Fatalf("capped inflight=%d, want 2", inflight)
	}
}

// TestQueueFullBackpressure: bounded queues reject overflow with
// ErrQueueFull, isolating a flooding tenant.
func TestQueueFullBackpressure(t *testing.T) {
	s := New(1)
	mustAdd(t, s, "noisy", 1, 10, 2)
	mustAdd(t, s, "calm", 1, 10, 2)
	for i := 0; i < 2; i++ {
		if err := s.Submit("noisy", Job{}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Submit("noisy", Job{}); err != ErrQueueFull {
		t.Fatalf("got %v, want ErrQueueFull", err)
	}
	// The other tenant is unaffected.
	if err := s.Submit("calm", Job{}); err != nil {
		t.Fatalf("calm tenant impacted by noisy neighbor: %v", err)
	}
}

// TestDeficitAccumulation: a job more expensive than one round of credit is
// still dispatched once enough deficit has been banked, and a job more
// expensive than the burst cap is afforded by raising the cap.
func TestDeficitAccumulation(t *testing.T) {
	s := New(1)
	mustAdd(t, s, "t", 1, 10, 10)
	if err := s.Submit("t", Job{Cost: 3}); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := s.Dispatch(); !ok {
		t.Fatal("cost 3 with quantum 1 must dispatch after banking 3 rounds")
	}
	if err := s.Submit("t", Job{Cost: 100}); err != nil { // exceeds burst cap 8
		t.Fatal(err)
	}
	if _, _, ok := s.Dispatch(); !ok {
		t.Fatal("job beyond burst cap must still eventually dispatch")
	}
}

// TestCostAccounting: with equal weights, a tenant whose jobs cost twice as
// much receives half the job throughput (equal cost-unit throughput).
func TestCostAccounting(t *testing.T) {
	s := New(1)
	mustAdd(t, s, "expensive", 1, 100000, 100000)
	mustAdd(t, s, "cheap", 1, 100000, 100000)
	for i := 0; i < 100000; i++ {
		if err := s.Submit("expensive", Job{Cost: 2}); err != nil {
			t.Fatal(err)
		}
		if err := s.Submit("cheap", Job{Cost: 1}); err != nil {
			t.Fatal(err)
		}
	}
	counts := dispatchWindow(s, 30000)
	ratio := float64(counts["cheap"]) / float64(counts["expensive"])
	if ratio < 1.9 || ratio > 2.1 {
		t.Fatalf("cost accounting ratio %.3f, want ~2.0 (%v)", ratio, counts)
	}
}

// TestZeroCostTreatedAsOne: non-positive costs are normalized to 1.
func TestZeroCostTreatedAsOne(t *testing.T) {
	s := New(2)
	mustAdd(t, s, "t", 1, 10, 10)
	if err := s.Submit("t", Job{Cost: 0}); err != nil {
		t.Fatal(err)
	}
	if err := s.Submit("t", Job{Cost: -5}); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := s.Dispatch(); !ok {
		t.Fatal("first job should dispatch (deficit 2 >= 1)")
	}
	if _, _, ok := s.Dispatch(); !ok {
		t.Fatal("second job should dispatch (deficit 1 >= 1)")
	}
}

// TestErrorPaths: unknown tenants, invalid configs, Done underflow,
// duplicate registration, and busy removal.
func TestErrorPaths(t *testing.T) {
	s := New(1)
	if err := s.Submit("ghost", Job{}); err != ErrUnknownTenant {
		t.Fatalf("submit: got %v, want ErrUnknownTenant", err)
	}
	if err := s.Done("ghost"); err != ErrUnknownTenant {
		t.Fatalf("done: got %v, want ErrUnknownTenant", err)
	}
	if _, _, err := s.TenantStats("ghost"); err != ErrUnknownTenant {
		t.Fatalf("stats: got %v, want ErrUnknownTenant", err)
	}
	if err := s.RemoveTenant("ghost"); err != ErrUnknownTenant {
		t.Fatalf("remove: got %v, want ErrUnknownTenant", err)
	}
	for _, cfg := range [][3]int64{{0, 1, 1}, {1, 0, 1}, {1, 1, 0}, {-1, 1, 1}} {
		if err := s.AddTenant("bad", cfg[0], int(cfg[1]), int(cfg[2])); err != ErrInvalidConfig {
			t.Fatalf("cfg %v: got %v, want ErrInvalidConfig", cfg, err)
		}
	}
	mustAdd(t, s, "t", 1, 1, 1)
	if err := s.AddTenant("t", 1, 1, 1); err != ErrInvalidConfig {
		t.Fatalf("duplicate: got %v, want ErrInvalidConfig", err)
	}
	if err := s.Done("t"); err != ErrNotInFlight {
		t.Fatalf("done underflow: got %v, want ErrNotInFlight", err)
	}
	// Busy tenant cannot be removed (no silent job loss).
	if err := s.Submit("t", Job{}); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveTenant("t"); err == nil {
		t.Fatal("remove with queued job must fail")
	}
	name, _, ok := s.Dispatch()
	if !ok || name != "t" {
		t.Fatal("dispatch failed")
	}
	if err := s.RemoveTenant("t"); err == nil {
		t.Fatal("remove with in-flight job must fail")
	}
	if err := s.Done("t"); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveTenant("t"); err != nil {
		t.Fatalf("remove after drain: %v", err)
	}
	if err := s.Submit("t", Job{}); err != ErrUnknownTenant {
		t.Fatalf("submit after remove: got %v, want ErrUnknownTenant", err)
	}
}

// TestEmptyScheduler: dispatch on an empty scheduler / idle tenants.
func TestEmptyScheduler(t *testing.T) {
	s := New(1)
	if _, _, ok := s.Dispatch(); ok {
		t.Fatal("empty scheduler must not dispatch")
	}
	mustAdd(t, s, "idle", 1, 1, 1)
	if _, _, ok := s.Dispatch(); ok {
		t.Fatal("idle tenants must not dispatch")
	}
}

// TestConcurrentNoLeak hammers the scheduler from many goroutines and
// asserts strict conservation: every submitted job is dispatched exactly
// once, every dispatched job is completed, nothing is left queued or
// in-flight, and no goroutines leak.
func TestConcurrentNoLeak(t *testing.T) {
	baseGoroutines := runtime.NumGoroutine()

	s := New(4)
	tenants := []string{"alpha", "beta", "gamma", "delta"}
	for i, name := range tenants {
		mustAdd(t, s, name, int64(i+1), 64, 100000)
	}

	const producersPerTenant = 4
	const jobsPerProducer = 500
	total := int64(len(tenants) * producersPerTenant * jobsPerProducer)

	var producers sync.WaitGroup
	for _, name := range tenants {
		for p := 0; p < producersPerTenant; p++ {
			producers.Add(1)
			go func(name string, p int) {
				defer producers.Done()
				for i := 0; i < jobsPerProducer; i++ {
					for {
						err := s.Submit(name, Job{Cost: int64(i%3 + 1)})
						if err == nil {
							break
						}
						if err != ErrQueueFull {
							t.Errorf("submit: %v", err)
							break
						}
						runtime.Gosched() // backpressure retry
					}
				}
			}(name, p)
		}
	}

	var consumers sync.WaitGroup
	stop := make(chan struct{})
	for c := 0; c < 4; c++ {
		consumers.Add(1)
		go func() {
			defer consumers.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				name, _, ok := s.Dispatch()
				if !ok {
					runtime.Gosched()
					continue
				}
				if err := s.Done(name); err != nil {
					t.Errorf("done: %v", err)
				}
			}
		}()
	}

	producers.Wait()
	// Drain until every submitted job has been completed.
	for s.Stats().Completed < total {
		runtime.Gosched()
	}
	close(stop)
	consumers.Wait()

	st := s.Stats()
	if st.Dispatched != total || st.Completed != total {
		t.Fatalf("conservation violated: %+v, want %d/%d", st, total, total)
	}
	if st.InFlight != 0 || st.Queued != 0 {
		t.Fatalf("leak: %+v, want zero in-flight and queued", st)
	}
	for _, name := range tenants {
		queued, inflight, err := s.TenantStats(name)
		if err != nil {
			t.Fatal(err)
		}
		if queued != 0 || inflight != 0 {
			t.Fatalf("tenant %s leaked: queued=%d inflight=%d", name, queued, inflight)
		}
	}

	// Strict goroutine-leak assertion.
	deadline := time.Now().Add(2 * time.Second)
	for runtime.NumGoroutine() > baseGoroutines && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if n := runtime.NumGoroutine(); n > baseGoroutines {
		t.Fatalf("goroutine leak: baseline %d, now %d", baseGoroutines, n)
	}
}

// TestConcurrentFairnessUnderContention: weighted fairness still holds when
// dispatches race on multiple consumer goroutines.
func TestConcurrentFairnessUnderContention(t *testing.T) {
	s := New(1)
	mustAdd(t, s, "heavy", 2, 100000, 1000000)
	mustAdd(t, s, "light", 1, 100000, 1000000)
	const jobs = 100000 // deep backlog: neither queue empties in the window
	for _, name := range []string{"heavy", "light"} {
		for i := 0; i < jobs; i++ {
			if err := s.Submit(name, Job{}); err != nil {
				t.Fatal(err)
			}
		}
	}
	const window = 40000
	var wg sync.WaitGroup
	counts := make(map[string]int)
	var mu sync.Mutex
	var dispatched int64
	var dmu sync.Mutex
	for c := 0; c < 4; c++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				dmu.Lock()
				if dispatched >= window {
					dmu.Unlock()
					return
				}
				dispatched++
				dmu.Unlock()
				name, _, ok := s.Dispatch()
				if !ok {
					t.Errorf("dispatch starved inside window")
					return
				}
				mu.Lock()
				counts[name]++
				mu.Unlock()
				if err := s.Done(name); err != nil {
					t.Errorf("done: %v", err)
				}
			}
		}()
	}
	wg.Wait()
	ratio := float64(counts["heavy"]) / float64(counts["light"])
	if ratio < 1.9 || ratio > 2.1 {
		t.Fatalf("concurrent fairness ratio %.3f, want ~2.0 (%v)", ratio, counts)
	}
}
