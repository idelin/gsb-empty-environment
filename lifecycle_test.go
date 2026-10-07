package lifecycle

import (
	"context"
	"errors"
	"testing"
	"time"
)

func waitPhase(t *testing.T, l *Lifecycle, want Phase) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if got := l.Phase(); got == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("phase = %v, want %v", l.Phase(), want)
}

// 关闭中派生: a task spawns a subtask while shutdown is draining; the
// drain must wait for both.
func TestDrainWaitsForSubtasksSpawnedDuringShutdown(t *testing.T) {
	l := New()

	proceedParent := make(chan struct{})
	subStarted := make(chan struct{})
	proceedSub := make(chan struct{})
	subDone := make(chan struct{})

	if err := l.Go(func() error {
		<-proceedParent
		// Spawn a subtask while the lifecycle is draining.
		if err := l.Go(func() error {
			close(subStarted)
			<-proceedSub
			close(subDone)
			return nil
		}); err != nil {
			return err
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	resultCh := make(chan error, 1)
	go func() { resultCh <- l.Shutdown(context.Background()) }()

	waitPhase(t, l, Draining)
	close(proceedParent)
	<-subStarted

	// The parent has returned but its subtask is still running, so
	// shutdown must not complete yet.
	select {
	case err := <-resultCh:
		t.Fatalf("Shutdown returned %v before subtask finished", err)
	case <-time.After(50 * time.Millisecond):
	}
	if got := l.Phase(); got != Draining {
		t.Fatalf("phase = %v, want Draining while subtask runs", got)
	}

	close(proceedSub)
	<-subDone
	if err := <-resultCh; err != nil {
		t.Fatalf("Shutdown = %v, want nil", err)
	}
	waitPhase(t, l, Stopped)
}

// 释放后禁止登记: once Releasing begins, Go and Defer are refused.
func TestReleaseRejectsNewWork(t *testing.T) {
	l := New()

	goDuringRelease := make(chan error, 1)
	deferDuringRelease := make(chan error, 1)
	if err := l.Defer(func() {
		// Cleanups run in the Releasing phase: registration must fail.
		goDuringRelease <- l.Go(func() error { return nil })
		deferDuringRelease <- l.Defer(func() {})
	}); err != nil {
		t.Fatal(err)
	}

	if err := l.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := <-goDuringRelease; err != ErrNotAccepting {
		t.Fatalf("Go during Releasing = %v, want ErrNotAccepting", err)
	}
	if err := <-deferDuringRelease; err != ErrNotAccepting {
		t.Fatalf("Defer during Releasing = %v, want ErrNotAccepting", err)
	}
	if err := l.Go(func() error { return nil }); err != ErrNotAccepting {
		t.Fatalf("Go after Stopped = %v, want ErrNotAccepting", err)
	}
	if err := l.Defer(func() {}); err != ErrNotAccepting {
		t.Fatalf("Defer after Stopped = %v, want ErrNotAccepting", err)
	}
}

// 失败清理: a failing task still triggers deferred cleanups (LIFO), and
// its error becomes the shared shutdown result.
func TestFailureRunsCleanupAndSharesError(t *testing.T) {
	l := New()
	boom := errors.New("boom")
	fail := make(chan struct{})

	var cleaned []string
	if err := l.Defer(func() { cleaned = append(cleaned, "first") }); err != nil {
		t.Fatal(err)
	}
	if err := l.Defer(func() { cleaned = append(cleaned, "second") }); err != nil {
		t.Fatal(err)
	}
	if err := l.Go(func() error { <-fail; return boom }); err != nil {
		t.Fatal(err)
	}

	resultCh := make(chan error, 1)
	go func() { resultCh <- l.Shutdown(context.Background()) }()
	waitPhase(t, l, Draining)
	close(fail)

	if err := <-resultCh; err != boom {
		t.Fatalf("Shutdown = %v, want %v", err, boom)
	}
	waitPhase(t, l, Stopped)

	if len(cleaned) != 2 || cleaned[0] != "second" || cleaned[1] != "first" {
		t.Fatalf("cleanups ran as %v, want [second first] (LIFO)", cleaned)
	}
	// Repeated close shares the same failure result.
	if err := l.Shutdown(context.Background()); err != boom {
		t.Fatalf("repeated Shutdown = %v, want %v", err, boom)
	}
}

// 重复关闭共享结果: concurrent Shutdown callers all observe one result.
func TestRepeatedShutdownSharesResult(t *testing.T) {
	l := New()
	release := make(chan struct{})
	if err := l.Go(func() error { <-release; return nil }); err != nil {
		t.Fatal(err)
	}

	const callers = 5
	results := make(chan error, callers)
	for i := 0; i < callers; i++ {
		go func() { results <- l.Shutdown(context.Background()) }()
	}
	waitPhase(t, l, Draining)
	close(release)

	for i := 0; i < callers; i++ {
		if err := <-results; err != nil {
			t.Fatalf("concurrent Shutdown = %v, want nil", err)
		}
	}
	if err := l.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown after Stopped = %v, want nil", err)
	}
	if err := l.Wait(); err != nil {
		t.Fatalf("Wait after Stopped = %v, want nil", err)
	}
}

// 超时返回后后台继续完成: a timed-out Shutdown returns ctx.Err() while
// the drain keeps going in the background and eventually stops.
func TestShutdownTimeoutReturnsWhileBackgroundCompletes(t *testing.T) {
	l := New()
	release := make(chan struct{})
	taskDone := make(chan struct{})
	if err := l.Go(func() error {
		<-release
		close(taskDone)
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := l.Shutdown(ctx); err != context.DeadlineExceeded {
		t.Fatalf("Shutdown = %v, want DeadlineExceeded", err)
	}
	// The caller gave up, but the lifecycle is still draining.
	if got := l.Phase(); got != Draining {
		t.Fatalf("phase after timeout = %v, want Draining", got)
	}
	select {
	case <-l.Done():
		t.Fatal("lifecycle stopped while task still blocked")
	default:
	}

	// Let the task finish; the background finisher must settle the
	// lifecycle even though no caller is waiting anymore.
	close(release)
	<-taskDone
	waitPhase(t, l, Stopped)

	// The shared result is now available to late callers.
	if err := l.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown after background completion = %v, want nil", err)
	}
}
