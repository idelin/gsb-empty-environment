package batchlease

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

// TestConcurrentAcquireAndConfirm stresses the mutex: many goroutines try to
// claim overlapping ranges (exactly one must win per range) and one writer
// drives the contiguous confirmation high-water mark.
func TestConcurrentAcquireAndConfirm(t *testing.T) {
	dir := t.TempDir()
	s := openStore(t, dir, nil)
	defer s.Close()

	base := mustAcquire(t, s, "base", 1, 200, "base")

	var wg sync.WaitGroup
	// Every goroutine fights for the same overlapping range; only one can
	// succeed, the rest must get ErrOverlap or ErrBatchExists.
	winners := make(chan string, 20)
	for g := 0; g < 20; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			id := fmt.Sprintf("dup-%d", g)
			_, err := s.Acquire(id, BatchSummary{FirstSeq: 50, LastSeq: 60, Hash: h(id)})
			if err == nil {
				winners <- id
				return
			}
			if !errors.Is(err, ErrOverlap) && !errors.Is(err, ErrBatchExists) {
				t.Errorf("unexpected acquire error: %v", err)
			}
		}(g)
	}

	// Concurrently confirm the base range in order from a single goroutine;
	// the final high-water mark must be exactly 200 with no gaps.
	confirmDone := make(chan struct{})
	go func() {
		defer close(confirmDone)
		for seq := uint64(1); seq <= 200; seq++ {
			if err := s.Confirm(base.Token, seq); err != nil {
				t.Errorf("confirm %d: %v", seq, err)
				return
			}
		}
	}()

	wg.Wait()
	close(winners)
	<-confirmDone

	if len(winners) != 0 {
		t.Fatalf("overlapping acquire succeeded %d times", len(winners))
	}
	if s.Confirmed() != 200 {
		t.Fatalf("contiguous high-water mark broken: %d", s.Confirmed())
	}

	// Reopen and confirm the concurrent work persisted intact.
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s2 := reopenStore(t, dir)
	defer s2.Close()
	if s2.Confirmed() != 200 {
		t.Fatalf("confirmed prefix lost after reopen: %d", s2.Confirmed())
	}
	for _, seq := range []uint64{1, 55, 100, 200} {
		if id, ok := s2.Identify(seq); !ok || id != "base" {
			t.Fatalf("seq %d identity: %q %v", seq, id, ok)
		}
	}
}
