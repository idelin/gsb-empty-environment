package scheduler

import (
	"fmt"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func mustAdd(t *testing.T, s *Scheduler, id string, weight uint64, maxQ, maxF int) {
	t.Helper()
	if err := s.AddTenant(id, weight, maxQ, maxF); err != nil {
		t.Fatalf("AddTenant(%s): %v", id, err)
	}
}

func drain(t *testing.T, s *Scheduler) map[string]uint64 {
	t.Helper()
	served := map[string]uint64{}
	for {
		job, err := s.Next()
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		if job == nil {
			return served
		}
		id := job.Payload.(string)
		served[id] += job.Cost
		if err := s.Complete(id, job); err != nil {
			t.Fatalf("Complete: %v", err)
		}
	}
}

func TestTenantLifecycle(t *testing.T) {
	s := New(100)
	if err := s.AddTenant("", 1, 1, 1); err != ErrInvalidConfig {
		t.Fatalf("empty id: %v", err)
	}
	if err := s.AddTenant("a", 0, 1, 1); err != ErrInvalidConfig {
		t.Fatalf("zero weight: %v", err)
	}
	mustAdd(t, s, "a", 1, 2, 1)
	if err := s.AddTenant("a", 1, 1, 1); err != ErrTenantExists {
		t.Fatalf("duplicate: %v", err)
	}
	if err := s.Submit("ghost", &Job{ID: "x", Cost: 1}); err != ErrUnknownTenant {
		t.Fatalf("unknown tenant submit: %v", err)
	}
	if _, err := s.Stats("ghost"); err != ErrUnknownTenant {
		t.Fatalf("unknown tenant stats: %v", err)
	}
	if err := s.RemoveTenant("ghost"); err != ErrUnknownTenant {
		t.Fatalf("unknown tenant remove: %v", err)
	}
	mustAdd(t, s, "b", 1, 1, 1)
	if err := s.RemoveTenant("b"); err != nil {
		t.Fatalf("remove idle tenant: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close with idle tenant: %v", err)
	}
	if err := s.Submit("a", &Job{ID: "x", Cost: 1}); err != ErrClosed {
		t.Fatalf("submit after close: %v", err)
	}
	if _, err := s.Next(); err != ErrClosed {
		t.Fatalf("next after close: %v", err)
	}
}

func TestQueueQuotaIsolation(t *testing.T) {
	s := New(10)
	mustAdd(t, s, "flood", 1, 2, 10) // tiny queue
	mustAdd(t, s, "quiet", 1, 2, 10)

	for i := 0; i < 2; i++ {
		if err := s.Submit("flood", &Job{ID: fmt.Sprint(i), Cost: 1, Payload: "flood"}); err != nil {
			t.Fatalf("submit %d: %v", i, err)
		}
	}
	if err := s.Submit("flood", &Job{ID: "x", Cost: 1, Payload: "flood"}); err != ErrQueueFull {
		t.Fatalf("third submit should hit quota, got %v", err)
	}
	// Isolation: the full tenant must not affect the other tenant.
	if err := s.Submit("quiet", &Job{ID: "q", Cost: 1, Payload: "quiet"}); err != nil {
		t.Fatalf("quiet tenant blocked by flooding tenant: %v", err)
	}
	st, _ := s.Stats("flood")
	if st.Rejected != 1 || st.Queued != 2 {
		t.Fatalf("flood stats: %+v", st)
	}
	served := drain(t, s)
	if served["flood"] != 2 || served["quiet"] != 1 {
		t.Fatalf("served: %v", served)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("clean close: %v", err)
	}
}

func TestInflightQuotaThrottles(t *testing.T) {
	s := New(10)
	mustAdd(t, s, "a", 1, 10, 1) // only 1 in flight
	mustAdd(t, s, "b", 1, 10, 1)
	for i := 0; i < 3; i++ {
		mustSubmit(t, s, "a", 1)
		mustSubmit(t, s, "b", 1)
	}
	j1, _ := s.Next()
	j2, _ := s.Next()
	if j1 == nil || j2 == nil {
		t.Fatal("expected two dispatches")
	}
	if j1.Payload.(string) == j2.Payload.(string) {
		t.Fatal("with inflight=1, second dispatch must come from the other tenant")
	}
	if j, _ := s.Next(); j != nil {
		t.Fatal("all inflight quotas exhausted, expected nil")
	}
	if err := s.Complete(j1.Payload.(string), j1); err != nil {
		t.Fatal(err)
	}
	j3, _ := s.Next()
	if j3 == nil || j3.Payload.(string) != j1.Payload.(string) {
		t.Fatal("freed slot should admit the same tenant again")
	}
	// Drain everything for a clean close.
	for _, j := range []*Job{j2, j3} {
		if err := s.Complete(j.Payload.(string), j); err != nil {
			t.Fatal(err)
		}
	}
	drain(t, s)
	if err := s.Close(); err != nil {
		t.Fatalf("clean close: %v", err)
	}
}

func mustSubmit(t *testing.T, s *Scheduler, tenant string, cost uint64) {
	t.Helper()
	if err := s.Submit(tenant, &Job{ID: fmt.Sprintf("%s/%d", tenant, cost), Cost: cost, Payload: tenant}); err != nil {
		t.Fatalf("Submit(%s): %v", tenant, err)
	}
}

// TestDRRWeightedFairness: weights 1:3 must yield ~1:3 served-cost ratio
// when both tenants are continuously backlogged.
func TestDRRWeightedFairness(t *testing.T) {
	s := New(10)
	mustAdd(t, s, "light", 1, 4000, 100)
	mustAdd(t, s, "heavy", 3, 4000, 100)
	const jobs = 1000
	for i := 0; i < jobs; i++ {
		mustSubmit(t, s, "light", 10)
		mustSubmit(t, s, "heavy", 10)
	}
	// Measure while both tenants are continuously backlogged.
	served := map[string]uint64{}
	for i := 0; i < 400; i++ {
		j, err := s.Next()
		if err != nil || j == nil {
			t.Fatalf("dispatch %d: job=%v err=%v", i, j, err)
		}
		id := j.Payload.(string)
		served[id] += j.Cost
		if err := s.Complete(id, j); err != nil {
			t.Fatal(err)
		}
	}
	ratio := float64(served["heavy"]) / float64(served["light"])
	if ratio < 2.7 || ratio > 3.3 {
		t.Fatalf("unfair: heavy/light = %.2f (want ~3.0), served=%v", ratio, served)
	}
	drain(t, s) // finish the rest so Close sees no leak
	if err := s.Close(); err != nil {
		t.Fatalf("clean close: %v", err)
	}
}

// TestDRRLargeJobDeficitCarryover: a job costing more than one round's
// credit must wait until the deficit accumulates, then be served.
func TestDRRLargeJobDeficitCarryover(t *testing.T) {
	s := New(10)
	mustAdd(t, s, "big", 1, 10, 10)
	mustAdd(t, s, "small", 1, 10, 10)
	mustSubmit(t, s, "big", 25) // needs 3 rounds of credit (10+10+10)
	mustSubmit(t, s, "small", 5)

	var order []string
	for attempts := 0; attempts < 10 && len(order) < 2; attempts++ {
		j, err := s.Next()
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		if j == nil {
			continue // deficit still accumulating
		}
		order = append(order, j.Payload.(string))
		if err := s.Complete(j.Payload.(string), j); err != nil {
			t.Fatal(err)
		}
	}
	if len(order) != 2 {
		t.Fatalf("expected 2 dispatches, got %v", order)
	}
	if order[0] != "small" || order[1] != "big" {
		t.Fatalf("small job should jump ahead of oversized job, got %v", order)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("clean close: %v", err)
	}
}

func TestRemoveBusyTenantRefused(t *testing.T) {
	s := New(10)
	mustAdd(t, s, "a", 1, 10, 10)
	mustSubmit(t, s, "a", 1)
	if err := s.RemoveTenant("a"); err != ErrTenantBusy {
		t.Fatalf("remove with queued job: %v", err)
	}
	j, _ := s.Next()
	if err := s.RemoveTenant("a"); err != ErrTenantBusy {
		t.Fatalf("remove with inflight job: %v", err)
	}
	if err := s.Complete("a", j); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveTenant("a"); err != nil {
		t.Fatalf("remove after drain: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("clean close: %v", err)
	}
}

func TestCloseDetectsLeaks(t *testing.T) {
	s := New(10)
	mustAdd(t, s, "a", 1, 10, 10)
	mustSubmit(t, s, "a", 1)
	if err := s.Close(); err == nil || !strings.Contains(err.Error(), ErrQueueLeak.Error()) {
		t.Fatalf("queued job at close: %v", err)
	}
	j, _ := s.Next()
	if err := s.Close(); err == nil || !strings.Contains(err.Error(), ErrInflightLeak.Error()) {
		t.Fatalf("inflight job at close: %v", err)
	}
	if err := s.Complete("a", j); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("clean close: %v", err)
	}
}

func TestCompleteWithoutDispatch(t *testing.T) {
	s := New(10)
	mustAdd(t, s, "a", 1, 10, 10)
	if err := s.Complete("a", &Job{ID: "x", Cost: 1}); err == nil {
		t.Fatal("complete without dispatch must fail")
	}
	if err := s.Close(); err != nil {
		t.Fatalf("clean close: %v", err)
	}
}

// TestConcurrentIsolationAndFairness hammers the scheduler from many
// goroutines across tenants, then asserts: exact accounting (submitted =
// served + rejected leftovers drained), per-tenant isolation of quotas,
// and zero leaks (all counters back to zero, clean Close).
func TestConcurrentIsolationAndFairness(t *testing.T) {
	s := New(10)
	tenants := []struct {
		id     string
		weight uint64
	}{
		{"t1", 1}, {"t2", 2}, {"t3", 3},
	}
	for _, tn := range tenants {
		mustAdd(t, s, tn.id, tn.weight, 100000, 4)
	}

	const perTenant = 3000
	var submitWG sync.WaitGroup
	for _, tn := range tenants {
		for w := 0; w < 4; w++ {
			submitWG.Add(1)
			go func(id string, w int) {
				defer submitWG.Done()
				for i := 0; i < perTenant/4; i++ {
					job := &Job{ID: fmt.Sprintf("%s/%d/%d", id, w, i), Cost: 10, Payload: id}
					for {
						err := s.Submit(id, job)
						if err == nil {
							break
						}
						if err != ErrQueueFull {
							t.Errorf("unexpected submit error: %v", err)
							return
						}
						runtime.Gosched() // queue momentarily full: retry
					}
				}
			}(tn.id, w)
		}
	}

	// Concurrent dispatchers.
	var dispatchWG sync.WaitGroup
	var mu sync.Mutex
	served := map[string]uint64{}
	count := map[string]uint64{}
	var finishOrder []string
	dispatched := map[string]uint64{}
	for w := 0; w < 3; w++ {
		dispatchWG.Add(1)
		go func() {
			defer dispatchWG.Done()
			for {
				job, err := s.Next()
				if err != nil {
					t.Errorf("Next: %v", err)
					return
				}
				if job == nil {
					mu.Lock()
					done := dispatched["__total__"] == 3*perTenant
					mu.Unlock()
					if done {
						return
					}
					runtime.Gosched()
					continue
				}
				id := job.Payload.(string)
				mu.Lock()
				served[id] += job.Cost
				count[id]++
				if count[id] == perTenant {
					finishOrder = append(finishOrder, id)
				}
				dispatched["__total__"]++
				mu.Unlock()
				if err := s.Complete(id, job); err != nil {
					t.Errorf("Complete: %v", err)
					return
				}
			}
		}()
	}
	submitWG.Wait()
	dispatchWG.Wait()

	// Exact accounting: every accepted job was served exactly once.
	var totalServed uint64
	for _, tn := range tenants {
		st, err := s.Stats(tn.id)
		if err != nil {
			t.Fatal(err)
		}
		if st.Submitted != perTenant {
			t.Fatalf("%s: submitted=%d want %d", tn.id, st.Submitted, perTenant)
		}
		if st.Served != perTenant*10 {
			t.Fatalf("%s: served=%d want %d", tn.id, st.Served, perTenant*10)
		}
		if st.Queued != 0 || st.Inflight != 0 {
			t.Fatalf("%s: leak queued=%d inflight=%d", tn.id, st.Queued, st.Inflight)
		}
		totalServed += served[tn.id]
	}
	if totalServed != 3*perTenant*10 {
		t.Fatalf("total served=%d want %d", totalServed, 3*perTenant*10)
	}
	// Weighted fairness under full load: with weights 1:2:3 and equal
	// demand, the heavier tenant must finish its backlog first.
	if len(finishOrder) != 3 || finishOrder[0] != "t3" || finishOrder[1] != "t2" || finishOrder[2] != "t1" {
		t.Fatalf("unfair finish order: %v (want [t3 t2 t1])", finishOrder)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("strict no-leak close: %v", err)
	}
}

// TestNoGoroutineLeak: the scheduler itself must not spawn or retain
// goroutines; goroutine count returns to baseline after use and Close.
func TestNoGoroutineLeak(t *testing.T) {
	before := runtime.NumGoroutine()
	s := New(10)
	mustAdd(t, s, "a", 1, 100, 100)
	for i := 0; i < 100; i++ {
		mustSubmit(t, s, "a", 1)
	}
	drain(t, s)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	runtime.GC()
	if after := runtime.NumGoroutine(); after > before {
		t.Fatalf("goroutine leak: before=%d after=%d", before, after)
	}
}
