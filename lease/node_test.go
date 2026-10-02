package lease

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// Acceptance scenario: node A receives a rolling-restart instruction,
// freezes local writes, hands the lease credential to candidate B, and B
// takes over the primary role seamlessly — zero write errors, switchover in
// the millisecond range.
func TestGracefulHandoverZeroDowntime(t *testing.T) {
	coord := NewCoordinator(nil)
	a := NewNode("A", coord)
	b := NewNode("B", coord)
	router := NewRouter(a, b)

	if err := a.Acquire("orders", 5*time.Second); err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if err := a.PutContext("checkpoint", "offset=1024"); err != nil {
		t.Fatalf("put context: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var writeErrs int64
	done := make(chan struct{})
	go func() {
		defer close(done)
		for ctx.Err() == nil {
			if err := router.Write(ctx, "order-event"); err != nil && ctx.Err() == nil {
				atomic.AddInt64(&writeErrs, 1)
			}
		}
	}()

	time.Sleep(20 * time.Millisecond) // A serves traffic as primary

	// Rolling-restart instruction arrives at A -> graceful handover to B.
	switchover, err := a.InitiateHandover(b)
	if err != nil {
		t.Fatalf("handover: %v", err)
	}

	time.Sleep(20 * time.Millisecond) // B serves traffic as the new primary
	cancel()
	<-done

	if got := atomic.LoadInt64(&writeErrs); got != 0 {
		t.Fatalf("zero-error handover violated: %d write errors", got)
	}
	if a.Writes() == 0 {
		t.Fatal("A never served a write")
	}
	if b.Writes() == 0 {
		t.Fatal("B never served a write after handover")
	}
	if switchover > 10*time.Millisecond {
		t.Fatalf("switchover took %v, want well under a few milliseconds", switchover)
	}
	if a.IsLeader() {
		t.Fatal("A still believes it is leader after handover")
	}
	if !b.IsLeader() {
		t.Fatal("B is not leader after handover")
	}
	cred := b.Credential()
	if cred.Context["checkpoint"] != "offset=1024" {
		t.Fatalf("B did not inherit lease context: %v", cred.Context)
	}
	if cred.Term != 2 {
		t.Fatalf("fencing term = %d, want 2", cred.Term)
	}
	t.Logf("switchover took %v; A served %d writes, B served %d writes, 0 errors",
		switchover, a.Writes(), b.Writes())
}

// A stale holder that missed the handover (e.g. partitioned) must be fenced
// out by the term check.
func TestStaleHolderFencedAfterHandover(t *testing.T) {
	coord := NewCoordinator(nil)
	a := NewNode("A", coord)
	b := NewNode("B", coord)

	if err := a.Acquire("orders", 5*time.Second); err != nil {
		t.Fatal(err)
	}
	stale := a.Credential()

	if _, err := a.InitiateHandover(b); err != nil {
		t.Fatalf("handover: %v", err)
	}

	err := coord.Validate(stale.ID, stale.HolderID, stale.Term)
	if !errors.Is(err, ErrInvalidTerm) {
		t.Fatalf("stale credential: err = %v, want ErrInvalidTerm", err)
	}
	if err := coord.Validate("orders", "B", 2); err != nil {
		t.Fatalf("new holder: %v", err)
	}
}

// While A is frozen mid-handover, a business write must block (not fail)
// and resolve as soon as the handover completes.
func TestWriteWaitsOutHandoverFreeze(t *testing.T) {
	coord := NewCoordinator(nil)
	a := NewNode("A", coord)
	b := NewNode("B", coord)
	if err := a.Acquire("orders", 5*time.Second); err != nil {
		t.Fatal(err)
	}

	release := make(chan struct{})
	b.acceptHook = func() error { <-release; return nil } // hold the handover open

	handoverDone := make(chan error, 1)
	go func() {
		_, err := a.InitiateHandover(b)
		handoverDone <- err
	}()

	// Wait until the lease is frozen at the coordinator.
	deadline := time.Now().Add(2 * time.Second)
	for {
		if err := coord.Validate("orders", "A", 1); errors.Is(err, ErrLeaseFrozen) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("lease never froze")
		}
		time.Sleep(time.Millisecond)
	}

	writeDone := make(chan error, 1)
	go func() { writeDone <- a.Write(context.Background(), "order-event") }()

	select {
	case err := <-writeDone:
		t.Fatalf("write returned while frozen: %v", err)
	case <-time.After(50 * time.Millisecond):
		// still blocked, as required
	}

	close(release) // let the handover complete
	if err := <-handoverDone; err != nil {
		t.Fatalf("handover: %v", err)
	}
	// The blocked write wakes on the old holder, which is no longer leader;
	// a router would transparently retry it against B.
	if err := <-writeDone; !errors.Is(err, ErrNotLeader) {
		t.Fatalf("blocked write resolved with %v, want ErrNotLeader", err)
	}
	if !b.IsLeader() {
		t.Fatal("B did not take over")
	}
}

// If the candidate cannot accept, the handover aborts and A resumes serving
// without losing the lease.
func TestHandoverFailureRestoresHolder(t *testing.T) {
	coord := NewCoordinator(nil)
	a := NewNode("A", coord)
	b := NewNode("B", coord)
	if err := a.Acquire("orders", 5*time.Second); err != nil {
		t.Fatal(err)
	}

	// Candidate B fails to accept; A must abort and resume cleanly.
	b.acceptHook = func() error { return errors.New("candidate unreachable") }
	if _, err := a.InitiateHandover(b); err == nil {
		t.Fatal("expected handover to fail")
	}
	if !a.IsLeader() {
		t.Fatal("A should still be leader after aborted handover")
	}
	if err := a.Write(context.Background(), "order-event"); err != nil {
		t.Fatalf("A should resume writes after abort: %v", err)
	}
	if a.Writes() != 1 {
		t.Fatalf("writes = %d, want 1", a.Writes())
	}
}
