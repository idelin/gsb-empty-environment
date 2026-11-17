package netsec

import (
	"errors"
	"sync"
	"testing"
)

func mustAllocate(t *testing.T, pool *PagePool, n int) []*Page {
	t.Helper()
	pages := make([]*Page, 0, n)
	for range n {
		page, err := pool.Allocate()
		if err != nil {
			t.Fatalf("Allocate: %v", err)
		}
		pages = append(pages, page)
	}
	return pages
}

func mustMapping(t *testing.T, pool *PagePool, generation Generation, pages ...*Page) *Mapping {
	t.Helper()
	m, err := NewMapping(pool, generation, pages...)
	if err != nil {
		t.Fatalf("NewMapping: %v", err)
	}
	return m
}

func mustSubmit(t *testing.T, q *DMAQueue, m *Mapping) *Operation {
	t.Helper()
	op, err := q.Submit(m)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	return op
}

func TestPartialCompletionKeepsPagesPinned(t *testing.T) {
	pool := NewPagePool(2)
	pages := mustAllocate(t, pool, 2)
	m := mustMapping(t, pool, 7, pages...)
	q := NewDMAQueue()

	first := mustSubmit(t, q, m)
	second := mustSubmit(t, q, m)
	if got := m.Pending(); got != 2 {
		t.Fatalf("Pending after two submissions = %d, want 2", got)
	}

	if err := q.Complete(first); err != nil {
		t.Fatalf("Complete first: %v", err)
	}
	if got := m.Pending(); got != 1 {
		t.Fatalf("Pending after partial completion = %d, want 1", got)
	}
	if got := pool.FreeCount(); got != 0 {
		t.Fatalf("FreeCount with one operation in flight = %d, want 0", got)
	}
	if _, err := pool.Allocate(); !errors.Is(err, ErrNoFreePages) {
		t.Fatalf("Allocate while pages are pinned: got %v, want ErrNoFreePages", err)
	}

	m.Revoke()
	if !m.Revoked() {
		t.Fatal("mapping is not revoked")
	}
	if _, err := q.Submit(m); !errors.Is(err, ErrMappingRevoked) {
		t.Fatalf("Submit after revoke: got %v, want ErrMappingRevoked", err)
	}
	if got := pool.FreeCount(); got != 0 {
		t.Fatalf("FreeCount after revoke with one in-flight operation = %d, want 0", got)
	}

	// A duplicate report for the already-completed operation must not consume
	// the other operation's reference.
	if err := q.Complete(first); err != nil {
		t.Fatalf("duplicate Complete: %v", err)
	}
	if got := m.Pending(); got != 1 {
		t.Fatalf("Pending after duplicate completion = %d, want 1", got)
	}
	if got := pool.FreeCount(); got != 0 {
		t.Fatalf("FreeCount after duplicate completion = %d, want 0", got)
	}

	if err := q.Complete(second); err != nil {
		t.Fatalf("Complete second: %v", err)
	}
	if got := m.Pending(); got != 0 {
		t.Fatalf("Pending after all completions = %d, want 0", got)
	}
	if !m.Released() {
		t.Fatal("mapping did not release its pages")
	}
	if got := pool.FreeCount(); got != 2 {
		t.Fatalf("FreeCount after all completions = %d, want 2", got)
	}

	// Late duplicate acknowledgments are harmless after release.
	if err := q.Complete(second); err != nil {
		t.Fatalf("late duplicate Complete: %v", err)
	}
	if got := pool.FreeCount(); got != 2 {
		t.Fatalf("FreeCount after late duplicate = %d, want 2", got)
	}
}

func TestDuplicateCompletionCannotReleaseLastReference(t *testing.T) {
	pool := NewPagePool(1)
	page := mustAllocate(t, pool, 1)[0]
	m := mustMapping(t, pool, 1, page)
	q := NewDMAQueue()

	first := mustSubmit(t, q, m)
	second := mustSubmit(t, q, m)

	if err := q.Complete(first); err != nil {
		t.Fatalf("Complete first: %v", err)
	}
	for i := range 3 {
		if err := q.Complete(first); err != nil {
			t.Fatalf("duplicate Complete %d: %v", i, err)
		}
	}
	m.Revoke()

	if got := m.Pending(); got != 1 {
		t.Fatalf("Pending after duplicate completion = %d, want 1", got)
	}
	if m.Released() {
		t.Fatal("mapping released pages before the final operation completed")
	}
	if got := pool.FreeCount(); got != 0 {
		t.Fatalf("FreeCount after duplicate completion = %d, want 0", got)
	}
	if _, err := pool.Allocate(); !errors.Is(err, ErrNoFreePages) {
		t.Fatalf("secret page was reused prematurely: got %v, want ErrNoFreePages", err)
	}

	if err := q.Complete(second); err != nil {
		t.Fatalf("Complete second: %v", err)
	}
	if got := pool.FreeCount(); got != 1 {
		t.Fatalf("FreeCount after final completion = %d, want 1", got)
	}
}

func TestResetRequiresFullQueueStopProof(t *testing.T) {
	pool := NewPagePool(2)
	pages := mustAllocate(t, pool, 2)
	m := mustMapping(t, pool, 11, pages...)
	q := NewDMAQueue()

	first := mustSubmit(t, q, m)
	second := mustSubmit(t, q, m)
	if err := q.Complete(first); err != nil {
		t.Fatalf("Complete first: %v", err)
	}
	m.Revoke()

	if err := m.Reset(StopProof{}); !errors.Is(err, ErrInvalidStopProof) {
		t.Fatalf("Reset without proof: got %v, want ErrInvalidStopProof", err)
	}
	if m.Released() {
		t.Fatal("failed reset released pages")
	}
	if got := pool.FreeCount(); got != 0 {
		t.Fatalf("FreeCount after failed reset = %d, want 0", got)
	}

	if _, err := q.StopProof(m); !errors.Is(err, ErrQueueNotStopped) {
		t.Fatalf("StopProof before halt: got %v, want ErrQueueNotStopped", err)
	}
	if err := q.Halt(); err != nil {
		t.Fatalf("Halt: %v", err)
	}
	proof, err := q.StopProof(m)
	if err != nil {
		t.Fatalf("StopProof: %v", err)
	}

	if err := m.Reset(proof); err != nil {
		t.Fatalf("Reset with full-queue proof: %v", err)
	}
	if !m.Released() {
		t.Fatal("successful reset did not release pages")
	}
	if got := m.Pending(); got != 0 {
		t.Fatalf("Pending after reset = %d, want 0", got)
	}
	if got := pool.FreeCount(); got != 2 {
		t.Fatalf("FreeCount after reset = %d, want 2", got)
	}

	// The unacknowledged operation was fenced by the halt. A later completion
	// report must not disturb the already-released references.
	if err := q.Complete(second); err != nil {
		t.Fatalf("late Complete after reset: %v", err)
	}
	if got := pool.FreeCount(); got != 2 {
		t.Fatalf("FreeCount after late completion = %d, want 2", got)
	}
	if err := m.Reset(proof); err != nil {
		t.Fatalf("idempotent Reset with old proof: %v", err)
	}
}

func TestFailedResetDoesNotCorruptAccounting(t *testing.T) {
	pool := NewPagePool(1)
	page := mustAllocate(t, pool, 1)[0]
	m := mustMapping(t, pool, 3, page)
	q := NewDMAQueue()

	first := mustSubmit(t, q, m)
	second := mustSubmit(t, q, m)
	if err := q.Complete(first); err != nil {
		t.Fatalf("Complete first: %v", err)
	}
	m.Revoke()

	if err := m.Reset(StopProof{}); !errors.Is(err, ErrInvalidStopProof) {
		t.Fatalf("Reset without proof: got %v, want ErrInvalidStopProof", err)
	}
	if got := m.Pending(); got != 1 {
		t.Fatalf("Pending after failed reset = %d, want 1", got)
	}
	if got := pool.FreeCount(); got != 0 {
		t.Fatalf("FreeCount after failed reset = %d, want 0", got)
	}

	// The normal completion path still works after a reset failure.
	if err := q.Complete(second); err != nil {
		t.Fatalf("Complete second: %v", err)
	}
	if got := m.Pending(); got != 0 {
		t.Fatalf("Pending after final completion = %d, want 0", got)
	}
	if got := pool.FreeCount(); got != 1 {
		t.Fatalf("FreeCount after final completion = %d, want 1", got)
	}
}

func TestProofIsScopedToMappingAndGeneration(t *testing.T) {
	pool := NewPagePool(2)
	pages := mustAllocate(t, pool, 2)
	first := mustMapping(t, pool, 100, pages[0])
	second := mustMapping(t, pool, 101, pages[1])
	q := NewDMAQueue()

	firstOp := mustSubmit(t, q, first)
	secondOp := mustSubmit(t, q, second)
	if firstOp.Generation() != first.Generation() || secondOp.Generation() != second.Generation() {
		t.Fatal("operation generation does not match its mapping")
	}
	if err := q.Complete(firstOp); err != nil {
		t.Fatalf("Complete first: %v", err)
	}
	if err := q.Halt(); err != nil {
		t.Fatalf("Halt: %v", err)
	}

	firstProof, err := q.StopProof(first)
	if err != nil {
		t.Fatalf("StopProof first: %v", err)
	}
	if err := second.Reset(firstProof); !errors.Is(err, ErrInvalidStopProof) {
		t.Fatalf("Reset with another mapping's proof: got %v, want ErrInvalidStopProof", err)
	}
	if got := pool.FreeCount(); got != 0 {
		t.Fatalf("FreeCount after cross-mapping reset = %d, want 0", got)
	}

	secondProof, err := q.StopProof(second)
	if err != nil {
		t.Fatalf("StopProof second: %v", err)
	}
	if err := second.Reset(secondProof); err != nil {
		t.Fatalf("Reset second: %v", err)
	}
	if got := pool.FreeCount(); got != 1 {
		t.Fatalf("FreeCount after second reset = %d, want 1", got)
	}
}

func TestProofDoesNotCoverDirectSubmissions(t *testing.T) {
	pool := NewPagePool(1)
	page := mustAllocate(t, pool, 1)[0]
	m := mustMapping(t, pool, 9, page)
	q := NewDMAQueue()

	if _, err := m.Submit(); err != nil {
		t.Fatalf("direct Submit: %v", err)
	}
	if err := q.Halt(); err != nil {
		t.Fatalf("Halt: %v", err)
	}
	proof, err := q.StopProof(m)
	if err != nil {
		t.Fatalf("StopProof: %v", err)
	}
	if err := m.Reset(proof); !errors.Is(err, ErrInvalidStopProof) {
		t.Fatalf("Reset with proof that misses a direct submission: got %v, want ErrInvalidStopProof", err)
	}
	if got := pool.FreeCount(); got != 0 {
		t.Fatalf("FreeCount after incomplete proof = %d, want 0", got)
	}
}

func TestRestartInvalidatesStopProof(t *testing.T) {
	pool := NewPagePool(1)
	page := mustAllocate(t, pool, 1)[0]
	m := mustMapping(t, pool, 4, page)
	q := NewDMAQueue()

	mustSubmit(t, q, m)
	if err := q.Halt(); err != nil {
		t.Fatalf("Halt: %v", err)
	}
	stale, err := q.StopProof(m)
	if err != nil {
		t.Fatalf("StopProof: %v", err)
	}
	if err := q.Restart(); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	m.Revoke()

	if err := m.Reset(stale); !errors.Is(err, ErrInvalidStopProof) {
		t.Fatalf("Reset with stale proof: got %v, want ErrInvalidStopProof", err)
	}
	if got := pool.FreeCount(); got != 0 {
		t.Fatalf("FreeCount after stale proof = %d, want 0", got)
	}
}

func TestPageBindingValidation(t *testing.T) {
	pool := NewPagePool(2)
	pages := mustAllocate(t, pool, 2)

	if _, err := NewMapping(pool, 1, pages[0], pages[0]); err == nil {
		t.Fatal("NewMapping accepted a duplicate page")
	}
	if got := pool.FreeCount(); got != 0 {
		t.Fatalf("FreeCount after failed duplicate bind = %d, want 0", got)
	}

	m := mustMapping(t, pool, 1, pages[0])
	if _, err := NewMapping(pool, 2, pages[0]); err == nil {
		t.Fatal("NewMapping accepted a page already bound to another mapping")
	}
	m.Revoke()
	if got := pool.FreeCount(); got != 1 {
		t.Fatalf("FreeCount after revoking one-page mapping = %d, want 1", got)
	}

	otherPool := NewPagePool(1)
	other := mustAllocate(t, otherPool, 1)[0]
	if _, err := NewMapping(pool, 2, other); err == nil {
		t.Fatal("NewMapping accepted a page from another pool")
	}
}

func TestConcurrentCompletionsAndRevoke(t *testing.T) {
	const operationCount = 256

	pool := NewPagePool(1)
	page := mustAllocate(t, pool, 1)[0]
	m := mustMapping(t, pool, 55, page)
	q := NewDMAQueue()

	ops := make([]*Operation, operationCount)
	for i := range ops {
		ops[i] = mustSubmit(t, q, m)
	}

	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(operationCount*2 + 1)
	for _, op := range ops {
		for range 2 {
			go func(op *Operation) {
				defer wg.Done()
				<-start
				if err := q.Complete(op); err != nil {
					t.Errorf("concurrent Complete: %v", err)
				}
			}(op)
		}
	}
	go func() {
		defer wg.Done()
		<-start
		m.Revoke()
	}()
	close(start)
	wg.Wait()

	if got := m.Pending(); got != 0 {
		t.Fatalf("Pending after concurrent completions = %d, want 0", got)
	}
	if !m.Released() {
		t.Fatal("mapping did not release after concurrent completions")
	}
	if got := pool.FreeCount(); got != 1 {
		t.Fatalf("FreeCount after concurrent completions = %d, want 1", got)
	}
}
