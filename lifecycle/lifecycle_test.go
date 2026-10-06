package lifecycle

import (
	"errors"
	"sync"
	"testing"
	"time"
)

// gate 是一个可控制的任务关卡：Close 后放行所有等待者。
type gate struct {
	ch chan struct{}
}

func newGate() *gate { return &gate{ch: make(chan struct{})} }

func (g *gate) wait() { <-g.ch }

func (g *gate) open() { close(g.ch) }

// waitState 轮询等待组进入目标状态。
func waitState(t *testing.T, g *Group, want State) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if g.State() == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("state = %v, want %v", g.State(), want)
}

// TestDrainWaitsSubtaskSpawnedDuringShutdown 验证排空阶段
// 由运行中任务派生的子任务仍会被等待。
func TestDrainWaitsSubtaskSpawnedDuringShutdown(t *testing.T) {
	g := New()

	parentEntered := make(chan struct{})
	subGate := newGate()
	subDone := make(chan struct{})

	if err := g.Go(func(s *Scope) error {
		close(parentEntered)
		// 关闭已开始（排空阶段），派生子任务。
		if err := s.Go(func(*Scope) error {
			subGate.wait()
			close(subDone)
			return nil
		}); err != nil {
			t.Errorf("spawn subtask during drain: %v", err)
		}
		return nil
	}); err != nil {
		t.Fatalf("go parent: %v", err)
	}

	<-parentEntered
	closeReturned := make(chan error, 1)
	go func() { closeReturned <- g.Close() }()

	waitState(t, g, Draining)
	select {
	case <-closeReturned:
		t.Fatal("Close returned while subtask still blocked")
	case <-time.After(50 * time.Millisecond):
	}

	subGate.open()
	<-subDone
	if err := <-closeReturned; err != nil {
		t.Fatalf("Close: %v", err)
	}
	waitState(t, g, Stopped)
}

// TestReleaseRejectsNewWork 验证进入释放阶段后禁止登记任何新工作。
func TestReleaseRejectsNewWork(t *testing.T) {
	g := New()

	releaseEntered := make(chan struct{})
	releaseProceed := newGate()
	errInHook := make(chan error, 1)

	g.OnRelease(func() {
		close(releaseEntered)
		// 释放阶段中尝试登记，必须被拒绝。
		errInHook <- g.Go(func(*Scope) error { return nil })
		releaseProceed.wait()
	})

	closeDone := make(chan error, 1)
	go func() { closeDone <- g.Close() }()

	<-releaseEntered
	waitState(t, g, Releasing)
	if err := <-errInHook; err != ErrClosed {
		t.Fatalf("Go during release = %v, want ErrClosed", err)
	}
	if err := g.Go(func(*Scope) error { return nil }); err != ErrClosed {
		t.Fatalf("Go from outside during release = %v, want ErrClosed", err)
	}

	releaseProceed.open()
	if err := <-closeDone; err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := g.Go(func(*Scope) error { return nil }); err != ErrClosed {
		t.Fatalf("Go after stop = %v, want ErrClosed", err)
	}
}

// TestFailureCleanup 验证任务失败后释放钩子仍执行清理，
// 且 Close 返回首个任务错误。
func TestFailureCleanup(t *testing.T) {
	g := New()

	taskErr := errors.New("task exploded")
	if err := g.Go(func(*Scope) error { return taskErr }); err != nil {
		t.Fatalf("go: %v", err)
	}

	var mu sync.Mutex
	cleaned := false
	g.OnRelease(func() {
		mu.Lock()
		cleaned = true
		mu.Unlock()
	})

	if err := g.Close(); err != taskErr {
		t.Fatalf("Close = %v, want task error", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if !cleaned {
		t.Fatal("release hook did not run after task failure")
	}
}

// TestCloseTimeoutReturnsButBackgroundCompletes 验证超时返回后
// 关闭流程在后台继续推进，最终到达停止阶段。
func TestCloseTimeoutReturnsButBackgroundCompletes(t *testing.T) {
	g := New()

	taskGate := newGate()
	taskDone := make(chan struct{})
	if err := g.Go(func(*Scope) error {
		taskGate.wait()
		close(taskDone)
		return nil
	}); err != nil {
		t.Fatalf("go: %v", err)
	}

	start := time.Now()
	if err := g.CloseTimeout(50 * time.Millisecond); err != ErrTimeout {
		t.Fatalf("CloseTimeout = %v, want ErrTimeout", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("CloseTimeout blocked too long: %v", elapsed)
	}
	if st := g.State(); st == Stopped {
		t.Fatal("group stopped while task still blocked")
	}

	// 放行任务，后台关闭流程应继续完成。
	taskGate.open()
	<-taskDone
	waitState(t, g, Stopped)

	// 之后的 Close 立即返回最终结果。
	if err := g.Close(); err != nil {
		t.Fatalf("Close after background completion = %v", err)
	}
}

// TestRepeatedCloseSharesResult 验证并发重复关闭共享同一份结果。
func TestRepeatedCloseSharesResult(t *testing.T) {
	g := New()

	taskErr := errors.New("boom")
	if err := g.Go(func(*Scope) error { return taskErr }); err != nil {
		t.Fatalf("go: %v", err)
	}

	const callers = 8
	results := make(chan error, callers)
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- g.Close()
		}()
	}
	wg.Wait()
	close(results)

	for err := range results {
		if err != taskErr {
			t.Fatalf("Close = %v, want shared task error", err)
		}
	}
	waitState(t, g, Stopped)
}

// TestCloseWithoutTasks 验证空组可直接完成关闭。
func TestCloseWithoutTasks(t *testing.T) {
	g := New()
	if err := g.Close(); err != nil {
		t.Fatalf("Close = %v", err)
	}
	if st := g.State(); st != Stopped {
		t.Fatalf("state = %v, want Stopped", st)
	}
}
