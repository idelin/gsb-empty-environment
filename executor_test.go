package executor

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

// TestPeakConcurrency verifies that with a wide finite task tree the number
// of simultaneously running tasks reaches but never exceeds the limit.
func TestPeakConcurrency(t *testing.T) {
	const limit = 3
	const children = 9

	ex := New(limit)
	var cur, max atomic.Int64

	err := ex.Run(context.Background(), func(ctx context.Context, s *Scope) error {
		for i := 0; i < children; i++ {
			s.Spawn(func(ctx context.Context, s *Scope) error {
				n := cur.Add(1)
				for {
					m := max.Load()
					if n <= m || max.CompareAndSwap(m, n) {
						break
					}
				}
				time.Sleep(30 * time.Millisecond)
				cur.Add(-1)
				return nil
			})
		}
		return s.Wait()
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := max.Load(); got != limit {
		t.Fatalf("observed max running = %d, want %d", got, limit)
	}
	if got := ex.Peak(); got != limit {
		t.Fatalf("Peak() = %d, want %d", got, limit)
	}
	if got := ex.Running(); got != 0 {
		t.Fatalf("Running() = %d, want 0 after run", got)
	}
}

// TestNestedWaitNoDeadlock builds a chain of depth 4 with a single slot.
// Each level must lend its slot while waiting so the next level can run.
func TestNestedWaitNoDeadlock(t *testing.T) {
	ex := New(1)

	var leafRan atomic.Bool
	var descend func(depth int) TaskFunc
	descend = func(depth int) TaskFunc {
		return func(ctx context.Context, s *Scope) error {
			if depth == 0 {
				leafRan.Store(true)
				return nil
			}
			s.Spawn(descend(depth - 1))
			return s.Wait()
		}
	}

	done := make(chan error, 1)
	go func() { done <- ex.Run(context.Background(), descend(4)) }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("deadlock: nested waits did not complete with limit=1")
	}
	if !leafRan.Load() {
		t.Fatal("leaf task never ran")
	}
	if got := ex.Running(); got != 0 {
		t.Fatalf("Running() = %d, want 0 after run", got)
	}
}

// TestWaitLendsSlot verifies that a waiting parent's lent slot lets more
// children run than the remaining free slots would allow.
func TestWaitLendsSlot(t *testing.T) {
	ex := New(1) // parent holds the only slot; children need it lent

	var ran atomic.Int64
	done := make(chan error, 1)
	go func() {
		done <- ex.Run(context.Background(), func(ctx context.Context, s *Scope) error {
			for i := 0; i < 3; i++ {
				s.Spawn(func(ctx context.Context, s *Scope) error {
					ran.Add(1)
					return nil
				})
			}
			return s.Wait()
		})
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("deadlock: parent did not lend its slot while waiting")
	}
	if got := ran.Load(); got != 3 {
		t.Fatalf("ran = %d, want 3", got)
	}
}

// TestCancellationReturnsSlots cancels a run whose tasks block forever and
// verifies every slot is returned and the executor stays usable.
func TestCancellationReturnsSlots(t *testing.T) {
	const limit = 2
	ex := New(limit)

	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})

	done := make(chan error, 1)
	go func() {
		done <- ex.Run(ctx, func(ctx context.Context, s *Scope) error {
			for i := 0; i < limit; i++ {
				s.Spawn(func(ctx context.Context, s *Scope) error {
					<-ctx.Done()
					return ctx.Err()
				})
			}
			close(started)
			return s.Wait()
		})
	}()

	<-started
	time.Sleep(50 * time.Millisecond) // let children acquire slots
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run error = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("run did not finish after cancel")
	}

	if got := ex.Running(); got != 0 {
		t.Fatalf("Running() = %d after cancel, want 0 (slots leaked)", got)
	}

	// The executor must still be fully usable.
	if err := ex.Run(context.Background(), func(ctx context.Context, s *Scope) error {
		for i := 0; i < limit*2; i++ {
			s.Spawn(func(ctx context.Context, s *Scope) error { return nil })
		}
		return s.Wait()
	}); err != nil {
		t.Fatalf("second Run after cancel: %v", err)
	}
}

// TestErrorPropagation verifies that an error from a deeply nested child
// reaches the root Run call and that siblings are canceled.
func TestErrorPropagation(t *testing.T) {
	ex := New(4)
	sentinel := errors.New("boom")

	var siblingCanceled atomic.Bool

	err := ex.Run(context.Background(), func(ctx context.Context, s *Scope) error {
		s.Spawn(func(ctx context.Context, s *Scope) error {
			s.Spawn(func(ctx context.Context, s *Scope) error {
				return fmt.Errorf("deep: %w", sentinel)
			})
			return s.Wait()
		})
		s.Spawn(func(ctx context.Context, s *Scope) error {
			<-ctx.Done()
			siblingCanceled.Store(true)
			return nil
		})
		return s.Wait()
	})

	if !errors.Is(err, sentinel) {
		t.Fatalf("Run error = %v, want wrapped sentinel", err)
	}
	if !siblingCanceled.Load() {
		t.Fatal("sibling was not canceled after child failure")
	}
	if got := ex.Running(); got != 0 {
		t.Fatalf("Running() = %d, want 0 after run", got)
	}
}

// TestWaitWithoutSpawn is a sanity check that Wait on a childless scope is
// a no-op and the slot stays held.
func TestWaitWithoutSpawn(t *testing.T) {
	ex := New(1)
	if err := ex.Run(context.Background(), func(ctx context.Context, s *Scope) error {
		if err := s.Wait(); err != nil {
			return err
		}
		if got := ex.Running(); got != 1 {
			t.Errorf("Running() inside task = %d, want 1", got)
		}
		return nil
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
}
