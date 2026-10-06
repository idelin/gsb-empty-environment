package resman

import (
	"context"
	"math/rand"
	"sync"
	"testing"
	"time"
)

func mustNew(t *testing.T, cap Vector) *Manager {
	t.Helper()
	m, err := New(cap)
	if err != nil {
		t.Fatalf("New(%v): %v", []int64(cap), err)
	}
	return m
}

// acquireAsync starts an Acquire and returns a channel receiving its result.
func acquireAsync(m *Manager, ctx context.Context, need Vector) <-chan acquireResult {
	ch := make(chan acquireResult, 1)
	go func() {
		g, err := m.Acquire(ctx, need)
		ch <- acquireResult{g, err}
	}()
	return ch
}

type acquireResult struct {
	grant *Grant
	err   error
}

func mustGrant(t *testing.T, ch <-chan acquireResult, d time.Duration, what string) *Grant {
	t.Helper()
	select {
	case r := <-ch:
		if r.err != nil {
			t.Fatalf("%s: unexpected error: %v", what, r.err)
		}
		return r.grant
	case <-time.After(d):
		t.Fatalf("%s: timed out waiting for grant", what)
		return nil
	}
}

func mustNotGrant(t *testing.T, ch <-chan acquireResult, d time.Duration, what string) {
	t.Helper()
	select {
	case r := <-ch:
		t.Fatalf("%s: unexpectedly granted (err=%v)", what, r.err)
	case <-time.After(d):
	}
}

func eqVec(a, b Vector) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestAllOrNothingAndHeadOfLineBlocking verifies that a request is granted
// only when its whole vector fits, and that a blocked head of the FIFO queue
// blocks later requests even if those would fit.
func TestAllOrNothingAndHeadOfLineBlocking(t *testing.T) {
	m := mustNew(t, Vector{2, 2})
	ctx := context.Background()

	// A takes all of resource 0.
	ga := mustGrant(t, acquireAsync(m, ctx, Vector{2, 0}), time.Second, "A{2,0}")
	if got := m.Available(); !eqVec(got, Vector{0, 2}) {
		t.Fatalf("after A: available=%v, want {0 2}", []int64(got))
	}

	// B needs {1,2}: resource 0 is exhausted, so B blocks. Nothing may be
	// deducted for B while it waits (no partial grant).
	chB := acquireAsync(m, ctx, Vector{1, 2})
	mustNotGrant(t, chB, 100*time.Millisecond, "B{1,2}")
	if got := m.Available(); !eqVec(got, Vector{0, 2}) {
		t.Fatalf("B waiting must not deduct tokens: available=%v, want {0 2}", []int64(got))
	}

	// C needs {0,1}, which would fit, but FIFO head-of-line blocking keeps
	// it queued behind B.
	chC := acquireAsync(m, ctx, Vector{0, 1})
	mustNotGrant(t, chC, 100*time.Millisecond, "C{0,1} behind blocked B")

	// Releasing A unblocks B first (FIFO order).
	ga.Release()
	gb := mustGrant(t, chB, time.Second, "B{1,2} after A release")
	if got := m.Available(); !eqVec(got, Vector{1, 0}) {
		t.Fatalf("after B granted: available=%v, want {1 0}", []int64(got))
	}
	// C still blocked: resource 1 exhausted by B.
	mustNotGrant(t, chC, 100*time.Millisecond, "C{0,1} while B holds tokens")

	gb.Release()
	gc := mustGrant(t, chC, time.Second, "C{0,1} after B release")
	gc.Release()
	if got := m.Available(); !eqVec(got, Vector{2, 2}) {
		t.Fatalf("at end: available=%v, want {2 2}", []int64(got))
	}
}

// TestCancelWaitingOnly verifies that cancelling a waiting Acquire removes it
// from the queue without touching any tokens, and lets the queue proceed.
func TestCancelWaitingOnly(t *testing.T) {
	m := mustNew(t, Vector{1, 1})
	ctx := context.Background()

	holder := mustGrant(t, acquireAsync(m, ctx, Vector{1, 1}), time.Second, "holder{1,1}")

	waitCtx, cancel := context.WithCancel(ctx)
	chW := acquireAsync(m, waitCtx, Vector{1, 0})
	mustNotGrant(t, chW, 50*time.Millisecond, "waiter{1,0}")
	cancel()
	select {
	case r := <-chW:
		if r.err == nil {
			t.Fatal("cancelled waiter should return an error")
		}
		if r.grant != nil {
			t.Fatal("cancelled waiter must not hold a grant")
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled waiter did not return")
	}
	if got := m.Available(); !eqVec(got, Vector{0, 0}) {
		t.Fatalf("cancellation of waiter changed tokens: available=%v, want {0 0}", []int64(got))
	}

	// Queue still works afterwards.
	holder.Release()
	g := mustGrant(t, acquireAsync(m, ctx, Vector{1, 1}), time.Second, "post-cancel acquire")
	g.Release()
}

// TestCancelAfterGrantKeepsTokens verifies that context cancellation never
// auto-releases granted (in-use) tokens: only an explicit Release returns
// them to the pool.
func TestCancelAfterGrantKeepsTokens(t *testing.T) {
	m := mustNew(t, Vector{3, 1})
	ctx, cancel := context.WithCancel(context.Background())

	g, err := m.Acquire(ctx, Vector{2, 1})
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	cancel() // cancel AFTER the grant

	// Tokens stay checked out; a waiter for them must block.
	chW := acquireAsync(m, context.Background(), Vector{1, 1})
	mustNotGrant(t, chW, 100*time.Millisecond, "waiter while cancelled grant holds tokens")
	if got := m.Available(); !eqVec(got, Vector{1, 0}) {
		t.Fatalf("cancel auto-released tokens: available=%v, want {1 0}", []int64(got))
	}

	// Explicit Release returns them and unblocks the waiter.
	g.Release()
	gw := mustGrant(t, chW, time.Second, "waiter after explicit release")
	gw.Release()

	// Release is idempotent.
	g.Release()
	if got := m.Available(); !eqVec(got, Vector{3, 1}) {
		t.Fatalf("at end: available=%v, want {3 1}", []int64(got))
	}
}

// TestCancelledConcurrentWithGrantOwnsTokens documents the race outcome: if
// the grant wins the race with cancellation, Acquire returns the grant (nil
// error) and the caller owns the tokens until Release.
func TestCancelledConcurrentWithGrantOwnsTokens(t *testing.T) {
	m := mustNew(t, Vector{1})
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled before/while acquiring

	g, err := m.Acquire(ctx, Vector{1})
	if err != nil {
		// Cancellation won: no tokens may be held.
		if got := m.Available(); !eqVec(got, Vector{1}) {
			t.Fatalf("failed acquire leaked tokens: available=%v, want {1}", []int64(got))
		}
		return
	}
	// Grant won: tokens are checked out and require explicit Release.
	if got := m.Available(); !eqVec(got, Vector{0}) {
		t.Fatalf("granted acquire did not deduct tokens: available=%v, want {0}", []int64(got))
	}
	g.Release()
}

// TestInvalidVectors rejects malformed or unsatisfiable requests up front.
func TestInvalidVectors(t *testing.T) {
	m := mustNew(t, Vector{2, 2})
	for _, v := range []Vector{
		{1},       // wrong dim
		{1, 2, 3}, // wrong dim
		{-1, 0},   // negative
		{3, 0},    // exceeds capacity: can never be granted
		{0, 5},    // exceeds capacity
	} {
		if _, err := m.Acquire(context.Background(), v); err == nil {
			t.Fatalf("Acquire(%v): expected error", []int64(v))
		}
	}
	if got := m.Available(); !eqVec(got, Vector{2, 2}) {
		t.Fatalf("invalid requests changed tokens: available=%v", []int64(got))
	}
}

// TestConservationUnderContention hammers the manager with random vectors
// from many goroutines and continuously checks the conservation invariant
//
//	available + granted == capacity, with every component >= 0,
//
// which partial grants or lost/duplicated tokens would violate. Run with
// -race to also catch data races.
func TestConservationUnderContention(t *testing.T) {
	cap := Vector{4, 3, 2}
	m := mustNew(t, cap)

	check := func() {
		if err := m.CheckConservation(); err != nil {
			t.Error(err)
		}
	}

	const workers = 32
	const iters = 200
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			for i := 0; i < iters; i++ {
				need := Vector{
					rng.Int63n(cap[0] + 1),
					rng.Int63n(cap[1] + 1),
					rng.Int63n(cap[2] + 1),
				}
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				g, err := m.Acquire(ctx, need)
				cancel()
				if err != nil {
					continue // timed out waiting: holds nothing
				}
				check()

				time.Sleep(time.Duration(rng.Intn(100)) * time.Microsecond)

				g.Release()
				check()
			}
		}(int64(w))
	}
	wg.Wait()

	check()
	if got := m.Available(); !eqVec(got, cap) {
		t.Errorf("final available=%v, want capacity %v", []int64(got), []int64(cap))
	}
}

// TestNoPartialGrantOnTimeout: a request that cannot be fully satisfied must
// leave the pool untouched when it gives up.
func TestNoPartialGrantOnTimeout(t *testing.T) {
	m := mustNew(t, Vector{1, 1})
	g, err := m.Acquire(context.Background(), Vector{1, 0})
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer g.Release()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := m.Acquire(ctx, Vector{1, 1}); err == nil {
		t.Fatal("expected timeout for unsatisfiable {1,1}")
	}
	if got := m.Available(); !eqVec(got, Vector{0, 1}) {
		t.Fatalf("timed-out request deducted tokens: available=%v, want {0 1}", []int64(got))
	}
}
