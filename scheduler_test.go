package worksteal

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestAtMostOnceUnderSteal hammers the scheduler with many tasks while
// workers race between local pop and steal; every task must run exactly once.
func TestAtMostOnceUnderSteal(t *testing.T) {
	const n = 5000
	counts := make([]atomic.Int64, n)
	s := New(8)
	s.Start()
	for i := 0; i < n; i++ {
		i := i
		s.Submit(func(*Scope) error {
			counts[i].Add(1)
			return nil
		})
	}
	s.Shutdown()
	for i := 0; i < n; i++ {
		if got := counts[i].Load(); got != 1 {
			t.Fatalf("task %d ran %d times, want exactly 1", i, got)
		}
	}
	if st := s.Stats(); !st.Conserved() || st.Submitted != n {
		t.Fatalf("stats not conserved: %+v", st)
	}
}

// TestSpawningWorkerGetsHelp: one root task continuously spawns subtasks;
// other workers must steal and share the load.
func TestSpawningWorkerGetsHelp(t *testing.T) {
	const children = 2000
	var executed atomic.Int64
	var lanesMu sync.Mutex
	lanes := map[int]bool{}

	s := New(4)
	s.Start()
	s.Submit(func(sc *Scope) error {
		for i := 0; i < children; i++ {
			sc.Submit(func(c *Scope) error {
				executed.Add(1)
				lanesMu.Lock()
				lanes[c.Lane()] = true
				lanesMu.Unlock()
				return nil
			})
		}
		return nil
	})
	s.Shutdown()

	if got := executed.Load(); got != children {
		t.Fatalf("executed %d subtasks, want %d", got, children)
	}
	if len(lanes) < 2 {
		t.Fatalf("subtasks ran on %d worker(s), want help from peers", len(lanes))
	}
	st := s.Stats()
	if !st.Conserved() || st.Submitted != children+1 {
		t.Fatalf("stats not conserved: %+v", st)
	}
	t.Logf("subtasks spread across %d/%d workers", len(lanes), s.Workers())
}

// TestRecursiveTreeSpawn: subtasks spawn further subtasks; the whole tree
// must complete exactly once.
func TestRecursiveTreeSpawn(t *testing.T) {
	const depth, branch = 6, 4
	var total atomic.Int64
	var spawn func(sc *Scope, d int)
	spawn = func(sc *Scope, d int) {
		total.Add(1)
		if d == 0 {
			return
		}
		for i := 0; i < branch; i++ {
			d := d - 1
			sc.Submit(func(c *Scope) error {
				spawn(c, d)
				return nil
			})
		}
	}
	s := New(8)
	s.Start()
	s.Submit(func(sc *Scope) error { spawn(sc, depth); return nil })
	s.Shutdown()

	// total tasks = sum_{i=0}^{depth} branch^i
	want := int64(0)
	for p, d := 1, 0; d <= depth; d++ {
		want += int64(p)
		p *= branch
	}
	if got := total.Load(); got != want {
		t.Fatalf("ran %d tasks, want %d", got, want)
	}
	if st := s.Stats(); !st.Conserved() || st.Submitted != want {
		t.Fatalf("stats not conserved: %+v, want submitted=%d", st, want)
	}
}

// TestFailuresCountedNotRetried: injected failures are counted once and
// never retried; conservation still holds.
func TestFailuresCountedNotRetried(t *testing.T) {
	const n = 1000
	counts := make([]atomic.Int64, n)
	s := New(6)
	s.Start()
	for i := 0; i < n; i++ {
		i := i
		s.Submit(func(*Scope) error {
			counts[i].Add(1)
			if i%3 == 0 {
				return fmt.Errorf("injected failure %d", i)
			}
			return nil
		})
	}
	s.Shutdown()
	for i := 0; i < n; i++ {
		if got := counts[i].Load(); got != 1 {
			t.Fatalf("task %d ran %d times (failed tasks must not be retried)", i, got)
		}
	}
	st := s.Stats()
	if want := int64((n + 2) / 3); st.Failed != want { // i%3==0 for i in [0,n)
		t.Fatalf("failed=%d, want %d", st.Failed, want)
	}
	if !st.Conserved() {
		t.Fatalf("stats not conserved: %+v", st)
	}
}

// TestConcurrentExternalSubmitters: many goroutines submit while workers
// run; Wait must not return early and stats must conserve.
func TestConcurrentExternalSubmitters(t *testing.T) {
	const submitters, perSubmitter = 8, 500
	var executed atomic.Int64
	s := New(4)
	s.Start()
	var wg sync.WaitGroup
	for g := 0; g < submitters; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perSubmitter; i++ {
				s.Submit(func(*Scope) error {
					executed.Add(1)
					return nil
				})
			}
		}()
	}
	wg.Wait()
	s.Shutdown()
	if got, want := executed.Load(), int64(submitters*perSubmitter); got != want {
		t.Fatalf("executed=%d, want %d", got, want)
	}
	if st := s.Stats(); !st.Conserved() {
		t.Fatalf("stats not conserved: %+v", st)
	}
}

// TestResubmissionDuringShutdownRace: tasks keep resubmitting until a flag
// flips; shutdown started concurrently must still drain every task.
func TestResubmissionDuringShutdownRace(t *testing.T) {
	var stop atomic.Bool
	var executed atomic.Int64
	s := New(4)
	s.Start()
	var chain func(*Scope) error
	chain = func(sc *Scope) error {
		executed.Add(1)
		if !stop.Load() {
			sc.Submit(chain) // resubmit while shutdown may be starting
		}
		return nil
	}
	for i := 0; i < 16; i++ {
		s.Submit(chain)
	}
	time.Sleep(2 * time.Millisecond)
	stop.Store(true)
	done := make(chan struct{})
	go func() { s.Shutdown(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Shutdown deadlocked with in-flight resubmissions")
	}
	st := s.Stats()
	if !st.Conserved() || st.Submitted != executed.Load() {
		t.Fatalf("stats not conserved: %+v, executed=%d", st, executed.Load())
	}
}

// TestTimingInjection: tasks with injected delays must all complete before
// Wait returns.
func TestTimingInjection(t *testing.T) {
	const n = 200
	var executed atomic.Int64
	s := New(4)
	s.Start()
	for i := 0; i < n; i++ {
		i := i
		s.Submit(func(*Scope) error {
			if i%2 == 0 {
				time.Sleep(time.Millisecond)
			}
			executed.Add(1)
			return nil
		})
	}
	s.Wait() // must block until slow tasks finish
	if got := executed.Load(); got != n {
		t.Fatalf("Wait returned early: executed=%d, want %d", got, n)
	}
	s.Shutdown()
}

// TestShutdownIdempotent: double Shutdown must be safe.
func TestShutdownIdempotent(t *testing.T) {
	s := New(2)
	s.Start()
	s.Submit(func(*Scope) error { return nil })
	s.Shutdown()
	s.Shutdown()
	if st := s.Stats(); !st.Conserved() || st.Submitted != 1 {
		t.Fatalf("stats not conserved: %+v", st)
	}
}
