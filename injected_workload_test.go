package tasklock

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// TestInjectedWorkload shows the intended usage: the test injects task
// bodies, timing, and failures; the library only provides the protocol.
func TestInjectedWorkload(t *testing.T) {
	var s Scheduler
	shared := s.NewMutex("shared")

	low := s.NewTask("low", 1)
	high := s.NewTask("high", 10)
	injectedFailure := errors.New("injected failure")

	entered := make(chan struct{})  // closed when low holds the lock
	release := make(chan struct{})  // closed when the test lets low finish
	highDone := make(chan struct{}) // closed when high's wait has ended

	type job struct {
		task *Task
		body func(ctx context.Context) error // injected task body
	}
	jobs := []job{
		{task: low, body: func(ctx context.Context) error {
			if err := s.Lock(ctx, low, shared); err != nil {
				return err
			}
			defer s.Unlock(low, shared)
			close(entered)
			<-release // injected timing: hold until the test says so
			return nil
		}},
		{task: high, body: func(ctx context.Context) error {
			defer close(highDone)
			<-entered // injected timing: only contend once low holds the lock
			if err := s.Lock(ctx, high, shared); err != nil {
				// injected failure mapping: surface the timeout as a task failure
				return errors.Join(injectedFailure, err)
			}
			return s.Unlock(high, shared)
		}},
	}

	var wg sync.WaitGroup
	errs := make([]error, len(jobs))
	for i, j := range jobs {
		wg.Add(1)
		go func(i int, j job) {
			defer wg.Done()
			ctx := context.Background()
			if j.task == high {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(context.Background(), 50*time.Millisecond)
				defer cancel()
			}
			errs[i] = j.body(ctx)
		}(i, j)
	}

	// While high is blocked, low must run at high's priority.
	waitFor(t, "low to be boosted", func() bool { return low.EffectivePriority() == 10 })

	// High's injected timeout fires first; only then let low finish.
	<-highDone
	if got := low.EffectivePriority(); got != 1 {
		t.Fatalf("low effective priority after high timed out = %d, want base 1", got)
	}

	close(release)
	wg.Wait()
	if errs[0] != nil {
		t.Fatalf("low job failed: %v", errs[0])
	}
	if !errors.Is(errs[1], injectedFailure) || !errors.Is(errs[1], context.DeadlineExceeded) {
		t.Fatalf("high job error = %v, want injected failure wrapping DeadlineExceeded", errs[1])
	}
}
