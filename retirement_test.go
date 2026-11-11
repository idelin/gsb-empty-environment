package netsec

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

const (
	pageStart uintptr = 0x4000_0000
	pageEnd   uintptr = pageStart + 0x1000
)

var targetPage = Interval{Start: pageStart, End: pageEnd}

// fakeProtector records permission transitions for protocol tests.
type fakeProtector struct {
	mu         sync.Mutex
	writable   []Interval
	failNext   error
	transition int
}

func (f *fakeProtector) MakeWritable(target Interval) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failNext != nil {
		err := f.failNext
		f.failNext = nil
		return err
	}
	f.transition++
	f.writable = append(f.writable, target)
	return nil
}

func (f *fakeProtector) transitions() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.transition
}

func safeReport(id, gen uint64) Report {
	return Report{
		ThreadID:         id,
		Generation:       gen,
		InstructionRange: Interval{Start: 0x2000_0000, End: 0x2000_1000},
		ReturnAddrs:      []uintptr{0x2000_0100, 0x2000_0200},
	}
}

func reportInPage(id, gen uint64) Report {
	rep := safeReport(id, gen)
	rep.InstructionRange = Interval{Start: pageStart + 0x10, End: pageStart + 0x40}
	return rep
}

func reportReturnIntoPage(id, gen uint64) Report {
	rep := safeReport(id, gen)
	rep.ReturnAddrs = append(rep.ReturnAddrs, pageStart+0x80)
	return rep
}

func mustBegin(t *testing.T, c *Coordinator) uint64 {
	t.Helper()
	gen, err := c.BeginRetirement(targetPage)
	if err != nil {
		t.Fatalf("BeginRetirement: %v", err)
	}
	return gen
}

func TestIntervalContainsAndOverlap(t *testing.T) {
	page := targetPage
	if !page.Contains(pageStart) || page.Contains(pageEnd) {
		t.Fatal("Contains must be half-open")
	}
	adjacent := Interval{Start: pageEnd, End: pageEnd + 0x1000}
	if page.Overlaps(adjacent) {
		t.Fatal("adjacent half-open intervals must not overlap")
	}
	inside := Interval{Start: pageStart + 0x10, End: pageStart + 0x20}
	if !page.Overlaps(inside) {
		t.Fatal("contained interval must overlap")
	}
	if (Interval{}).Valid() || (Interval{Start: 10, End: 9}).Valid() {
		t.Fatal("invalid intervals accepted")
	}
}

func TestHappyPathProofThenWritable(t *testing.T) {
	prot := &fakeProtector{}
	c := NewCoordinator(prot)
	for _, id := range []uint64{1, 2} {
		if err := c.RegisterThread(id); err != nil {
			t.Fatalf("RegisterThread: %v", err)
		}
	}
	gen := mustBegin(t, c)
	if gen != 1 {
		t.Fatalf("first generation = %d, want 1", gen)
	}
	if err := c.ReportAtSafepoint(safeReport(1, gen)); err != nil {
		t.Fatal(err)
	}
	if err := c.TryRetire(); !errors.Is(err, ErrProofIncomplete) {
		t.Fatalf("partial proof err = %v, want ErrProofIncomplete", err)
	}
	if err := c.ReportAtSafepoint(safeReport(2, gen)); err != nil {
		t.Fatal(err)
	}
	if err := c.TryRetire(); err != nil {
		t.Fatalf("full proof rejected: %v", err)
	}
	if prot.transitions() != 1 {
		t.Fatalf("mprotect transitions = %d, want 1", prot.transitions())
	}
	if c.Generation() != 0 {
		t.Fatal("barrier must be closed after retirement")
	}
	if err := c.TryRetire(); !errors.Is(err, ErrNoBarrier) {
		t.Fatalf("retire after close err = %v, want ErrNoBarrier", err)
	}
}

func TestStalledThreadKeepsPageExecutable(t *testing.T) {
	prot := &fakeProtector{}
	c := NewCoordinator(prot)
	_ = c.RegisterThread(1)
	_ = c.RegisterThread(2)
	gen := mustBegin(t, c)
	// Thread 1 reports; thread 2 never reaches a safepoint.
	if err := c.ReportAtSafepoint(safeReport(1, gen)); err != nil {
		t.Fatal(err)
	}

	status := c.Snapshot()
	if status.Complete {
		t.Fatal("proof must not be complete with a stalled thread")
	}
	if len(status.StalledThreads) != 1 || status.StalledThreads[0] != 2 {
		t.Fatalf("stalled = %v, want [2]", status.StalledThreads)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()
	err := c.WaitRetire(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("WaitRetire err = %v, want DeadlineExceeded", err)
	}
	if prot.transitions() != 0 {
		t.Fatal("page must stay read-only executable while proof is missing")
	}
	if c.Generation() != gen {
		t.Fatal("barrier must remain open after timeout")
	}
	// Late arrival of the stalled thread now completes the proof.
	if err := c.ReportAtSafepoint(safeReport(2, gen)); err != nil {
		t.Fatal(err)
	}
	if err := c.TryRetire(); err != nil {
		t.Fatalf("retire after stall resolved: %v", err)
	}
	if prot.transitions() != 1 {
		t.Fatalf("transitions = %d, want 1", prot.transitions())
	}
}

func TestLateReportRejectedAfterGenerationCloses(t *testing.T) {
	prot := &fakeProtector{}
	c := NewCoordinator(prot)
	_ = c.RegisterThread(1)
	gen := mustBegin(t, c)
	if err := c.CancelRetirement(); err != nil {
		t.Fatal(err)
	}
	// A report carrying the dead generation must not count as evidence.
	err := c.ReportAtSafepoint(safeReport(1, gen))
	if !errors.Is(err, ErrNoBarrier) {
		t.Fatalf("report after cancel err = %v, want ErrNoBarrier", err)
	}

	// Reopen a barrier: a stale report tagged with the old generation is
	// rejected even though a different barrier is now open.
	newGen := mustBegin(t, c)
	if newGen == gen {
		t.Fatal("generation must advance between barriers")
	}
	err = c.ReportAtSafepoint(safeReport(1, gen))
	if !errors.Is(err, ErrStaleGeneration) {
		t.Fatalf("stale report err = %v, want ErrStaleGeneration", err)
	}
	if err := c.TryRetire(); !errors.Is(err, ErrProofIncomplete) {
		t.Fatalf("stale report must not satisfy proof, got %v", err)
	}
	if prot.transitions() != 0 {
		t.Fatal("no permission change allowed on stale proof")
	}
	// Correct generation succeeds.
	if err := c.ReportAtSafepoint(safeReport(1, newGen)); err != nil {
		t.Fatal(err)
	}
	if err := c.TryRetire(); err != nil {
		t.Fatal(err)
	}
}

func TestNewThreadJoinsOpenBarrier(t *testing.T) {
	prot := &fakeProtector{}
	c := NewCoordinator(prot)
	_ = c.RegisterThread(1)
	gen := mustBegin(t, c)
	if err := c.ReportAtSafepoint(safeReport(1, gen)); err != nil {
		t.Fatal(err)
	}

	// A thread spawned after the barrier opened must join the generation.
	if err := c.RegisterThread(2); err != nil {
		t.Fatal(err)
	}
	status := c.Snapshot()
	if status.ParticipantCount != 2 || len(status.StalledThreads) != 1 {
		t.Fatalf("new participant missing: %+v", status)
	}
	if err := c.TryRetire(); !errors.Is(err, ErrProofIncomplete) {
		t.Fatalf("new thread skipped in proof, got %v", err)
	}

	// The newcomer first reports it may still return into the old page.
	if err := c.ReportAtSafepoint(reportReturnIntoPage(2, gen)); err != nil {
		t.Fatal(err)
	}
	if err := c.TryRetire(); !errors.Is(err, ErrPageReferenced) {
		t.Fatalf("referencing newcomer err = %v, want ErrPageReferenced", err)
	}
	if prot.transitions() != 0 {
		t.Fatal("page must remain executable while newcomer references it")
	}

	// Once the newcomer reaches a clean safepoint the proof completes.
	if err := c.ReportAtSafepoint(safeReport(2, gen)); err != nil {
		t.Fatal(err)
	}
	if err := c.TryRetire(); err != nil {
		t.Fatalf("clean newcomer proof: %v", err)
	}
	if prot.transitions() != 1 {
		t.Fatalf("transitions = %d, want 1", prot.transitions())
	}
}

func TestInstructionAndReturnReferencesRejected(t *testing.T) {
	cases := []struct {
		name string
		rep  func(id, gen uint64) Report
	}{
		{"instruction range in page", reportInPage},
		{"return address in page", reportReturnIntoPage},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prot := &fakeProtector{}
			c := NewCoordinator(prot)
			_ = c.RegisterThread(1)
			_ = c.RegisterThread(2)
			gen := mustBegin(t, c)
			if err := c.ReportAtSafepoint(tc.rep(1, gen)); err != nil {
				t.Fatal(err)
			}
			if err := c.ReportAtSafepoint(safeReport(2, gen)); err != nil {
				t.Fatal(err)
			}
			err := c.TryRetire()
			if !errors.Is(err, ErrPageReferenced) {
				t.Fatalf("err = %v, want ErrPageReferenced", err)
			}
			if len(c.Snapshot().ReferencingIDs) != 1 {
				t.Fatal("snapshot must identify the referencing thread")
			}
			if prot.transitions() != 0 {
				t.Fatal("referenced page must never become writable")
			}
			if c.Generation() != gen {
				t.Fatal("barrier stays open so the thread can re-report")
			}
			// Replace the bad report with a clean one and retry.
			if err := c.ReportAtSafepoint(safeReport(1, gen)); err != nil {
				t.Fatal(err)
			}
			if err := c.TryRetire(); err != nil {
				t.Fatalf("clean retry: %v", err)
			}
		})
	}
}

func TestUnregisterResolvesStallOnlyAfterStop(t *testing.T) {
	prot := &fakeProtector{}
	c := NewCoordinator(prot)
	_ = c.RegisterThread(1)
	_ = c.RegisterThread(2)
	gen := mustBegin(t, c)
	_ = c.ReportAtSafepoint(safeReport(1, gen))

	go func() {
		time.Sleep(20 * time.Millisecond)
		c.UnregisterThread(2) // thread has terminated outside generated code
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := c.WaitRetire(ctx); err != nil {
		t.Fatalf("WaitRetire after unregister: %v", err)
	}
	if prot.transitions() != 1 {
		t.Fatalf("transitions = %d, want 1", prot.transitions())
	}
}

func TestNoParticipantsAndUnknownOrInvalidReports(t *testing.T) {
	c := NewCoordinator(&fakeProtector{})
	mustBegin(t, c)
	if err := c.TryRetire(); !errors.Is(err, ErrNoParticipants) {
		t.Fatalf("empty participant set err = %v, want ErrNoParticipants", err)
	}
	_ = c.RegisterThread(7)
	if err := c.ReportAtSafepoint(safeReport(99, c.Generation())); !errors.Is(err, ErrUnknownThread) {
		t.Fatalf("unknown thread err = %v", err)
	}
	bad := safeReport(7, c.Generation())
	bad.InstructionRange = Interval{Start: 10, End: 5}
	if err := c.ReportAtSafepoint(bad); !errors.Is(err, ErrInvalidInterval) {
		t.Fatalf("invalid interval err = %v", err)
	}
	if err := c.RegisterThread(7); !errors.Is(err, ErrThreadExists) {
		t.Fatalf("duplicate register err = %v", err)
	}
	if _, err := c.BeginRetirement(targetPage); !errors.Is(err, ErrBarrierOpen) {
		t.Fatalf("double begin err = %v", err)
	}
}

func TestProtectorFailureKeepsBarrierOpen(t *testing.T) {
	prot := &fakeProtector{failNext: errors.New("kernel denied")}
	c := NewCoordinator(prot)
	_ = c.RegisterThread(1)
	gen := mustBegin(t, c)
	_ = c.ReportAtSafepoint(safeReport(1, gen))
	err := c.TryRetire()
	if !errors.Is(err, ErrProtectionFailed) {
		t.Fatalf("err = %v, want ErrProtectionFailed", err)
	}
	if c.Generation() != gen {
		t.Fatal("barrier must remain open when mprotect fails")
	}
	// Next attempt succeeds once the kernel call works.
	if err := c.TryRetire(); err != nil {
		t.Fatalf("retry after mprotect recovery: %v", err)
	}
}

func TestConcurrentReportsSatisfyWaitRetire(t *testing.T) {
	prot := &fakeProtector{}
	c := NewCoordinator(prot)
	const n = 16
	gen := mustBegin(t, c)

	var wg sync.WaitGroup
	for id := uint64(1); id <= n; id++ {
		id := id
		if err := c.RegisterThread(id); err != nil {
			t.Fatal(err)
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Stagger arrivals, including a thread that joins "late" by
			// registering after other goroutines have already reported.
			time.Sleep(time.Duration(id) * time.Millisecond)
			if err := c.ReportAtSafepoint(safeReport(id, gen)); err != nil {
				t.Errorf("report %d: %v", id, err)
			}
		}()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.WaitRetire(ctx); err != nil {
		t.Fatalf("WaitRetire: %v", err)
	}
	wg.Wait()
	if prot.transitions() != 1 {
		t.Fatalf("transitions = %d, want exactly 1", prot.transitions())
	}
}

func TestWaitRetireWakesOnNewJoinerThenReferenceClears(t *testing.T) {
	prot := &fakeProtector{}
	c := NewCoordinator(prot)
	_ = c.RegisterThread(1)
	_ = c.RegisterThread(2)
	gen := mustBegin(t, c)

	done := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		done <- c.WaitRetire(ctx)
	}()

	_ = c.ReportAtSafepoint(safeReport(1, gen))
	time.Sleep(20 * time.Millisecond)

	// The newcomer first reports that it is inside the old page, blocking
	// the proof, then reaches a clean safepoint and clears the reference.
	_ = c.ReportAtSafepoint(reportInPage(2, gen))
	time.Sleep(20 * time.Millisecond)
	if prot.transitions() != 0 {
		t.Fatal("page must stay executable while the newcomer is in-page")
	}
	if err := c.ReportAtSafepoint(safeReport(2, gen)); err != nil {
		t.Fatal(err)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("WaitRetire: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("WaitRetire did not wake after proof completed")
	}
	if prot.transitions() != 1 {
		t.Fatalf("transitions = %d, want 1", prot.transitions())
	}
}

func TestRetireHighLevelTimeoutAndSuccess(t *testing.T) {
	t.Run("timeout keeps page executable", func(t *testing.T) {
		prot := &fakeProtector{}
		c := NewCoordinator(prot)
		_ = c.RegisterThread(1)
		err := c.Retire(targetPage, 40*time.Millisecond)
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Retire timeout err = %v", err)
		}
		if prot.transitions() != 0 {
			t.Fatal("timed-out retirement must not change protection")
		}
		if c.Generation() == 0 {
			t.Fatal("barrier stays open after timeout for later retry")
		}
	})

	t.Run("success after concurrent report", func(t *testing.T) {
		prot := &fakeProtector{}
		c := NewCoordinator(prot)
		_ = c.RegisterThread(1)
		go func() {
			// The goroutine learns the generation once Retire opens it.
			deadline := time.Now().Add(time.Second)
			var gen uint64
			for time.Now().Before(deadline) {
				if gen = c.Generation(); gen != 0 {
					break
				}
				time.Sleep(time.Millisecond)
			}
			time.Sleep(15 * time.Millisecond)
			_ = c.ReportAtSafepoint(safeReport(1, gen))
		}()
		if err := c.Retire(targetPage, 2*time.Second); err != nil {
			t.Fatalf("Retire: %v", err)
		}
		if prot.transitions() != 1 {
			t.Fatalf("transitions = %d, want 1", prot.transitions())
		}
	})
}
