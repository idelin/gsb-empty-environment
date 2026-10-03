package scheduler

import (
	"context"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func noopTask(tenant string) *Task {
	return &Task{TenantID: tenant, Cost: 1, Run: func(context.Context) error { return nil }}
}

func drain(t *testing.T, dones ...<-chan error) {
	t.Helper()
	for _, d := range dones {
		select {
		case err := <-d:
			if err != nil {
				t.Fatalf("task failed: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for task")
		}
	}
}

func TestRegisterDuplicateTenant(t *testing.T) {
	s := New(Config{Workers: 1})
	defer s.Shutdown()
	if err := s.RegisterTenant("a", Quota{MaxQueued: 10}); err != nil {
		t.Fatal(err)
	}
	if err := s.RegisterTenant("a", Quota{MaxQueued: 10}); err != ErrTenantExists {
		t.Fatalf("got %v want ErrTenantExists", err)
	}
	if err := s.RegisterTenant("", Quota{MaxQueued: 10}); err != ErrInvalidTask {
		t.Fatalf("got %v want ErrInvalidTask", err)
	}
}

func TestSubmitUnknownTenant(t *testing.T) {
	s := New(Config{Workers: 1})
	defer s.Shutdown()
	if _, err := s.Submit(noopTask("ghost")); err != ErrNoSuchTenant {
		t.Fatalf("got %v want ErrNoSuchTenant", err)
	}
	if _, err := s.Submit(&Task{Cost: 1}); err != ErrInvalidTask {
		t.Fatalf("got %v want ErrInvalidTask", err)
	}
	if _, err := s.Submit(nil); err != ErrInvalidTask {
		t.Fatalf("got %v want ErrInvalidTask", err)
	}
}

func TestSubmitAfterShutdown(t *testing.T) {
	s := New(Config{Workers: 1})
	s.RegisterTenant("a", Quota{MaxQueued: 10})
	s.Shutdown()
	if _, err := s.Submit(noopTask("a")); err != ErrClosed {
		t.Fatalf("got %v want ErrClosed", err)
	}
	if err := s.RegisterTenant("b", Quota{}); err != ErrClosed {
		t.Fatalf("got %v want ErrClosed", err)
	}
	// Shutdown must be idempotent.
	s.Shutdown()
}

func TestQueueQuotaEnforced(t *testing.T) {
	s := New(Config{Workers: 0}) // no workers: queue never drains
	s.RegisterTenant("a", Quota{MaxQueued: 2})
	d1, err1 := s.Submit(noopTask("a"))
	d2, err2 := s.Submit(noopTask("a"))
	if err1 != nil || err2 != nil {
		t.Fatalf("unexpected errors: %v %v", err1, err2)
	}
	if _, err := s.Submit(noopTask("a")); err != ErrQueueFull {
		t.Fatalf("got %v want ErrQueueFull", err)
	}
	st, _ := s.Stats("a")
	if st.Queued != 2 || st.Rejected != 1 || st.Submitted != 2 {
		t.Fatalf("bad stats: %+v", st)
	}
	s.Shutdown()
	// Undrained tasks must fail with ErrShutDown, not hang.
	for _, d := range []<-chan error{d1, d2} {
		if err := <-d; err != ErrShutDown {
			t.Fatalf("got %v want ErrShutDown", err)
		}
	}
}

func TestZeroQueueQuotaRejectsEverything(t *testing.T) {
	s := New(Config{Workers: 1})
	defer s.Shutdown()
	s.RegisterTenant("a", Quota{MaxQueued: 0})
	if _, err := s.Submit(noopTask("a")); err != ErrQueueFull {
		t.Fatalf("got %v want ErrQueueFull", err)
	}
}

func TestInflightQuotaEnforced(t *testing.T) {
	s := New(Config{Workers: 8, Quantum: 100})
	defer s.Shutdown()
	s.RegisterTenant("a", Quota{MaxQueued: 100, MaxInflight: 2})
	release := make(chan struct{})
	var cur, maxSeen int32
	var dones []<-chan error
	for i := 0; i < 10; i++ {
		d, err := s.Submit(&Task{TenantID: "a", Cost: 1, Run: func(context.Context) error {
			c := atomic.AddInt32(&cur, 1)
			for {
				m := atomic.LoadInt32(&maxSeen)
				if c <= m || atomic.CompareAndSwapInt32(&maxSeen, m, c) {
					break
				}
			}
			<-release
			atomic.AddInt32(&cur, -1)
			return nil
		}})
		if err != nil {
			t.Fatal(err)
		}
		dones = append(dones, d)
	}
	deadline := time.Now().Add(2 * time.Second)
	for atomic.LoadInt32(&cur) < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if atomic.LoadInt32(&cur) != 2 {
		t.Fatalf("expected 2 in-flight tasks, got %d", cur)
	}
	close(release)
	drain(t, dones...)
	if m := atomic.LoadInt32(&maxSeen); m > 2 {
		t.Fatalf("in-flight quota violated: max concurrency %d", m)
	}
	st, _ := s.Stats("a")
	if st.Inflight != 0 || st.Queued != 0 || st.Completed != 10 {
		t.Fatalf("bad final stats: %+v", st)
	}
}

func TestWeightedFairness(t *testing.T) {
	s := New(Config{Workers: 1, Quantum: 1})
	s.RegisterTenant("gate", Quota{MaxQueued: 10})
	s.RegisterTenant("light", Quota{Weight: 1, MaxQueued: 1000, MaxInflight: 1000})
	s.RegisterTenant("heavy", Quota{Weight: 2, MaxQueued: 1000, MaxInflight: 1000})
	// Hold the single worker with a gate task so both tenant queues build a
	// backlog; DRR fairness only applies under backlog.
	release := make(chan struct{})
	gate, err := s.Submit(&Task{TenantID: "gate", Cost: 1, Run: func(context.Context) error {
		<-release
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var order []string
	const perTenant = 300
	var dones []<-chan error
	for i := 0; i < perTenant; i++ {
		for _, id := range []string{"light", "heavy"} {
			id := id
			d, err := s.Submit(&Task{TenantID: id, Cost: 1, Run: func(context.Context) error {
				mu.Lock()
				order = append(order, id)
				mu.Unlock()
				return nil
			}})
			if err != nil {
				t.Fatal(err)
			}
			dones = append(dones, d)
		}
	}
	close(release)
	drain(t, append(dones, gate)...)
	s.Shutdown()
	// In the first half of completions the 2:1 weight ratio must hold.
	half := order[:len(order)/2]
	var light, heavy int
	for _, id := range half {
		if id == "light" {
			light++
		} else {
			heavy++
		}
	}
	ratio := float64(heavy) / float64(light)
	t.Logf("first-half completions: light=%d heavy=%d ratio=%.2f", light, heavy, ratio)
	if ratio < 1.6 || ratio > 2.5 {
		t.Fatalf("unfair scheduling: heavy/light ratio %.2f outside [1.6, 2.5]", ratio)
	}
}

func TestFIFOWithinTenant(t *testing.T) {
	s := New(Config{Workers: 1, Quantum: 1})
	defer s.Shutdown()
	s.RegisterTenant("a", Quota{MaxQueued: 1000})
	var mu sync.Mutex
	var got []int
	var dones []<-chan error
	for i := 0; i < 200; i++ {
		i := i
		d, err := s.Submit(&Task{TenantID: "a", Cost: 1, Run: func(context.Context) error {
			mu.Lock()
			got = append(got, i)
			mu.Unlock()
			return nil
		}})
		if err != nil {
			t.Fatal(err)
		}
		dones = append(dones, d)
	}
	drain(t, dones...)
	for i := 0; i < 200; i++ {
		if got[i] != i {
			t.Fatalf("FIFO violated at position %d: got %d", i, got[i])
		}
	}
}

func TestExpensiveTaskNotStarved(t *testing.T) {
	s := New(Config{Workers: 1, Quantum: 10})
	defer s.Shutdown()
	s.RegisterTenant("bulk", Quota{Weight: 1, MaxQueued: 100})
	s.RegisterTenant("interactive", Quota{Weight: 1, MaxQueued: 100})
	var dones []<-chan error
	for i := 0; i < 5; i++ {
		d, err := s.Submit(&Task{TenantID: "bulk", Cost: 100, Run: func(context.Context) error { return nil }})
		if err != nil {
			t.Fatal(err)
		}
		dones = append(dones, d)
	}
	for i := 0; i < 50; i++ {
		d, err := s.Submit(noopTask("interactive"))
		if err != nil {
			t.Fatal(err)
		}
		dones = append(dones, d)
	}
	// Cost 100 exceeds the default deficit cap (8*1*10=80); the dynamic cap
	// must still let bulk tasks complete.
	drain(t, dones...)
}

func TestUnregisterTenant(t *testing.T) {
	s := New(Config{Workers: 1, Quantum: 1})
	defer s.Shutdown()
	s.RegisterTenant("a", Quota{MaxQueued: 10})
	if err := s.UnregisterTenant("ghost"); err != ErrNoSuchTenant {
		t.Fatalf("got %v want ErrNoSuchTenant", err)
	}
	d, err := s.Submit(noopTask("a"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UnregisterTenant("a"); err != ErrTenantBusy {
		t.Fatalf("got %v want ErrTenantBusy", err)
	}
	drain(t, d)
	if err := s.UnregisterTenant("a"); err != nil {
		t.Fatalf("unregister after drain: %v", err)
	}
	if _, err := s.Submit(noopTask("a")); err != ErrNoSuchTenant {
		t.Fatalf("got %v want ErrNoSuchTenant", err)
	}
	if err := s.RegisterTenant("a", Quota{MaxQueued: 10}); err != nil {
		t.Fatalf("re-register: %v", err)
	}
}

func TestConcurrentSubmitters(t *testing.T) {
	s := New(Config{Workers: 4, Quantum: 16})
	tenants := []string{"t1", "t2", "t3", "t4"}
	for _, id := range tenants {
		s.RegisterTenant(id, Quota{Weight: 1, MaxQueued: 10000, MaxInflight: 4})
	}
	var wg sync.WaitGroup
	var doneChans sync.Map
	const submitters = 8
	const perSubmitter = 200
	for w := 0; w < submitters; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			id := tenants[w%len(tenants)]
			var ds []<-chan error
			for i := 0; i < perSubmitter; i++ {
				d, err := s.Submit(noopTask(id))
				if err != nil {
					t.Errorf("submit: %v", err)
					return
				}
				ds = append(ds, d)
			}
			doneChans.Store(w, ds)
		}(w)
	}
	wg.Wait()
	doneChans.Range(func(_, v any) bool {
		for _, d := range v.([]<-chan error) {
			if err := <-d; err != nil {
				t.Errorf("task: %v", err)
			}
		}
		return true
	})
	for _, id := range tenants {
		st, err := s.Stats(id)
		if err != nil {
			t.Fatal(err)
		}
		want := uint64(submitters / len(tenants) * perSubmitter)
		if st.Submitted != want || st.Completed != want || st.Queued != 0 || st.Inflight != 0 {
			t.Fatalf("tenant %s: bad stats %+v want submitted=completed=%d", id, st, want)
		}
	}
	s.Shutdown()
}

func TestNoGoroutineLeak(t *testing.T) {
	base := runtime.NumGoroutine()
	for iter := 0; iter < 3; iter++ {
		s := New(Config{Workers: 4, Quantum: 4})
		s.RegisterTenant("a", Quota{MaxQueued: 1000, MaxInflight: 4})
		var dones []<-chan error
		for i := 0; i < 200; i++ {
			d, err := s.Submit(noopTask("a"))
			if err != nil {
				t.Fatal(err)
			}
			dones = append(dones, d)
		}
		drain(t, dones...)
		s.Shutdown()
		st, _ := s.Stats("a")
		if st.Queued != 0 || st.Inflight != 0 {
			t.Fatalf("scheduler state leaked: %+v", st)
		}
	}
	deadline := time.Now().Add(3 * time.Second)
	for runtime.NumGoroutine() > base && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if n := runtime.NumGoroutine(); n > base {
		t.Fatalf("goroutine leak: before=%d after=%d", base, n)
	}
}

func TestStoreTenantIsolation(t *testing.T) {
	s := New(Config{Workers: 2, Quantum: 64})
	s.RegisterTenant("alice", Quota{MaxQueued: 100, MaxInflight: 2})
	s.RegisterTenant("bob", Quota{MaxQueued: 100, MaxInflight: 2})
	st := NewStore(s)

	if err := st.Add("alice", "ids", 1, 2, 3, 100, 200); err != nil {
		t.Fatal(err)
	}
	if err := st.Add("carol", "ids", 1); err != ErrNoSuchTenant {
		t.Fatalf("got %v want ErrNoSuchTenant", err)
	}
	if _, ok := st.Snapshot("bob", "ids"); ok {
		t.Fatal("isolation breach: bob sees alice's bitmap")
	}
	snap, ok := st.Snapshot("alice", "ids")
	if !ok {
		t.Fatal("alice's bitmap missing")
	}
	for _, id := range []uint64{1, 2, 3, 100, 200} {
		if !snap.Contains(id) {
			t.Fatalf("missing id %d", id)
		}
	}
	// Snapshots are deep copies: mutating one must not affect the store.
	snap.Add(4)
	snap2, _ := st.Snapshot("alice", "ids")
	if snap2.Contains(4) {
		t.Fatal("snapshot mutation leaked into store")
	}
	// Same bitmap name under another tenant stays independent.
	if err := st.Add("bob", "ids", 7); err != nil {
		t.Fatal(err)
	}
	bobSnap, _ := st.Snapshot("bob", "ids")
	if bobSnap.Cardinality() != 1 || !bobSnap.Contains(7) {
		t.Fatalf("bob's bitmap corrupted: cardinality %d", bobSnap.Cardinality())
	}
	s.Shutdown()
}
