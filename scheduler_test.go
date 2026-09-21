package worksteal

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestEveryTaskRunsOnce: each submitted task executes exactly once.
func TestEveryTaskRunsOnce(t *testing.T) {
	s := New(4)
	const n = 1000
	counts := make([]atomic.Int64, n)
	for i := 0; i < n; i++ {
		i := i
		if err := s.Submit(func(w *Worker) error {
			counts[i].Add(1)
			return nil
		}); err != nil {
			t.Fatalf("submit %d: %v", i, err)
		}
	}
	s.Close()
	for i := range counts {
		if got := counts[i].Load(); got != 1 {
			t.Fatalf("task %d executed %d times, want 1", i, got)
		}
	}
	stats := s.Stats()
	if stats.Submitted != n || stats.Completed != n || stats.Failed != 0 {
		t.Fatalf("bad stats: %+v", stats)
	}
}

// TestContinuousSpawnerIsShared: a worker that keeps spawning subtasks
// gets help from idle peers (stealing happens, multiple workers run tasks).
func TestContinuousSpawnerIsShared(t *testing.T) {
	s := New(8)
	const children = 500
	var executed atomic.Int64
	var participants sync.Map // worker id -> struct{}
	spawned := make(chan struct{})

	root := Task(func(w *Worker) error {
		defer close(spawned)
		for i := 0; i < children; i++ {
			err := w.Submit(func(w *Worker) error {
				time.Sleep(time.Millisecond) // injected latency so work piles up
				executed.Add(1)
				participants.Store(w.ID(), struct{}{})
				return nil
			})
			if err != nil {
				return err
			}
		}
		return nil
	})
	if err := s.Submit(root); err != nil {
		t.Fatal(err)
	}
	<-spawned // let the root finish fanning out before shutting down
	s.Close()

	if got := executed.Load(); got != children {
		t.Fatalf("executed %d subtasks, want %d", got, children)
	}
	workers := 0
	participants.Range(func(_, _ any) bool { workers++; return true })
	if workers < 2 {
		t.Fatalf("only %d worker(s) participated, want stealing across workers", workers)
	}
	if stats := s.Stats(); stats.Stolen == 0 {
		t.Fatalf("no tasks were stolen; stats: %+v", stats)
	} else {
		t.Logf("workers participated: %d, stolen: %d", workers, stats.Stolen)
	}
}

// TestStealVsLocalPopNoDoubleRun: hammer a single deque from a producer
// while all workers race to pop/steal; total executions must equal total
// admissions exactly.
func TestStealVsLocalPopNoDoubleRun(t *testing.T) {
	s := New(8)
	const n = 5000
	var executed atomic.Int64
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < n; i++ {
			err := s.Submit(func(w *Worker) error {
				executed.Add(1)
				return nil
			})
			if err != nil {
				t.Errorf("submit: %v", err)
				return
			}
		}
	}()
	wg.Wait()
	s.Close()
	if got := executed.Load(); got != n {
		t.Fatalf("executed %d tasks, want exactly %d", got, n)
	}
}

// TestFailuresCounted: injected errors and panics are counted as failures
// and do not stop the scheduler.
func TestFailuresCounted(t *testing.T) {
	s := New(4)
	boom := errors.New("boom")
	const errs, panics, oks = 30, 20, 50
	for i := 0; i < errs; i++ {
		s.Submit(func(w *Worker) error { return boom })
	}
	for i := 0; i < panics; i++ {
		s.Submit(func(w *Worker) error { panic("injected panic") })
	}
	for i := 0; i < oks; i++ {
		s.Submit(func(w *Worker) error { return nil })
	}
	s.Close()
	stats := s.Stats()
	want := Stats{Submitted: errs + panics + oks, Completed: oks, Failed: errs + panics}
	if stats.Submitted != want.Submitted || stats.Completed != want.Completed || stats.Failed != want.Failed {
		t.Fatalf("stats %+v, want %+v", stats, want)
	}
}

// TestTimingInjection: tasks with injected sleeps all complete before Close returns.
func TestTimingInjection(t *testing.T) {
	s := New(4)
	const n = 100
	var done atomic.Int64
	for i := 0; i < n; i++ {
		i := i
		s.Submit(func(w *Worker) error {
			time.Sleep(time.Duration(i%10) * time.Millisecond)
			done.Add(1)
			return nil
		})
	}
	s.Close()
	if got := done.Load(); got != n {
		t.Fatalf("Close returned with %d/%d tasks done", got, n)
	}
}

// TestSubmitAfterClose: submissions are rejected once shutdown begins.
func TestSubmitAfterClose(t *testing.T) {
	s := New(2)
	s.Close()
	if err := s.Submit(func(w *Worker) error { return nil }); !errors.Is(err, ErrClosed) {
		t.Fatalf("Submit after Close = %v, want ErrClosed", err)
	}
}

// TestResubmitRaceOnClose: a task chain keeps resubmitting itself while
// Close runs concurrently. Every admitted task must execute exactly once
// and the counters must stay conserved.
func TestResubmitRaceOnClose(t *testing.T) {
	for run := 0; run < 20; run++ {
		s := New(4)
		var admitted, executed atomic.Int64
		var chain Task
		chain = func(w *Worker) error {
			executed.Add(1)
			if err := w.Submit(chain); err == nil {
				admitted.Add(1)
			}
			return nil
		}
		if err := s.Submit(chain); err != nil {
			t.Fatal(err)
		}
		admitted.Add(1)
		time.Sleep(time.Duration(run%5) * 100 * time.Microsecond)
		s.Close() // must not return while admitted tasks are still pending
		if got, want := executed.Load(), admitted.Load(); got != want {
			t.Fatalf("run %d: executed %d, admitted %d", run, got, want)
		}
		stats := s.Stats()
		if stats.Submitted != stats.Completed+stats.Failed {
			t.Fatalf("run %d: stats not conserved: %+v", run, stats)
		}
	}
}

// TestCloseConservation: concurrent external submitters racing with Close;
// afterwards Submitted == Completed + Failed and every admitted task ran.
func TestCloseConservation(t *testing.T) {
	s := New(4)
	var executed atomic.Int64
	var stop atomic.Bool
	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !stop.Load() {
				err := s.Submit(func(w *Worker) error {
					executed.Add(1)
					return nil
				})
				if errors.Is(err, ErrClosed) {
					return
				}
			}
		}()
	}
	time.Sleep(5 * time.Millisecond)
	s.Close()
	stop.Store(true)
	wg.Wait()
	stats := s.Stats()
	if stats.Submitted != stats.Completed+stats.Failed {
		t.Fatalf("not conserved: %+v", stats)
	}
	if got := executed.Load(); got != stats.Submitted {
		t.Fatalf("executed %d, submitted %d", got, stats.Submitted)
	}
	if stats.Submitted == 0 {
		t.Fatal("no tasks were submitted during the race window")
	}
	t.Logf("conserved with %d submitted tasks", stats.Submitted)
}
