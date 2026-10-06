package taskgroup_test

import (
	"context"
	"fmt"
	"runtime"
	"testing"
	"time"

	"taskgroup"
)

// waitForGoroutines fails the test unless the goroutine count drops to
// at most want within two seconds.
func waitForGoroutines(t *testing.T, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if runtime.NumGoroutine() <= want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("leaked goroutines: baseline %d, now %d", want, runtime.NumGoroutine())
}

// TestReverseArrivalPicksEarliestCreatedError builds a task tree whose
// failing tasks are held at per-task gates and released in reverse
// creation order, so their errors arrive newest-first. Wait must still
// report the error from the earliest-created failing task, and no
// goroutine may survive Wait.
func TestReverseArrivalPicksEarliestCreatedError(t *testing.T) {
	baseline := runtime.NumGoroutine()

	var (
		err0 = fmt.Errorf("business error from task 0")
		err2 = fmt.Errorf("business error from task 2")
		err3 = fmt.Errorf("business error from task 3")
		err5 = fmt.Errorf("business error from task 5")
	)
	gate := make(map[int]chan struct{})
	for _, seq := range []int{0, 2, 3, 5} {
		gate[seq] = make(chan struct{})
	}

	g, ctx := taskgroup.WithContext(context.Background())

	// waitCancel yields a cancellation error once the tree is
	// canceled; it must never override a business error.
	waitCancel := func(ctx context.Context, _ *taskgroup.Group) error {
		<-ctx.Done()
		return ctx.Err()
	}

	g.Go(func(ctx context.Context, c *taskgroup.Group) error { // seq 0
		c.Go(waitCancel) // seq 1
		<-gate[0]
		return err0
	})
	g.Go(func(ctx context.Context, c *taskgroup.Group) error { // seq 2
		c.Go(func(ctx context.Context, cc *taskgroup.Group) error { // seq 3
			cc.Go(func(ctx context.Context, _ *taskgroup.Group) error { // seq 4
				<-ctx.Done()
				return nil
			})
			<-gate[3]
			return err3
		})
		<-gate[2]
		return err2
	})
	g.Go(func(ctx context.Context, c *taskgroup.Group) error { // seq 5
		<-gate[5]
		return err5
	})

	// Barrier: release the gates in reverse creation order so the
	// errors arrive 5, 3, 2, 0 — newest first.
	for _, seq := range []int{5, 3, 2, 0} {
		close(gate[seq])
		time.Sleep(10 * time.Millisecond)
	}

	if err := g.Wait(); err != err0 {
		t.Fatalf("Wait() = %v, want %v", err, err0)
	}
	if err := ctx.Err(); err != context.Canceled {
		t.Fatalf("group context error = %v, want context.Canceled", err)
	}
	waitForGoroutines(t, baseline)
}

// TestParentWaitsForDescendants ensures Wait does not return while any
// descendant task is still running, even if its parent already
// returned.
func TestParentWaitsForDescendants(t *testing.T) {
	g, _ := taskgroup.WithContext(context.Background())
	release := make(chan struct{})

	g.Go(func(ctx context.Context, c *taskgroup.Group) error {
		c.Go(func(ctx context.Context, _ *taskgroup.Group) error {
			<-release
			return nil
		})
		return nil // parent returns immediately
	})

	waitDone := make(chan error, 1)
	go func() { waitDone <- g.Wait() }()

	select {
	case err := <-waitDone:
		t.Fatalf("Wait() = %v returned before descendants exited", err)
	case <-time.After(100 * time.Millisecond):
	}

	close(release)
	if err := <-waitDone; err != nil {
		t.Fatalf("Wait() = %v, want nil", err)
	}
}

// TestFirstErrorCancelsTree ensures the first non-cancellation error
// cancels the context observed by sibling tasks.
func TestFirstErrorCancelsTree(t *testing.T) {
	g, _ := taskgroup.WithContext(context.Background())
	boom := fmt.Errorf("boom")
	observed := make(chan error, 1)

	g.Go(func(ctx context.Context, c *taskgroup.Group) error {
		<-ctx.Done()
		observed <- ctx.Err()
		return ctx.Err()
	})
	g.Go(func(ctx context.Context, c *taskgroup.Group) error {
		time.Sleep(20 * time.Millisecond) // let the sibling park first
		return boom
	})

	if err := g.Wait(); err != boom {
		t.Fatalf("Wait() = %v, want %v", err, boom)
	}
	select {
	case err := <-observed:
		if err != context.Canceled {
			t.Fatalf("sibling observed %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("sibling never observed cancellation")
	}
}

// TestCancelNeverOverridesBusinessError ensures a cancellation error
// loses to a business error even when it has the smaller creation
// sequence number.
func TestCancelNeverOverridesBusinessError(t *testing.T) {
	g, _ := taskgroup.WithContext(context.Background())
	boom := fmt.Errorf("boom")

	g.Go(func(ctx context.Context, c *taskgroup.Group) error { // seq 0
		<-ctx.Done()
		return ctx.Err()
	})
	g.Go(func(ctx context.Context, c *taskgroup.Group) error { // seq 1
		return boom
	})

	if err := g.Wait(); err != boom {
		t.Fatalf("Wait() = %v, want %v", err, boom)
	}
}

// TestOnlyCancelErrors ensures cancellation errors are reported when
// no business error occurred.
func TestOnlyCancelErrors(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	g, _ := taskgroup.WithContext(parent)

	g.Go(func(ctx context.Context, c *taskgroup.Group) error {
		<-ctx.Done()
		return ctx.Err()
	})
	cancel()

	if err := g.Wait(); err != context.Canceled {
		t.Fatalf("Wait() = %v, want context.Canceled", err)
	}
}

// TestNoError ensures a clean run reports nil and still releases the
// group context.
func TestNoError(t *testing.T) {
	g, ctx := taskgroup.WithContext(context.Background())

	g.Go(func(ctx context.Context, c *taskgroup.Group) error {
		c.Go(func(ctx context.Context, _ *taskgroup.Group) error { return nil })
		return nil
	})

	if err := g.Wait(); err != nil {
		t.Fatalf("Wait() = %v, want nil", err)
	}
	if err := ctx.Err(); err != context.Canceled {
		t.Fatalf("after Wait, context error = %v, want context.Canceled", err)
	}
}

// TestGoAfterWaitPanics ensures launching tasks into a finished group
// is rejected.
func TestGoAfterWaitPanics(t *testing.T) {
	g, _ := taskgroup.WithContext(context.Background())
	g.Go(func(ctx context.Context, c *taskgroup.Group) error { return nil })
	if err := g.Wait(); err != nil {
		t.Fatalf("Wait() = %v, want nil", err)
	}

	defer func() {
		if recover() == nil {
			t.Fatal("Go after Wait did not panic")
		}
	}()
	g.Go(func(ctx context.Context, c *taskgroup.Group) error { return nil })
}
