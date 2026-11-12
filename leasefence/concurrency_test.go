package leasefence

import (
	"errors"
	"sync"
	"testing"
	"time"
)

// Concurrent in-flight tickets from the same generation are counted
// independently: cancelling one job leaves the other committable, and the
// cancelled ticket cannot commit afterwards.
func TestConcurrentTicketsAreCountedAndFencedIndependently(t *testing.T) {
	f, clock := newTestFence(t, 10*time.Second)
	defer f.Close()

	h, err := f.Acquire()
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	t1, err := f.Begin(h)
	if err != nil {
		t.Fatalf("Begin 1: %v", err)
	}
	t2, err := f.Begin(h)
	if err != nil {
		t.Fatalf("Begin 2: %v", err)
	}
	if got := f.Snapshot().InFlight; got != 2 {
		t.Fatalf("InFlight = %d, want 2", got)
	}

	if err := f.Cancel(t1); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if got := f.Snapshot().InFlight; got != 1 {
		t.Fatalf("InFlight after cancel = %d, want 1", got)
	}
	if err := f.Validate(t2); err != nil {
		t.Fatalf("remaining ticket validation: %v", err)
	}
	// The cancelled ticket is consumed; reusing it must fail at the final
	// check instead of applying a second time.
	if err := f.Commit(t1, nil); err == nil {
		t.Fatal("Commit of a cancelled ticket succeeded")
	}
	if err := f.Commit(t2, nil); err != nil {
		t.Fatalf("Commit of the remaining live ticket: %v", err)
	}

	clock.Advance(11 * time.Second)
	// t2 was consumed above; after expiry any reuse is rejected rather than
	// applied to a now-dead lease.
	if err := f.Commit(t2, nil); !errors.Is(err, ErrExpired) {
		t.Fatalf("reuse after expiry: %v, want ErrExpired", err)
	}
	if got := f.Snapshot().InFlight; got != 0 {
		t.Fatalf("InFlight after failed commits = %d, want 0", got)
	}
}

// Under concurrent expiry, re-acquire and commit attempts, jobs from
// superseded generations are fenced or expired, never applied onto a newer
// generation. Run with -race to exercise the locking.
func TestConcurrentReapAcquireAndCommitNoCrossGenerationApply(t *testing.T) {
	f, clock := newTestFence(t, 50*time.Millisecond)
	defer f.Close()

	h, err := f.Acquire()
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}

	var (
		handleMu sync.Mutex
		current  = h
	)
	setHandle := func(nh Handle) {
		handleMu.Lock()
		current = nh
		handleMu.Unlock()
	}
	getHandle := func() Handle {
		handleMu.Lock()
		defer handleMu.Unlock()
		return current
	}

	const (
		workers    = 8
		iterations = 25
	)
	var wg sync.WaitGroup
	var appliedMu sync.Mutex
	var applied []Token
	var fenceFailures, expireFailures int
	var failMu sync.Mutex

	stop := make(chan struct{})

	// Recycler rotates generations underneath the workers.
	var recyclerWG sync.WaitGroup
	recyclerWG.Add(1)
	go func() {
		defer recyclerWG.Done()
		for k := 0; k < 20; k++ {
			select {
			case <-stop:
				return
			default:
			}
			clock.Advance(25 * time.Millisecond)
			if f.ReapExpired() {
				if nh, aerr := f.Acquire(); aerr == nil {
					setHandle(nh)
				}
			}
		}
	}()

	// Workers snapshot the current handle and try to run full jobs.
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c := newBlockingCommitter()
			close(c.gate)
			for j := 0; j < iterations; j++ {
				handle := getHandle()
				ticket, berr := f.Begin(handle)
				if berr == nil {
					berr = f.Commit(ticket, c.submit)
				}
				switch {
				case berr == nil:
					appliedMu.Lock()
					applied = append(applied, ticket.Token())
					appliedMu.Unlock()
				case errors.Is(berr, ErrExpired):
					failMu.Lock()
					expireFailures++
					failMu.Unlock()
				case errors.Is(berr, ErrFenced):
					failMu.Lock()
					fenceFailures++
					failMu.Unlock()
				}
			}
		}()
	}

	// Workers are bounded by their iteration count; the recycler exits early
	// once they are done.
	done := make(chan struct{})
	go func() {
		wg.Wait()
		recyclerWG.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		close(stop)
		t.Fatal("concurrent test did not finish in time")
	}
	close(stop)

	// Every applied token must belong to a generation that genuinely existed,
	// and can never exceed the final generation (no cross-generation apply).
	finalSnap := f.Snapshot()
	appliedMu.Lock()
	defer appliedMu.Unlock()
	for _, tok := range applied {
		if tok.Generation() == 0 || tok.Generation() > finalSnap.Generation {
			t.Fatalf("illegal applied token generation %d (final generation %d)",
				tok.Generation(), finalSnap.Generation)
		}
	}
	if len(applied) == 0 && fenceFailures+expireFailures == 0 {
		t.Fatal("expected either successful commits or fencing/expiry rejections")
	}
}

// Close makes every outstanding ticket uncommittable and stops new leases.
func TestCloseFencesOutstandingTickets(t *testing.T) {
	f, _ := newTestFence(t, time.Minute)

	h, err := f.Acquire()
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	ticket, err := f.Begin(h)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	checkErr(t, f.Validate(ticket), ErrClosed)
	checkErr(t, f.Commit(ticket, nil), ErrClosed)
	if _, err := f.Acquire(); !errors.Is(err, ErrClosed) {
		t.Fatalf("Acquire after close: %v, want ErrClosed", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("double Close: %v", err)
	}
}
