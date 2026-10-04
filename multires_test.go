package multires

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

const (
	blockWait  = 120 * time.Millisecond // 断言“未授权”的观察窗口
	grantWait  = 2 * time.Second        // 断言“已授权”的超时
	settleWait = 50 * time.Millisecond  // 等待异步 Acquire 入队
)

type acqResult struct {
	rel func()
	err error
}

func acquireAsync(p *Pool, ctx context.Context, d Demand) chan acqResult {
	ch := make(chan acqResult, 1)
	go func() {
		rel, err := p.Acquire(ctx, d)
		ch <- acqResult{rel, err}
	}()
	return ch
}

func mustAcquire(t *testing.T, p *Pool, d Demand) func() {
	t.Helper()
	rel, err := p.Acquire(context.Background(), d)
	if err != nil {
		t.Fatalf("Acquire%v: %v", d, err)
	}
	return rel
}

func mustNotGrant(t *testing.T, ch chan acqResult, what string) {
	t.Helper()
	select {
	case r := <-ch:
		t.Fatalf("%s: 意外被授权 (err=%v)", what, r.err)
	case <-time.After(blockWait):
	}
}

func mustGrant(t *testing.T, ch chan acqResult, what string) func() {
	t.Helper()
	select {
	case r := <-ch:
		if r.err != nil {
			t.Fatalf("%s: %v", what, r.err)
		}
		return r.rel
	case <-time.After(grantWait):
		t.Fatalf("%s: 等待授权超时", what)
		return nil
	}
}

// 全有或全无：资源不齐时，等待者不得部分占用任何资源。
func TestAllOrNothing(t *testing.T) {
	p := New(map[string]int64{"cpu": 1, "mem": 1})
	relMem := mustAcquire(t, p, Demand{"mem": 1})

	ch := acquireAsync(p, context.Background(), Demand{"cpu": 1, "mem": 1})
	time.Sleep(settleWait)

	s := p.Stats()
	if s.InUse["cpu"] != 0 {
		t.Fatalf("等待者部分占用了 cpu: InUse=%d", s.InUse["cpu"])
	}
	if s.Queued != 1 {
		t.Fatalf("Queued=%d, want 1", s.Queued)
	}
	mustNotGrant(t, ch, "cpu+mem")

	relMem()
	rel := mustGrant(t, ch, "cpu+mem")
	s = p.Stats()
	if s.InUse["cpu"] != 1 || s.InUse["mem"] != 1 {
		t.Fatalf("授权后占用不符: %+v", s.InUse)
	}
	rel()
	s = p.Stats()
	if s.InUse["cpu"] != 0 || s.InUse["mem"] != 0 {
		t.Fatalf("释放后占用未清零: %+v", s.InUse)
	}
}

// 队列次序优先可满足请求：不可满足的队首可被后面的可满足请求跳过（前 3 次）。
func TestSatisfiableRequestBypassesUnsatisfiable(t *testing.T) {
	p := New(map[string]int64{"cpu": 1, "mem": 5})
	relCPU := mustAcquire(t, p, Demand{"cpu": 1})
	defer relCPU()

	blocked := acquireAsync(p, context.Background(), Demand{"cpu": 1})
	time.Sleep(settleWait)
	mustNotGrant(t, blocked, "cpu 等待者")

	// 后来的、可满足的请求可以跳过队首。
	rel := mustAcquire(t, p, Demand{"mem": 1})
	rel()
	mustNotGrant(t, blocked, "cpu 等待者仍应阻塞")
}

// 队首累计被跳过 3 次后成为屏障：第 4 个可满足请求不得再跳过；
// 队首被满足后，后续请求恢复调度。
func TestHeadOfLineBarrierAfterThreeSkips(t *testing.T) {
	p := New(map[string]int64{"cpu": 1, "mem": 10})
	relCPU := mustAcquire(t, p, Demand{"cpu": 1})

	head := acquireAsync(p, context.Background(), Demand{"cpu": 1})
	time.Sleep(settleWait)

	// 三次跳过：均可满足，全部授权。
	hold := make([]func(), 0, 3)
	for i := 0; i < 3; i++ {
		hold = append(hold, mustAcquire(t, p, Demand{"mem": 1}))
	}
	defer func() {
		for _, r := range hold {
			r()
		}
	}()

	// 第 4 个可满足请求：队首已被跳过 3 次，屏障生效，不得授权。
	fourth := acquireAsync(p, context.Background(), Demand{"mem": 1})
	mustNotGrant(t, fourth, "屏障生效后第 4 个请求")

	// 释放队首所需资源：队首先被满足，随后第 4 个请求也被调度。
	relCPU()
	relHead := mustGrant(t, head, "屏障队首")
	relFourth := mustGrant(t, fourth, "屏障解除后的第 4 个请求")
	relHead()
	relFourth()
}

// 取消：等待者被取消后从队列移除，不占用资源，队列其余请求正常推进。
func TestCancelRemovesWaiter(t *testing.T) {
	p := New(map[string]int64{"cpu": 1})
	relCPU := mustAcquire(t, p, Demand{"cpu": 1})

	ctx, cancel := context.WithCancel(context.Background())
	first := acquireAsync(p, ctx, Demand{"cpu": 1})
	second := acquireAsync(p, context.Background(), Demand{"cpu": 1})
	time.Sleep(settleWait)

	cancel()
	select {
	case r := <-first:
		if r.err != context.Canceled {
			t.Fatalf("取消后 err=%v, want context.Canceled", r.err)
		}
	case <-time.After(grantWait):
		t.Fatal("取消未生效")
	}
	if s := p.Stats(); s.Queued != 1 {
		t.Fatalf("取消后 Queued=%d, want 1", s.Queued)
	}

	relCPU()
	rel := mustGrant(t, second, "取消后的队首")
	rel()
}

// 取消屏障队首：屏障移除后，被挡住的可满足请求立即恢复调度。
func TestCancelBarrierHeadResumesScheduling(t *testing.T) {
	p := New(map[string]int64{"cpu": 1, "mem": 5})
	relCPU := mustAcquire(t, p, Demand{"cpu": 1})
	defer relCPU()

	ctx, cancel := context.WithCancel(context.Background())
	head := acquireAsync(p, ctx, Demand{"cpu": 1})
	time.Sleep(settleWait)

	for i := 0; i < 3; i++ {
		mustAcquire(t, p, Demand{"mem": 1}) // 跳过队首 3 次，屏障生效
	}
	blocked := acquireAsync(p, context.Background(), Demand{"mem": 1})
	mustNotGrant(t, blocked, "屏障后的可满足请求")

	cancel()
	select {
	case r := <-head:
		if r.err != context.Canceled {
			t.Fatalf("head err=%v, want context.Canceled", r.err)
		}
	case <-time.After(grantWait):
		t.Fatal("队首取消未生效")
	}
	rel := mustGrant(t, blocked, "屏障解除后的请求")
	rel()
}

// 释放回调重入：回调在锁外执行，可安全地再次 Acquire/Release/Stats。
func TestReleaseCallbackReentrancy(t *testing.T) {
	p := New(map[string]int64{"cpu": 2})
	done := make(chan struct{})
	var calls int32

	p.SetOnRelease(func(d Demand) {
		n := atomic.AddInt32(&calls, 1)
		if n != 1 {
			return // 只重入一次，避免嵌套释放再次触发
		}
		_ = p.Stats()                                                 // 重入读取状态
		rel, err := p.Acquire(context.Background(), Demand{"cpu": 1}) // 重入领取
		if err != nil {
			t.Errorf("回调内重入 Acquire 失败: %v", err)
			return
		}
		rel() // 重入释放（会再次触发本回调，calls=2 直接返回）
		close(done)
	})

	rel := mustAcquire(t, p, Demand{"cpu": 2})
	rel() // 同步触发回调；若回调在锁内执行将死锁

	select {
	case <-done:
	case <-time.After(grantWait):
		t.Fatal("释放回调重入发生死锁")
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("回调触发次数=%d, want 2", got)
	}
	if s := p.Stats(); s.InUse["cpu"] != 0 {
		t.Fatalf("重入释放后 InUse=%d, want 0", s.InUse["cpu"])
	}
}

// 配额缩减：不抢占已持有资源，可用量可为负并阻止新授权，
// 持有者归还后占用回落，等待者才被满足；调大则立即唤醒等待者。
func TestQuotaShrink(t *testing.T) {
	p := New(map[string]int64{"cpu": 4})
	relBig := mustAcquire(t, p, Demand{"cpu": 3})

	p.SetQuota("cpu", 2) // 缩减到低于当前占用
	s := p.Stats()
	if s.Available["cpu"] != -1 {
		t.Fatalf("缩减后 Available=%d, want -1", s.Available["cpu"])
	}

	// 可用量为负：即使只领 1 个也必须阻塞。
	waiting := acquireAsync(p, context.Background(), Demand{"cpu": 1})
	mustNotGrant(t, waiting, "配额缩减后的领取")

	// 持有者归还，占用回落到配额内，等待者被满足。
	relBig()
	rel := mustGrant(t, waiting, "占用回落后的等待者")

	// 再次缩减把可用量压到 0，新领取阻塞；调大后立即唤醒。
	p.SetQuota("cpu", 1)
	waiting2 := acquireAsync(p, context.Background(), Demand{"cpu": 1})
	mustNotGrant(t, waiting2, "可用量为 0 时的领取")
	p.SetQuota("cpu", 3)
	rel2 := mustGrant(t, waiting2, "配额调大后的等待者")
	rel2()
	rel()
}

// 取消与授权竞争：ctx 已过期但请求恰好被授权时，按成功处理且不泄漏。
func TestCancelRacingGrantSucceeds(t *testing.T) {
	p := New(map[string]int64{"cpu": 1})
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 已取消，但队列空、资源可满足，授权与取消竞争
	rel, err := p.Acquire(ctx, Demand{"cpu": 1})
	if err != nil {
		t.Fatalf("已取消但可满足时应授权: %v", err)
	}
	rel()
	if s := p.Stats(); s.InUse["cpu"] != 0 {
		t.Fatalf("释放后 InUse=%d, want 0", s.InUse["cpu"])
	}
}

// 释放函数幂等：重复调用不重复归还。
func TestReleaseIdempotent(t *testing.T) {
	p := New(map[string]int64{"cpu": 1})
	rel := mustAcquire(t, p, Demand{"cpu": 1})
	rel()
	rel()
	if s := p.Stats(); s.InUse["cpu"] != 0 {
		t.Fatalf("重复释放后 InUse=%d, want 0", s.InUse["cpu"])
	}
	// 配额未被重复归还撑大：第二个领取只能拿到 1 个。
	rel2 := mustAcquire(t, p, Demand{"cpu": 1})
	waiting := acquireAsync(p, context.Background(), Demand{"cpu": 1})
	mustNotGrant(t, waiting, "配额被重复归还撑大")
	rel2()
	mustGrant(t, waiting, "归还后授权")
}
