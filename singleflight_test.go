package singleflight

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// eventually 在超时前等待条件成立。
func eventually(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("condition not met: %s", msg)
}

// 十个并发调用只执行一次 fn，且全部拿到相同结果。
func TestTenCallersShareOneExecution(t *testing.T) {
	var g Group
	var runs atomic.Int32
	release := make(chan struct{})
	fn := func(ctx context.Context) (any, error) {
		runs.Add(1)
		<-release // 由测试注入时间：阻塞直到放行
		return "value", nil
	}

	const n = 10
	type res struct {
		v   any
		err error
	}
	results := make(chan res, n)
	for i := 0; i < n; i++ {
		go func() {
			v, err := g.Do(context.Background(), "k", fn)
			results <- res{v, err}
		}()
	}

	eventually(t, func() bool { return runs.Load() == 1 }, "fn started")
	close(release)

	for i := 0; i < n; i++ {
		select {
		case r := <-results:
			if r.err != nil || r.v != "value" {
				t.Fatalf("waiter %d got (%v, %v)", i, r.v, r.err)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("waiter %d not released", i)
		}
	}
	if got := runs.Load(); got != 1 {
		t.Fatalf("fn ran %d times, want 1", got)
	}
}

// 取消九个等待者不影响剩余一个：计算不被取消，幸存者拿到结果。
func TestCancelNineOfTen(t *testing.T) {
	var g Group
	var runs atomic.Int32
	started := make(chan struct{})
	release := make(chan struct{})
	fn := func(ctx context.Context) (any, error) {
		runs.Add(1)
		close(started)
		select {
		case <-release:
			return "ok", nil // 只有未被取消才能走到这里
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	// 幸存者：不可取消。
	type res struct {
		v   any
		err error
	}
	survivor := make(chan res, 1)
	go func() {
		v, err := g.Do(context.Background(), "k", fn)
		survivor <- res{v, err}
	}()
	<-started // 确保计算已开始、幸存者已附着

	// 九个可取消的等待者。
	const quitters = 9
	var ready sync.WaitGroup
	ready.Add(quitters)
	cancels := make([]context.CancelFunc, quitters)
	quitErrs := make(chan error, quitters)
	for i := 0; i < quitters; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		cancels[i] = cancel
		go func(ctx context.Context) {
			ready.Done()
			_, err := g.Do(ctx, "k", fn)
			quitErrs <- err
		}(ctx)
	}
	ready.Wait()
	for _, cancel := range cancels {
		cancel()
	}
	for i := 0; i < quitters; i++ {
		select {
		case err := <-quitErrs:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("quitter got %v, want context.Canceled", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("quitter %d not released after cancel", i)
		}
	}

	// 九个都取消后，幸存者仍在等待；放行计算。
	close(release)
	select {
	case r := <-survivor:
		if r.err != nil || r.v != "ok" {
			t.Fatalf("survivor got (%v, %v)", r.v, r.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("survivor not released")
	}
	if got := runs.Load(); got != 1 {
		t.Fatalf("fn ran %d times, want 1", got)
	}
}

// 所有等待者取消时终止计算：fn 的 context 被取消，且新调用重新执行。
func TestAllWaitersCancelTerminatesComputation(t *testing.T) {
	var g Group
	var runs atomic.Int32
	started := make(chan struct{})
	fnCtxDone := make(chan struct{})
	fn := func(ctx context.Context) (any, error) {
		runs.Add(1)
		close(started)
		<-ctx.Done()
		close(fnCtxDone)
		return nil, ctx.Err()
	}

	const n = 3
	var ready sync.WaitGroup
	ready.Add(n)
	cancels := make([]context.CancelFunc, n)
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		cancels[i] = cancel
		go func(ctx context.Context) {
			ready.Done()
			_, err := g.Do(ctx, "k", fn)
			errs <- err
		}(ctx)
	}
	<-started
	ready.Wait()
	for _, cancel := range cancels {
		cancel()
	}
	for i := 0; i < n; i++ {
		if err := <-errs; !errors.Is(err, context.Canceled) {
			t.Fatalf("waiter got %v, want context.Canceled", err)
		}
	}
	select {
	case <-fnCtxDone: // 计算确实被终止
	case <-time.After(2 * time.Second):
		t.Fatal("computation context was not canceled after all waiters left")
	}

	// 记录已移除：新调用重新执行 fn。
	eventually(t, func() bool { return runs.Load() >= 1 }, "first run finished")
	v, err := g.Do(context.Background(), "k", func(ctx context.Context) (any, error) {
		runs.Add(1)
		return "fresh", nil
	})
	if err != nil || v != "fresh" {
		t.Fatalf("new call got (%v, %v)", v, err)
	}
	if got := runs.Load(); got != 2 {
		t.Fatalf("fn ran %d times, want 2", got)
	}
}

// fn panic 转成 *PanicError 并释放所有等待者。
func TestPanicBecomesErrorAndReleasesWaiters(t *testing.T) {
	var g Group
	fn := func(ctx context.Context) (any, error) {
		panic("boom")
	}

	const n = 5
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		go func() {
			_, err := g.Do(context.Background(), "k", fn)
			errs <- err
		}()
	}
	for i := 0; i < n; i++ {
		select {
		case err := <-errs:
			var pe *PanicError
			if !errors.As(err, &pe) {
				t.Fatalf("waiter got %T %v, want *PanicError", err, err)
			}
			if pe.Value != "boom" {
				t.Fatalf("panic value = %v, want boom", pe.Value)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("waiter %d not released after panic", i)
		}
	}

	// panic 后状态已清理，新调用可正常执行。
	v, err := g.Do(context.Background(), "k", func(ctx context.Context) (any, error) {
		return "recovered", nil
	})
	if err != nil || v != "recovered" {
		t.Fatalf("call after panic got (%v, %v)", v, err)
	}
}

// 注入的失败传播给所有等待者，且失败状态不被后续调用复用。
func TestFailureNotReused(t *testing.T) {
	var g Group
	var runs atomic.Int32
	injected := errors.New("injected failure")
	release := make(chan struct{})
	fn := func(ctx context.Context) (any, error) {
		n := runs.Add(1)
		<-release
		if n == 1 {
			return nil, injected
		}
		return "recovered", nil
	}

	const waiters = 4
	errs := make(chan error, waiters)
	for i := 0; i < waiters; i++ {
		go func() {
			_, err := g.Do(context.Background(), "k", fn)
			errs <- err
		}()
	}
	eventually(t, func() bool { return runs.Load() == 1 }, "fn started")
	close(release)
	for i := 0; i < waiters; i++ {
		if err := <-errs; !errors.Is(err, injected) {
			t.Fatalf("waiter got %v, want injected failure", err)
		}
	}

	// 失败已关闭的状态不得复用：新调用必须重新执行。
	v, err := g.Do(context.Background(), "k", fn)
	if err != nil || v != "recovered" {
		t.Fatalf("call after failure got (%v, %v)", v, err)
	}
	if got := runs.Load(); got != 2 {
		t.Fatalf("fn ran %d times, want 2", got)
	}
}

// 完成与新调用竞态：大量并发下，结果要么来自共享执行、要么来自新执行，
// 绝不出现复用已关闭状态（由 -race 与结果一致性共同验证）。
func TestCompletionRacesWithNewCalls(t *testing.T) {
	for round := 0; round < 200; round++ {
		var g Group
		release := make(chan struct{})
		var runs atomic.Int32
		fn := func(ctx context.Context) (any, error) {
			n := runs.Add(1)
			<-release
			if n == 1 {
				return nil, fmt.Errorf("round %d first failure", round)
			}
			return n, nil
		}

		first := make(chan error, 1)
		go func() {
			_, err := g.Do(context.Background(), "k", fn)
			first <- err
		}()
		eventually(t, func() bool { return runs.Load() == 1 }, "fn started")

		const arrivals = 8
		results := make(chan error, arrivals)
		go func() {
			close(release) // 完成与下面的新调用并发
			for i := 0; i < arrivals; i++ {
				go func() {
					_, err := g.Do(context.Background(), "k", fn)
					results <- err
				}()
			}
		}()

		if err := <-first; err == nil {
			t.Fatal("first call should fail")
		}
		for i := 0; i < arrivals; i++ {
			<-results // 任何结果都合法，只要不挂死、不 panic
		}
	}
}

// 取消与完成竞态：等待者取消时计算恰好完成，两种结果都合法，但不能挂死。
func TestCancelRacesWithCompletion(t *testing.T) {
	for round := 0; round < 200; round++ {
		var g Group
		fn := func(ctx context.Context) (any, error) {
			return "done", nil
		}
		ctx, cancel := context.WithCancel(context.Background())
		out := make(chan error, 1)
		go func() {
			_, err := g.Do(ctx, "k", fn)
			out <- err
		}()
		go cancel()
		select {
		case err := <-out:
			if err != nil && !errors.Is(err, context.Canceled) {
				t.Fatalf("unexpected error: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("waiter stuck")
		}
	}
}
