package netsec

import (
	"context"
	"errors"
	"testing"
	"time"
)

func newAuthorizedTx(t *testing.T, tids ...TID) (*Kernel, *ProcHandle, *AttachTx, Auth) {
	t.Helper()
	k := NewKernel()
	pid, gen := k.Spawn()
	h, err := k.OpenPid(pid)
	if err != nil {
		t.Fatalf("open pid: %v", err)
	}
	if h.Gen() != gen {
		t.Fatalf("handle generation = %d, want %d", h.Gen(), gen)
	}
	tracer := k.NewTracer()
	auth, err := k.Authorize(tracer, h)
	if err != nil {
		t.Fatalf("authorize: %v", err)
	}
	tx, err := NewAttachTx(k, h, auth, tids)
	if err != nil {
		t.Fatalf("new attach tx: %v", err)
	}
	return k, h, tx, auth
}

// TestBarrierHappyPath confirms the basic flow: stop, confirm, read.
func TestBarrierHappyPath(t *testing.T) {
	k, h, tx, _ := newAuthorizedTx(t, 1)
	defer tx.Close()

	if err := tx.WaitBarrier(context.Background()); err != nil {
		t.Fatalf("wait barrier: %v", err)
	}
	if tx.PendingCount() != 0 {
		t.Fatalf("pending = %d, want 0", tx.PendingCount())
	}
	regs, err := tx.ReadRegisters(1)
	if err != nil {
		t.Fatalf("read regs: %v", err)
	}
	if regs != (Regs{PC: 1, SP: 2, Arg0: 3}) {
		t.Fatalf("regs = %+v", regs)
	}

	tx.Close()
	state, err := k.ThreadState(h, 1)
	if err != nil {
		t.Fatalf("thread state: %v", err)
	}
	if state != ThreadRunning {
		t.Fatalf("thread state after close = %v, want running", state)
	}
}

// TestProcessExitReusedPID is the headline PID-reuse scenario: the target
// exits, its PID is handed to a new tenant and the transaction must never read
// the new tenant's registers.
func TestProcessExitReusedPID(t *testing.T) {
	k, h, tx, auth := newAuthorizedTx(t, 1)

	if err := k.KillProcess(h); err != nil {
		t.Fatalf("kill: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := tx.WaitBarrier(ctx)
	if !errors.Is(err, ErrBarrierFailed) {
		t.Fatalf("barrier err = %v, want ErrBarrierFailed", err)
	}

	pid2, gen2 := k.Spawn()
	if pid2 != h.PID() {
		t.Fatalf("expected PID reuse, got %d vs %d", pid2, h.PID())
	}
	if gen2 == h.Gen() {
		t.Fatal("new tenant must get a fresh generation")
	}

	if _, err := tx.ReadRegisters(1); !errors.Is(err, ErrAuthStale) {
		t.Fatalf("stale read err = %v, want ErrAuthStale", err)
	}
	if _, err := k.ReadRegisters(auth, 1); !errors.Is(err, ErrAuthStale) {
		t.Fatalf("kernel stale read err = %v, want ErrAuthStale", err)
	}

	h2, err := k.OpenPid(pid2)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if !h2.Alive() || h.Alive() {
		t.Fatal("old handle must be dead, new handle alive")
	}
	tx.Close()
	state, err := k.ThreadState(h2, 1)
	if err != nil {
		t.Fatalf("new tenant state: %v", err)
	}
	if state != ThreadRunning {
		t.Fatalf("new tenant thread = %v, want running (untouched)", state)
	}
}

// TestThreadMigrationTIDReuse keeps the same process but exits and recreates a
// thread that reuses the numeric TID. A late stop from the old incarnation must
// not satisfy the barrier.
func TestThreadMigrationTIDReuse(t *testing.T) {
	k, h, tx, _ := newAuthorizedTx(t, 1)
	defer tx.Close()

	k.mu.Lock()
	oldThreadID := k.processes[h.PID()].threads[1].threadID
	k.mu.Unlock()
	if err := k.ExitThread(h, 1); err != nil {
		t.Fatalf("exit thread: %v", err)
	}
	// Deliver only the exit first: the tracked thread drops out of the barrier.
	tx.pumpEvents()

	tid2, err := k.StartThread(h)
	if err != nil {
		t.Fatalf("start thread: %v", err)
	}
	if tid2 != 1 {
		t.Fatalf("expected TID 1 reuse, got %d", tid2)
	}
	k.mu.Lock()
	newThreadID := k.processes[h.PID()].threads[1].threadID
	k.mu.Unlock()
	if newThreadID == oldThreadID {
		t.Fatal("recreated thread must have a new thread identity")
	}

	// A late stop for the old identity (same TID) must be rejected.
	tx.handleEvent(Event{
		Kind: EventStop, PID: h.PID(), Gen: h.Gen(), TID: 1, ThreadGen: oldThreadID,
	})
	if got := tx.PendingCount(); got != 0 {
		t.Fatalf("pending after stale stop = %d, want 0 (clone not yet ingested)", got)
	}

	// Now ingest the clone: the replacement joins the barrier as pending.
	tx.pumpEvents()
	if got := tx.PendingCount(); got != 1 {
		t.Fatalf("pending after clone = %d, want 1", got)
	}
	// Its same-identity stop confirmation then opens the barrier.
	tx.pumpEvents()
	if err := tx.WaitBarrier(context.Background()); err != nil {
		t.Fatalf("barrier after migration: %v", err)
	}
	regs, err := tx.ReadRegisters(1)
	if err != nil {
		t.Fatalf("read regs: %v", err)
	}
	if regs.PC != 101 {
		t.Fatalf("regs = %+v, want replacement thread", regs)
	}
}

// TestLateConfirmationIgnored injects stale and foreign events directly and
// checks that only a matching same-generation stop opens the barrier.
func TestLateConfirmationIgnored(t *testing.T) {
	k, h, tx, _ := newAuthorizedTx(t, 1)
	defer tx.Close()

	k.mu.Lock()
	correctID := k.processes[h.PID()].threads[1].threadID
	k.mu.Unlock()

	// Wrong process generation (a later tenant reusing the PID).
	tx.handleEvent(Event{Kind: EventStop, PID: h.PID(), Gen: h.Gen() + 99, TID: 1, ThreadGen: correctID})
	// Correct process generation but wrong thread identity.
	tx.handleEvent(Event{Kind: EventStop, PID: h.PID(), Gen: h.Gen(), TID: 1, ThreadGen: correctID + 99})
	// Unknown thread entirely.
	tx.handleEvent(Event{Kind: EventStop, PID: h.PID(), Gen: h.Gen(), TID: 4242, ThreadGen: 7})

	if tx.PendingCount() != 1 {
		t.Fatalf("pending = %d, stale events must not confirm", tx.PendingCount())
	}
	if _, err := tx.ReadRegisters(1); !errors.Is(err, ErrBarrierNotOpen) {
		t.Fatalf("early read err = %v, want ErrBarrierNotOpen", err)
	}

	if err := tx.WaitBarrier(context.Background()); err != nil {
		t.Fatalf("barrier: %v", err)
	}
}

// TestNewThreadJoinsBarrier verifies a thread created while the transaction is
// active is discovered via the clone event and added to the barrier.
func TestNewThreadJoinsBarrier(t *testing.T) {
	k, h, tx, _ := newAuthorizedTx(t, 1)
	defer tx.Close()

	if err := tx.WaitBarrier(context.Background()); err != nil {
		t.Fatalf("initial barrier: %v", err)
	}

	newTID, err := k.StartThread(h)
	if err != nil {
		t.Fatalf("start thread: %v", err)
	}
	tx.pumpEvents()
	if tx.PendingCount() != 1 {
		t.Fatalf("pending after clone = %d, want 1", tx.PendingCount())
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := tx.WaitBarrier(ctx); err != nil {
		t.Fatalf("barrier with new thread: %v", err)
	}
	regs, err := tx.ReadRegisters(newTID)
	if err != nil {
		t.Fatalf("read new thread regs: %v", err)
	}
	if regs.PC != 101 {
		t.Fatalf("new thread regs = %+v", regs)
	}

	tx.Close()
	for _, tid := range []TID{1, newTID} {
		state, err := k.ThreadState(h, tid)
		if err != nil {
			t.Fatalf("state tid %d: %v", tid, err)
		}
		if state != ThreadRunning {
			t.Fatalf("tid %d state = %v, want running", tid, state)
		}
	}
}

// TestFailureResumesOnlyOwnedStops ensures rollback resumes only threads this
// transaction stopped itself and leaves another tracer's stop alone.
func TestFailureResumesOnlyOwnedStops(t *testing.T) {
	k := NewKernel()
	pid, _ := k.Spawn()
	h, _ := k.OpenPid(pid)

	other := k.NewTracer()
	otherAuth, err := k.Authorize(other, h)
	if err != nil {
		t.Fatalf("other authorize: %v", err)
	}
	foreignTID, err := k.StartThread(h)
	if err != nil {
		t.Fatalf("start thread: %v", err)
	}
	if _, err := k.RequestSuspend(otherAuth, foreignTID); err != nil {
		t.Fatalf("other suspend: %v", err)
	}

	me := k.NewTracer()
	myAuth, err := k.Authorize(me, h)
	if err != nil {
		t.Fatalf("my authorize: %v", err)
	}
	tx, err := NewAttachTx(k, h, myAuth, []TID{1, foreignTID})
	if err != nil {
		t.Fatalf("new tx: %v", err)
	}

	tx.mu.Lock()
	foreign := tx.threads[foreignTID]
	ownedForeign := foreign.owned
	tx.mu.Unlock()
	if ownedForeign {
		t.Fatal("must not claim ownership of the other tracer's stop")
	}

	// The process exiting mid-barrier deterministically fails this
	// transaction and triggers rollback of only its own stops.
	if err := k.KillProcess(h); err != nil {
		t.Fatalf("kill: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := tx.WaitBarrier(ctx); !errors.Is(err, ErrBarrierFailed) {
		t.Fatalf("barrier err = %v, want ErrBarrierFailed", err)
	}
	tx.Close()
}

// TestResumeOwnership verifies only the tracer that owns a stop may resume it,
// while a same-generation observer may still read the stopped registers.
func TestResumeOwnership(t *testing.T) {
	k := NewKernel()
	pid, _ := k.Spawn()
	h, _ := k.OpenPid(pid)

	owner := k.NewTracer()
	ownerAuth, _ := k.Authorize(owner, h)
	if _, err := k.RequestSuspend(ownerAuth, 1); err != nil {
		t.Fatalf("owner suspend: %v", err)
	}

	observer := k.NewTracer()
	observerAuth, _ := k.Authorize(observer, h)
	outcome, err := k.RequestSuspend(observerAuth, 1)
	if err != nil {
		t.Fatalf("observer suspend: %v", err)
	}
	if outcome != StoppedByOther {
		t.Fatalf("observer outcome = %v, want StoppedByOther", outcome)
	}
	if _, err := k.ReadRegisters(observerAuth, 1); err != nil {
		t.Fatalf("observer read: %v", err)
	}
	if err := k.Resume(observerAuth, 1); !errors.Is(err, ErrNotOwner) {
		t.Fatalf("observer resume err = %v, want ErrNotOwner", err)
	}
	if state, _ := k.ThreadState(h, 1); state != ThreadStopped {
		t.Fatalf("thread = %v, want still stopped", state)
	}
	if err := k.Resume(ownerAuth, 1); err != nil {
		t.Fatalf("owner resume: %v", err)
	}
	if state, _ := k.ThreadState(h, 1); state != ThreadRunning {
		t.Fatalf("thread = %v, want running", state)
	}
}

// TestStaleAuthorizationRejected checks the kernel refuses an authorization
// bound to a dead generation even if the numeric PID is live again.
func TestStaleAuthorizationRejected(t *testing.T) {
	k, h, tx, auth := newAuthorizedTx(t, 1)
	defer tx.Close()

	if err := tx.WaitBarrier(context.Background()); err != nil {
		t.Fatalf("barrier: %v", err)
	}
	if err := k.KillProcess(h); err != nil {
		t.Fatalf("kill: %v", err)
	}
	pid2, _ := k.Spawn()
	if pid2 != h.PID() {
		t.Fatal("expected PID reuse")
	}
	if _, err := k.RequestSuspend(auth, 1); !errors.Is(err, ErrAuthStale) {
		t.Fatalf("suspend stale err = %v, want ErrAuthStale", err)
	}
}

// TestLateStopAfterClose confirms events arriving after Close are dropped and
// cannot mutate a finished transaction.
func TestLateStopAfterClose(t *testing.T) {
	k, h, tx, _ := newAuthorizedTx(t, 1)
	if err := tx.WaitBarrier(context.Background()); err != nil {
		t.Fatalf("barrier: %v", err)
	}
	tx.Close()

	k.mu.Lock()
	id := k.processes[h.PID()].threads[1].threadID
	k.mu.Unlock()
	tx.handleEvent(Event{Kind: EventStop, PID: h.PID(), Gen: h.Gen(), TID: 1, ThreadGen: id})
	if _, err := tx.ReadRegisters(1); !errors.Is(err, ErrTxClosed) {
		t.Fatalf("read after close err = %v, want ErrTxClosed", err)
	}
	if state, _ := k.ThreadState(h, 1); state != ThreadRunning {
		t.Fatalf("thread = %v, want running", state)
	}
}

// TestEmptyTargetRejected guards that a transaction binds at least one thread.
func TestEmptyTargetRejected(t *testing.T) {
	k := NewKernel()
	pid, _ := k.Spawn()
	h, _ := k.OpenPid(pid)
	tracer := k.NewTracer()
	auth, err := k.Authorize(tracer, h)
	if err != nil {
		t.Fatalf("authorize: %v", err)
	}
	if _, err := NewAttachTx(k, h, auth, nil); !errors.Is(err, ErrEmptyTarget) {
		t.Fatalf("empty target err = %v, want ErrEmptyTarget", err)
	}
}

// TestConcurrentTransactions exercises many independent transactions against
// the same kernel to surface locking/race problems. The tracers concurrently
// observe the same shared stop; none of them resumes it here.
func TestConcurrentTransactions(t *testing.T) {
	k := NewKernel()
	pid, _ := k.Spawn()
	h, _ := k.OpenPid(pid)

	const n = 25
	done := make(chan error, n)
	for i := 0; i < n; i++ {
		go func() {
			tracer := k.NewTracer()
			auth, err := k.Authorize(tracer, h)
			if err != nil {
				done <- err
				return
			}
			tx, err := NewAttachTx(k, h, auth, []TID{1})
			if err != nil {
				done <- err
				return
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if err := tx.WaitBarrier(ctx); err != nil {
				done <- err
				return
			}
			done <- nil
		}()
	}
	for i := 0; i < n; i++ {
		if err := <-done; err != nil {
			t.Fatalf("concurrent tx: %v", err)
		}
	}
	// The single thread remains stopped because observers never resume it.
	if state, _ := k.ThreadState(h, 1); state != ThreadStopped {
		t.Fatalf("thread = %v, want stopped", state)
	}
}
