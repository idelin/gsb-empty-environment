package memsched

import (
	"sync/atomic"
	"testing"
	"time"
)

func TestMonitorAccounting(t *testing.T) {
	m := NewMonitor(1000, 0.8, 0.6)
	m.alloc(1, 300)
	m.alloc(2, 500)
	if got := m.Used(); got != 800 {
		t.Fatalf("used = %d, want 800", got)
	}
	if !m.Pressured() {
		t.Fatal("expected pressure at 80% usage")
	}
	if got := m.FiberUsage(1); got != 300 {
		t.Fatalf("fiber 1 usage = %d, want 300", got)
	}
	m.release(2, 500)
	if m.Pressured() {
		t.Fatal("pressure should clear after release")
	}
	if got := m.releaseAll(1); got != 300 {
		t.Fatalf("releaseAll = %d, want 300", got)
	}
	if got := m.Used(); got != 0 {
		t.Fatalf("used = %d, want 0", got)
	}
	if got := m.MaxUsed(); got != 800 {
		t.Fatalf("maxUsed = %d, want 800", got)
	}
}

func TestAllocBlocksAtCapacity(t *testing.T) {
	m := NewMonitor(100, 0.8, 0.6)
	m.alloc(1, 100) // quota exhausted

	done := make(chan struct{})
	go func() {
		m.alloc(2, 50) // must block until fiber 1 releases
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("alloc should block while quota is exhausted")
	case <-time.After(50 * time.Millisecond):
	}
	m.release(1, 60)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("alloc did not unblock after release")
	}
	if got := m.Used(); got != 90 {
		t.Fatalf("used = %d, want 90", got)
	}
}

// TestBackpressureAcceptance injects 100 batch tasks and verifies:
//   - backpressure engages at the 80% high watermark,
//   - no non-GC-capable fiber is admitted while pressured,
//   - GC-capable fibers are prioritized under pressure,
//   - admission smoothly resumes after existing fibers release memory,
//   - all 100 tasks complete and all memory is reclaimed.
func TestBackpressureAcceptance(t *testing.T) {
	const (
		capacity    = int64(1000)
		normalTasks = 80
		gcTasks     = 20
		normalMem   = int64(40) // 80 * 40 = 3200 >> capacity, forces pressure
		gcMem       = int64(10)
	)
	mon := NewMonitor(capacity, 0.8, 0.6)
	sched := NewScheduler(mon)
	defer sched.Close()

	var gcAdmittedUnderPressure int64
	var doneNormal, doneGC int64

	for i := 0; i < normalTasks+gcTasks; i++ {
		gc := i%5 == 4 // every 5th task is GC-capable
		task := &Task{GCCapable: gc}
		if gc {
			task.MemDemand = gcMem
			task.Run = func(f *Fiber) {
				if sched.Pressured() {
					atomic.AddInt64(&gcAdmittedUnderPressure, 1)
				}
				f.Alloc(gcMem)
				time.Sleep(time.Millisecond)
				if reclaimed := f.GC(); reclaimed != gcMem {
					t.Errorf("GC reclaimed %d, want %d", reclaimed, gcMem)
				}
				atomic.AddInt64(&doneGC, 1)
			}
		} else {
			task.MemDemand = normalMem
			task.Run = func(f *Fiber) {
				f.Alloc(normalMem)
				time.Sleep(5 * time.Millisecond) // hold memory like real batch work
				f.Release(normalMem)
				atomic.AddInt64(&doneNormal, 1)
			}
		}
		sched.Submit(task)
	}

	finished := make(chan struct{})
	go func() {
		sched.Wait()
		close(finished)
	}()
	select {
	case <-finished:
	case <-time.After(30 * time.Second):
		t.Fatal("deadlock: 100 tasks did not finish in 30s")
	}

	admitted, completed, blocked, episodes, violations := sched.Stats()

	if completed != normalTasks+gcTasks {
		t.Errorf("completed = %d, want %d", completed, normalTasks+gcTasks)
	}
	if admitted != normalTasks+gcTasks {
		t.Errorf("admitted = %d, want %d", admitted, normalTasks+gcTasks)
	}
	if episodes == 0 {
		t.Error("backpressure never engaged; expected usage to cross 80%")
	}
	if blocked == 0 {
		t.Error("no admission was ever deferred; backpressure did not intercept new fibers")
	}
	if violations != 0 {
		t.Errorf("%d non-GC fibers admitted while pressured, want 0", violations)
	}
	if max := mon.MaxUsed(); max < int64(0.8*float64(capacity)) {
		t.Errorf("max usage = %d, expected to reach at least 80%% of %d", max, capacity)
	}
	if max := mon.MaxUsed(); max > capacity {
		t.Errorf("max usage = %d exceeds capacity %d", max, capacity)
	}
	if gcAdmittedUnderPressure == 0 {
		t.Error("no GC-capable fiber was prioritized under pressure")
	}
	if got := mon.Used(); got != 0 {
		t.Errorf("leaked memory: used = %d after all tasks, want 0", got)
	}
	if got := atomic.LoadInt64(&doneNormal); got != normalTasks {
		t.Errorf("normal tasks done = %d, want %d", got, normalTasks)
	}
	if got := atomic.LoadInt64(&doneGC); got != gcTasks {
		t.Errorf("gc tasks done = %d, want %d", got, gcTasks)
	}
	t.Logf("admitted=%d completed=%d blockedEvents=%d pressureEpisodes=%d maxUsed=%d/%d gcUnderPressure=%d",
		admitted, completed, blocked, episodes, mon.MaxUsed(), capacity, gcAdmittedUnderPressure)
}

// TestSmoothRecovery verifies that after pressure clears, admissions ramp up
// gradually instead of all at once.
func TestSmoothRecovery(t *testing.T) {
	mon := NewMonitor(100, 0.8, 0.6)
	sched := NewScheduler(mon)
	defer sched.Close()
	sched.SetPacingInterval(20 * time.Millisecond)

	release := make(chan struct{})
	// Blocker fills memory past the high watermark and holds it.
	sched.Submit(&Task{MemDemand: 90, Run: func(f *Fiber) {
		f.Alloc(90)
		<-release
		f.Release(90)
	}})
	waitFor(t, func() bool { return sched.Pressured() }, "pressure to engage")

	// Queue 10 followers; none may be admitted while pressured.
	for i := 0; i < 10; i++ {
		sched.Submit(&Task{MemDemand: 10, Run: func(f *Fiber) {
			f.Alloc(10)
			f.Release(10)
		}})
	}
	time.Sleep(50 * time.Millisecond)
	if _, completed, _, _, _ := sched.Stats(); completed != 0 {
		t.Fatalf("%d followers admitted during backpressure, want 0", completed)
	}

	close(release) // existing fiber frees memory -> recovery begins
	waitFor(t, func() bool { return !sched.Pressured() }, "pressure to clear")

	// Immediately after recovery only the initial token is available; the
	// bucket refills at one token per 20ms, so admissions trickle out.
	time.Sleep(25 * time.Millisecond)
	_, completed, _, _, _ := sched.Stats()
	if completed > 5 { // 1 blocker + a couple of paced followers
		t.Fatalf("recovery not smooth: %d tasks completed within 25ms of recovery", completed)
	}

	sched.Wait()
	_, completed, _, _, _ = sched.Stats()
	if completed != 11 {
		t.Fatalf("completed = %d, want 11", completed)
	}
}

func waitFor(t *testing.T, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}
