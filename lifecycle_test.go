package netsec

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

// Revocation stops new submissions immediately but lets in-flight
// operations drain; reset reopens submissions under a new generation.
func TestRevokeDrainAndResetReopensWithNewGeneration(t *testing.T) {
	pool := NewPool(1)
	tracker := NewTracker(pool)
	queue := NewQueue("q0", tracker)

	pageA := pool.Alloc()
	gen := tracker.Generation()
	op, err := tracker.Submit(gen, pageA)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if err := queue.Dispatch(op); err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	tracker.Revoke()
	if _, err := tracker.Submit(gen, pageA); !errors.Is(err, ErrRevoked) {
		t.Fatalf("submit after revoke = %v, want ErrRevoked", err)
	}

	if _, err := queue.CompleteOne(); err != nil {
		t.Fatalf("drain: %v", err)
	}
	if err := tracker.Reset(queue.Stop()); err != nil {
		t.Fatalf("reset: %v", err)
	}

	if _, err := tracker.Submit(gen, pageA); !errors.Is(err, ErrStaleGeneration) {
		t.Fatalf("submit with old generation = %v, want ErrStaleGeneration", err)
	}
	if _, err := tracker.Submit(tracker.Generation(), pageA); err != nil {
		t.Fatalf("submit with new generation: %v", err)
	}
}

// A submission referencing an unknown page fails and rolls back any pins it
// already took.
func TestSubmitUnknownPageRollsBackPins(t *testing.T) {
	pool := NewPool(1)
	tracker := NewTracker(pool)

	pageA := pool.Alloc()
	if _, err := tracker.Submit(tracker.Generation(), pageA, PageID(4242)); !errors.Is(err, ErrUnknownPage) {
		t.Fatalf("submit unknown page = %v, want ErrUnknownPage", err)
	}
	if got := pool.Pins(pageA); got != 0 {
		t.Fatalf("pageA pins after rollback = %d, want 0", got)
	}
	if got := tracker.InFlight(); got != 0 {
		t.Fatalf("in-flight after rollback = %d, want 0", got)
	}
}

// Concurrent completions, including duplicates, must never corrupt pin
// counts or free pages early.
func TestConcurrentCompletionNeverDoubleReleases(t *testing.T) {
	pool := NewPool(0)
	tracker := NewTracker(pool)

	pageA := pool.Alloc()
	writeSecret(t, pool, pageA)

	const ops = 64
	gen := tracker.Generation()
	ids := make([]OpID, 0, ops)
	for i := 0; i < ops; i++ {
		id, err := tracker.Submit(gen, pageA)
		if err != nil {
			t.Fatalf("submit %d: %v", i, err)
		}
		ids = append(ids, id)
	}
	tracker.Revoke()

	var wg sync.WaitGroup
	errs := make(chan error, 2*ops)
	for _, id := range ids {
		for r := 0; r < 2; r++ { // every op is completed twice
			wg.Add(1)
			go func(op OpID) {
				defer wg.Done()
				if err := tracker.Complete(op); err != nil && !errors.Is(err, ErrAlreadyComplete) {
					errs <- fmt.Errorf("complete %d: %w", op, err)
				}
			}(id)
		}
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}

	if got := pool.Pins(pageA); got != 0 {
		t.Fatalf("pageA pins after concurrent completion = %d, want 0", got)
	}
	if !pool.IsFree(pageA) {
		t.Fatal("pageA not returned to pool after all completions")
	}
}
