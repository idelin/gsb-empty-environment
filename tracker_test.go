package netsec

import (
	"bytes"
	"errors"
	"testing"
)

var secret = bytes.Repeat([]byte{0x5A}, PageSize)

func writeSecret(t *testing.T, pool *Pool, id PageID) {
	t.Helper()
	if err := pool.Write(id, secret); err != nil {
		t.Fatalf("write secret: %v", err)
	}
}

// Partial completion: a secret page shared by two operations must stay out
// of the normal allocation pool until both confirm completion.
func TestPartialCompletionKeepsSecretPagePinned(t *testing.T) {
	pool := NewPool(2)
	tracker := NewTracker(pool)
	queue := NewQueue("q0", tracker)

	pageA := pool.Alloc()
	writeSecret(t, pool, pageA)
	pageB := pool.Alloc()

	gen := tracker.Generation()
	op1, err := tracker.Submit(gen, pageA)
	if err != nil {
		t.Fatalf("submit op1: %v", err)
	}
	op2, err := tracker.Submit(gen, pageA, pageB)
	if err != nil {
		t.Fatalf("submit op2: %v", err)
	}
	if err := queue.Dispatch(op1); err != nil {
		t.Fatalf("dispatch op1: %v", err)
	}
	if err := queue.Dispatch(op2); err != nil {
		t.Fatalf("dispatch op2: %v", err)
	}

	tracker.Revoke()

	done, err := queue.CompleteOne()
	if err != nil || done != op1 {
		t.Fatalf("complete op1 = %v, %v", done, err)
	}
	if got := pool.Pins(pageA); got != 1 {
		t.Fatalf("pageA pins after partial completion = %d, want 1", got)
	}

	// The allocator must not hand the pinned secret page back out.
	reused := pool.Alloc()
	if reused == pageA || reused == pageB {
		t.Fatalf("allocator reused pinned page %d while DMA in flight", reused)
	}
	if pool.IsFree(pageA) || pool.IsFree(pageB) {
		t.Fatal("pinned page returned to free pool before completion")
	}

	done, err = queue.CompleteOne()
	if err != nil || done != op2 {
		t.Fatalf("complete op2 = %v, %v", done, err)
	}
	if got := pool.Pins(pageA); got != 0 {
		t.Fatalf("pageA pins after full completion = %d, want 0", got)
	}

	// Once fully drained the page returns, scrubbed of the secret.
	if !pool.IsFree(pageA) {
		t.Fatal("pageA not returned to free pool after last completion")
	}
	data, err := pool.Read(pageA)
	if err != nil {
		t.Fatalf("read pageA: %v", err)
	}
	if bytes.Contains(data, secret[:16]) {
		t.Fatal("secret survived page release")
	}
}

// Reset failure: without valid all-queue stop proofs, or with operations
// still in flight, reset must fail and keep every page pinned.
func TestResetFailureKeepsPagesPinned(t *testing.T) {
	pool := NewPool(1)
	tracker := NewTracker(pool)
	q0 := NewQueue("q0", tracker)
	q1 := NewQueue("q1", tracker)

	pageA := pool.Alloc()
	writeSecret(t, pool, pageA)

	op, err := tracker.Submit(tracker.Generation(), pageA)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if err := q0.Dispatch(op); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	tracker.Revoke()

	if err := tracker.Reset(); !errors.Is(err, ErrQueueNotStopped) {
		t.Fatalf("reset without proofs = %v, want ErrQueueNotStopped", err)
	}

	// Queue still has a pending op: its proof is invalid.
	proof0 := q0.Stop()
	proof1 := q1.Stop()
	if err := tracker.Reset(proof0, proof1); !errors.Is(err, ErrQueueNotStopped) {
		t.Fatalf("reset with pending op = %v, want ErrQueueNotStopped", err)
	}

	// Drain the queue, but restart q1 so its proof goes stale.
	if _, err := q0.CompleteOne(); err != nil {
		t.Fatalf("drain q0: %v", err)
	}
	q1.Start()
	if err := tracker.Reset(q0.Stop(), proof1); !errors.Is(err, ErrQueueNotStopped) {
		t.Fatalf("reset with stale proof = %v, want ErrQueueNotStopped", err)
	}

	// Missing proof for one of the attached queues.
	if err := tracker.Reset(q0.Stop()); !errors.Is(err, ErrQueueNotStopped) {
		t.Fatalf("reset with partial proofs = %v, want ErrQueueNotStopped", err)
	}

	// In-flight reset failure: new op, valid proofs, but not drained.
	if err := tracker.Reset(q0.Stop(), q1.Stop()); err != nil {
		t.Fatalf("reset after drain: %v", err)
	}
	q0.Start()
	op2, err := tracker.Submit(tracker.Generation(), pageA)
	if err != nil {
		t.Fatalf("submit op2: %v", err)
	}
	if err := q0.Dispatch(op2); err != nil {
		t.Fatalf("dispatch op2: %v", err)
	}
	if err := tracker.Reset(q0.Stop(), q1.Stop()); !errors.Is(err, ErrQueueNotStopped) {
		t.Fatalf("reset with undrained queue = %v, want ErrQueueNotStopped", err)
	}
	if got := pool.Pins(pageA); got != 1 {
		t.Fatalf("pageA pins during failed reset = %d, want 1", got)
	}
	if reused := pool.Alloc(); reused == pageA {
		t.Fatal("allocator reused secret page after failed reset")
	}
}

// Duplicate completion must be rejected and must never release a page
// reference twice.
func TestDuplicateCompletionDoesNotReleaseTwice(t *testing.T) {
	pool := NewPool(1)
	tracker := NewTracker(pool)

	pageA := pool.Alloc()
	writeSecret(t, pool, pageA)

	gen := tracker.Generation()
	op1, err := tracker.Submit(gen, pageA)
	if err != nil {
		t.Fatalf("submit op1: %v", err)
	}
	op2, err := tracker.Submit(gen, pageA)
	if err != nil {
		t.Fatalf("submit op2: %v", err)
	}
	tracker.Revoke()

	if err := tracker.Complete(op1); err != nil {
		t.Fatalf("complete op1: %v", err)
	}
	if err := tracker.Complete(op1); !errors.Is(err, ErrAlreadyComplete) {
		t.Fatalf("duplicate complete = %v, want ErrAlreadyComplete", err)
	}
	if got := pool.Pins(pageA); got != 1 {
		t.Fatalf("pageA pins after duplicate completion = %d, want 1", got)
	}
	if reused := pool.Alloc(); reused == pageA {
		t.Fatal("duplicate completion freed the secret page early")
	}

	if err := tracker.Complete(op2); err != nil {
		t.Fatalf("complete op2: %v", err)
	}
	if got := pool.Pins(pageA); got != 0 {
		t.Fatalf("pageA pins after real completion = %d, want 0", got)
	}
	if err := tracker.Complete(OpID(9999)); !errors.Is(err, ErrUnknownOp) {
		t.Fatalf("complete unknown op = %v, want ErrUnknownOp", err)
	}
}
