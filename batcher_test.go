package batcher

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// manualClock 是注入测试的假时钟：定时器不会自动触发，
// 由测试显式调用 fireAll 推进时间。
type manualClock struct {
	mu     sync.Mutex
	timers map[*manualTimer]struct{}
}

type manualTimer struct {
	mu      sync.Mutex
	f       func()
	fired   bool
	stopped bool
}

func newManualClock() *manualClock {
	return &manualClock{timers: make(map[*manualTimer]struct{})}
}

func (c *manualClock) newTimer(_ time.Duration, f func()) Timer {
	t := &manualTimer{f: f}
	c.mu.Lock()
	c.timers[t] = struct{}{}
	c.mu.Unlock()
	return t
}

// fireAll 触发所有未停止、未触发的定时器。
func (c *manualClock) fireAll() {
	c.mu.Lock()
	var fs []func()
	for t := range c.timers {
		t.mu.Lock()
		if !t.fired && !t.stopped {
			t.fired = true
			fs = append(fs, t.f)
		}
		t.mu.Unlock()
	}
	c.mu.Unlock()
	for _, f := range fs {
		f()
	}
}

func (c *manualClock) pendingTimers() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for t := range c.timers {
		t.mu.Lock()
		if !t.fired && !t.stopped {
			n++
		}
		t.mu.Unlock()
	}
	return n
}

func (t *manualTimer) Stop() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.fired || t.stopped {
		return false
	}
	t.stopped = true
	return true
}

// recvResult 带超时地接收一个结果，防止实现死锁导致测试挂起。
func recvResult(t *testing.T, ch <-chan Result) Result {
	t.Helper()
	select {
	case r := <-ch:
		return r
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for result")
		return Result{}
	}
}

// waitFor 轮询等待条件成立。
func waitFor(t *testing.T, msg string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for: %s", msg)
		}
		time.Sleep(time.Millisecond)
	}
}

// echoExec 把每个 item 原样作为结果返回。
func echoExec(_ context.Context, items []any) ([]any, error) {
	out := make([]any, len(items))
	copy(out, items)
	return out, nil
}

// TestTriggerOnMaxSize 验证攒满 MaxSize 立即触发，且定时器被停止。
func TestTriggerOnMaxSize(t *testing.T) {
	clock := newManualClock()
	b := New(Options{MaxSize: 3, MaxWait: time.Minute, Exec: echoExec, NewTimer: clock.newTimer})

	ch1 := b.Submit(context.Background(), "a")
	ch2 := b.Submit(context.Background(), "b")
	if got := clock.pendingTimers(); got != 1 {
		t.Fatalf("expected 1 pending timer after first submit, got %d", got)
	}
	ch3 := b.Submit(context.Background(), "c")

	for i, ch := range []<-chan Result{ch1, ch2, ch3} {
		r := recvResult(t, ch)
		if r.Err != nil {
			t.Fatalf("result %d: unexpected error: %v", i, r.Err)
		}
		want := string(rune('a' + i))
		if r.Value != want {
			t.Fatalf("result %d: got %v, want %v", i, r.Value, want)
		}
	}
	// 满批触发后定时器应被停止，fireAll 不应再产生任何批次。
	if got := clock.pendingTimers(); got != 0 {
		t.Fatalf("expected timer stopped after size trigger, %d still pending", got)
	}
}

// TestTriggerOnTimeout 验证批未满但到时必须触发。
func TestTriggerOnTimeout(t *testing.T) {
	clock := newManualClock()
	var mu sync.Mutex
	var batches [][]any
	b := New(Options{
		MaxSize: 10,
		MaxWait: 50 * time.Millisecond,
		Exec: func(ctx context.Context, items []any) ([]any, error) {
			mu.Lock()
			batches = append(batches, append([]any(nil), items...))
			mu.Unlock()
			return echoExec(ctx, items)
		},
		NewTimer: clock.newTimer,
	})

	ch1 := b.Submit(context.Background(), "x")
	ch2 := b.Submit(context.Background(), "y")

	// 批未满（2 < 10），推进时间触发超时。
	clock.fireAll()

	for i, ch := range []<-chan Result{ch1, ch2} {
		r := recvResult(t, ch)
		if r.Err != nil {
			t.Fatalf("result %d: unexpected error: %v", i, r.Err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(batches) != 1 || len(batches[0]) != 2 {
		t.Fatalf("expected one batch of 2 items, got %v", batches)
	}
}

// TestCancelledItemRemovedFromBatch 验证取消项从批中移除，不进入执行函数。
func TestCancelledItemRemovedFromBatch(t *testing.T) {
	clock := newManualClock()
	var mu sync.Mutex
	var gotItems []any
	b := New(Options{
		MaxSize: 5,
		MaxWait: time.Second,
		Exec: func(ctx context.Context, items []any) ([]any, error) {
			mu.Lock()
			gotItems = append([]any(nil), items...)
			mu.Unlock()
			return echoExec(ctx, items)
		},
		NewTimer: clock.newTimer,
	})

	ch1 := b.Submit(context.Background(), "keep-1")
	ctx2, cancel2 := context.WithCancel(context.Background())
	ch2 := b.Submit(ctx2, "drop-me")
	ch3 := b.Submit(context.Background(), "keep-2")

	cancel2()
	clock.fireAll()

	r2 := recvResult(t, ch2)
	if !errors.Is(r2.Err, context.Canceled) {
		t.Fatalf("cancelled item: got err %v, want context.Canceled", r2.Err)
	}
	for i, ch := range []<-chan Result{ch1, ch3} {
		r := recvResult(t, ch)
		if r.Err != nil {
			t.Fatalf("survivor %d: unexpected error: %v", i, r.Err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(gotItems) != 2 || gotItems[0] != "keep-1" || gotItems[1] != "keep-2" {
		t.Fatalf("exec should see only surviving items, got %v", gotItems)
	}
}

// TestAllCancelledSkipsExec 验证全批取消时不调用执行函数。
func TestAllCancelledSkipsExec(t *testing.T) {
	clock := newManualClock()
	execCalls := make(chan struct{}, 1)
	b := New(Options{
		MaxSize: 4,
		MaxWait: time.Second,
		Exec: func(ctx context.Context, items []any) ([]any, error) {
			execCalls <- struct{}{}
			return echoExec(ctx, items)
		},
		NewTimer: clock.newTimer,
	})

	ctx1, cancel1 := context.WithCancel(context.Background())
	ctx2, cancel2 := context.WithCancel(context.Background())
	ch1 := b.Submit(ctx1, "a")
	ch2 := b.Submit(ctx2, "b")

	cancel1()
	cancel2()
	clock.fireAll()

	for i, ch := range []<-chan Result{ch1, ch2} {
		r := recvResult(t, ch)
		if !errors.Is(r.Err, context.Canceled) {
			t.Fatalf("result %d: got err %v, want context.Canceled", i, r.Err)
		}
	}
	select {
	case <-execCalls:
		t.Fatal("exec must not be called when the whole batch is cancelled")
	case <-time.After(100 * time.Millisecond):
	}
}

// TestExecErrorMappedToEverySurvivor 验证批执行失败映射到每个存活请求。
func TestExecErrorMappedToEverySurvivor(t *testing.T) {
	clock := newManualClock()
	boom := errors.New("boom")
	b := New(Options{
		MaxSize:  3,
		MaxWait:  time.Second,
		Exec:     func(context.Context, []any) ([]any, error) { return nil, boom },
		NewTimer: clock.newTimer,
	})

	ctx1, cancel1 := context.WithCancel(context.Background())
	ch1 := b.Submit(ctx1, "doomed-anyway")
	ch2 := b.Submit(context.Background(), "x")
	ch3 := b.Submit(context.Background(), "y")

	cancel1() // 取消项在封存前被移除，不收到 boom。
	clock.fireAll()

	r1 := recvResult(t, ch1)
	if !errors.Is(r1.Err, context.Canceled) {
		t.Fatalf("cancelled item: got err %v, want context.Canceled", r1.Err)
	}
	for i, ch := range []<-chan Result{ch2, ch3} {
		r := recvResult(t, ch)
		if !errors.Is(r.Err, boom) {
			t.Fatalf("survivor %d: got err %v, want boom", i, r.Err)
		}
	}
}

// TestSubmitDuringExecutionNotMixedIntoSealedBatch 验证执行中新增项进入下一批次。
func TestSubmitDuringExecutionNotMixedIntoSealedBatch(t *testing.T) {
	clock := newManualClock()
	entered := make(chan []any, 2)
	release := make(chan struct{})
	var mu sync.Mutex
	var batches [][]any

	b := New(Options{
		MaxSize: 2,
		MaxWait: time.Second,
		Exec: func(_ context.Context, items []any) ([]any, error) {
			cp := append([]any(nil), items...)
			mu.Lock()
			batches = append(batches, cp)
			mu.Unlock()
			entered <- cp
			<-release // 阻塞执行，模拟执行期间有新提交到达
			return echoExec(context.Background(), items)
		},
		NewTimer: clock.newTimer,
	})

	ch1 := b.Submit(context.Background(), "a")
	ch2 := b.Submit(context.Background(), "b") // 满 2 个，封存第一批

	first := <-entered // 等待第一批开始执行

	// 执行期间提交新请求：必须进入新批次，而不是混入已封存批次。
	ch3 := b.Submit(context.Background(), "c")
	clock.fireAll() // 触发第二批超时

	second := <-entered
	close(release)

	for i, ch := range []<-chan Result{ch1, ch2, ch3} {
		r := recvResult(t, ch)
		if r.Err != nil {
			t.Fatalf("result %d: unexpected error: %v", i, r.Err)
		}
	}
	if len(first) != 2 || first[0] != "a" || first[1] != "b" {
		t.Fatalf("first batch = %v, want [a b]", first)
	}
	if len(second) != 1 || second[0] != "c" {
		t.Fatalf("second batch = %v, want [c] (new item must not join sealed batch)", second)
	}
}

// TestResultCountMismatch 验证执行函数返回数量不符时每个请求收到错误。
func TestResultCountMismatch(t *testing.T) {
	clock := newManualClock()
	b := New(Options{
		MaxSize:  2,
		MaxWait:  time.Second,
		Exec:     func(context.Context, []any) ([]any, error) { return []any{"only-one"}, nil },
		NewTimer: clock.newTimer,
	})
	ch1 := b.Submit(context.Background(), "a")
	ch2 := b.Submit(context.Background(), "b")
	for i, ch := range []<-chan Result{ch1, ch2} {
		r := recvResult(t, ch)
		if !errors.Is(r.Err, ErrResultCountMismatch) {
			t.Fatalf("result %d: got err %v, want ErrResultCountMismatch", i, r.Err)
		}
	}
}

// TestConcurrentSubmitRace 并发提交压力测试，配合 -race 验证数据竞争。
func TestConcurrentSubmitRace(t *testing.T) {
	clock := newManualClock()
	var execCount atomic.Int64
	b := New(Options{
		MaxSize: 8,
		MaxWait: time.Millisecond,
		Exec: func(ctx context.Context, items []any) ([]any, error) {
			execCount.Add(1)
			return echoExec(ctx, items)
		},
		NewTimer: clock.newTimer,
	})

	const workers = 32
	const perWorker = 50
	results := make(chan Result, workers*perWorker)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				ctx := context.Background()
				if i%7 == 0 { // 一部分请求带取消，交叉取消路径
					var cancel context.CancelFunc
					ctx, cancel = context.WithCancel(ctx)
					if i%14 == 0 {
						cancel()
					} else {
						defer cancel()
					}
				}
				ch := b.Submit(ctx, fmt.Sprintf("w%d-%d", id, i))
				results <- recvResult(t, ch)
			}
		}(w)
	}
	// 持续拨动时钟，触发超时批次。
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-done:
				return
			default:
				clock.fireAll()
			}
		}
	}()
	wg.Wait()
	close(done)
	close(results)

	var errs int
	for r := range results {
		if r.Err != nil && !errors.Is(r.Err, context.Canceled) {
			errs++
		}
	}
	if errs != 0 {
		t.Fatalf("unexpected %d non-cancellation errors", errs)
	}
	if execCount.Load() == 0 {
		t.Fatal("exec was never called")
	}
}

// TestCloseFlushesPending 验证 Close 封存未满批次并拒绝后续提交。
func TestCloseFlushesPending(t *testing.T) {
	clock := newManualClock()
	b := New(Options{MaxSize: 10, MaxWait: time.Minute, Exec: echoExec, NewTimer: clock.newTimer})
	ch := b.Submit(context.Background(), "last")
	b.Close()
	r := recvResult(t, ch)
	if r.Err != nil || r.Value != "last" {
		t.Fatalf("got %+v, want value last", r)
	}
	r2 := recvResult(t, b.Submit(context.Background(), "too-late"))
	if !errors.Is(r2.Err, ErrClosed) {
		t.Fatalf("got err %v, want ErrClosed", r2.Err)
	}
}
