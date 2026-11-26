package retry

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func testCfg() Config {
	return Config{
		MaxAttempts:         4,
		BudgetRatio:         0.2,
		MinRetryConcurrency: 1,
		Window:              time.Second,
		BaseBackoff:         time.Millisecond,
		BackoffMultiplier:   2,
		Jitter:              0.1,
	}
}

// 预算公式：allowedRetries = max(minRetryConcurrency, floor(ratio*active))。
func TestAllowedRetriesFormula(t *testing.T) {
	e := NewEdge(Config{BudgetRatio: 0.2, MinRetryConcurrency: 2, Window: time.Second})
	if got := e.AllowedRetries(); got != 2 {
		t.Fatalf("空窗口保底: got %d want 2", got)
	}
	ids := make([]uint64, 10)
	for i := range ids {
		ids[i] = e.begin()
	}
	if got := e.AllowedRetries(); got != 2 { // floor(0.2*10)=2，保底仍为 2
		t.Fatalf("10 活跃: got %d want 2", got)
	}
	for i := 0; i < 5; i++ {
		e.begin()
	}
	if got := e.AllowedRetries(); got != 3 { // floor(0.2*15)=3 > 保底 2
		t.Fatalf("15 活跃: got %d want 3", got)
	}
}

// 无预算（Unlimited）时重试会把失败请求放大到 MaxAttempts 倍。
func TestUnboundedRetriesAmplify(t *testing.T) {
	cfg := testCfg()
	cfg.BudgetRatio = 0
	cfg.MinRetryConcurrency = 100
	e := NewEdge(cfg)
	var calls int32
	res := e.Do(context.Background(), "req-1", time.Time{}, func(ctx context.Context) error {
		atomic.AddInt32(&calls, 1)
		return errors.New("boom")
	})
	if res.Success {
		t.Fatal("应全部失败")
	}
	if calls != 4 {
		t.Fatalf("无预算应尝试 4 次, got %d", calls)
	}
}

// 有预算时额外尝试被限制，放大倍率被压住。
func TestBudgetCapsRetries(t *testing.T) {
	cfg := testCfg()
	cfg.MinRetryConcurrency = 0
	cfg.BudgetRatio = 0.2
	e := NewEdge(cfg)

	// 单请求窗口：floor(0.2*1)=0，且无保底 => 首次失败后不得重试。
	var calls int32
	res := e.Do(context.Background(), "req-budget", time.Time{}, func(ctx context.Context) error {
		atomic.AddInt32(&calls, 1)
		return errors.New("boom")
	})
	if calls != 1 {
		t.Fatalf("预算为 0 时只允许首次尝试, got %d", calls)
	}
	if res.Reject != RejectBudget || res.LastErr != ErrBudgetExhausted {
		t.Fatalf("应返回预算耗尽, got %v", res.Reject)
	}
}

// 截止期不足：重试前剩余时间 <= 退避 + 最小处理耗时时提前截断。
func TestDeadlineTruncatesRetry(t *testing.T) {
	cfg := testCfg()
	cfg.MinRetryConcurrency = 100 // 预算充足，确保是截止期在起作用
	cfg.MinProcessingTime = 5 * time.Millisecond
	cfg.BaseBackoff = 2 * time.Millisecond
	e := NewEdge(cfg)

	var calls int32
	deadline := time.Now().Add(3 * time.Millisecond) // 小于 backoff+minProc
	res := e.Do(context.Background(), "req-dl", deadline, func(ctx context.Context) error {
		atomic.AddInt32(&calls, 1)
		return errors.New("boom")
	})
	if calls != 1 {
		t.Fatalf("截止期不足应只发首次, got %d", calls)
	}
	if !res.DeadlineRejected || res.Reject != RejectDeadline {
		t.Fatalf("应标记截止期截断, got %+v", res)
	}

	// 充足截止期下应允许重试。
	e2 := NewEdge(cfg)
	var calls2 int32
	res2 := e2.Do(context.Background(), "req-dl2", time.Now().Add(time.Second),
		func(ctx context.Context) error {
			if atomic.AddInt32(&calls2, 1) >= 3 {
				return nil
			}
			return errors.New("boom")
		})
	if !res2.Success || calls2 != 3 {
		t.Fatalf("充足截止期应重试到成功, success=%v calls=%d", res2.Success, calls2)
	}
}

// minRetryConcurrency 低流量保底：窗口只有 1 个请求也允许保底数量的重试。
func TestMinRetryConcurrencyFloor(t *testing.T) {
	cfg := testCfg()
	cfg.BudgetRatio = 0.0 // 比例预算为 0
	cfg.MinRetryConcurrency = 2
	e := NewEdge(cfg)
	var calls int32
	res := e.Do(context.Background(), "req-min", time.Time{}, func(ctx context.Context) error {
		atomic.AddInt32(&calls, 1)
		return errors.New("boom")
	})
	if calls != 3 { // 首次 + 2 次保底重试，第 4 次尝试被预算拒绝
		t.Fatalf("保底 2 应共尝试 3 次, got %d", calls)
	}
	if res.Reject != RejectBudget {
		t.Fatalf("保底用尽后应被预算拒绝, got %v", res.Reject)
	}
}

// 对冲竞速：首个成功立即取消其余在途分支。
func TestHedgeRaceCancellation(t *testing.T) {
	cfg := testCfg()
	cfg.HedgeDelay = 20 * time.Millisecond
	cfg.MinRetryConcurrency = 100
	e := NewEdge(cfg)

	var inFlight int32
	var maxFlight int32
	var done int32
	res := e.Do(context.Background(), "req-hedge", time.Time{}, func(ctx context.Context) error {
		cur := atomic.AddInt32(&inFlight, 1)
		for old := atomic.LoadInt32(&maxFlight); cur > old; old = atomic.LoadInt32(&maxFlight) {
			if atomic.CompareAndSwapInt32(&maxFlight, old, cur) {
				break
			}
		}
		defer atomic.AddInt32(&inFlight, -1)
		select {
		case <-time.After(80 * time.Millisecond):
			atomic.AddInt32(&done, 1)
			return nil
		case <-ctx.Done():
			return ctx.Err() // 被竞速取消
		}
	})
	if !res.Success {
		t.Fatalf("对冲请求应成功, err=%v", res.LastErr)
	}
	if mf := atomic.LoadInt32(&maxFlight); mf < 2 {
		t.Fatalf("应观察到至少 2 个在途对冲分支, max=%d", mf)
	}
	if d := atomic.LoadInt32(&done); d != 1 {
		t.Fatalf("只有一个分支应真正跑完, done=%d", d)
	}
	if res.Hedges < 1 {
		t.Fatalf("应至少发出一个对冲分支, got %d", res.Hedges)
	}
}

// 确定性抖动：相同 requestID/attempt 产生相同退避，不同 ID 产生不同序列。
func TestDeterministicJitter(t *testing.T) {
	base := 10 * time.Millisecond
	a := Backoff(base, 2, 0.5, "id-A", 2)
	b := Backoff(base, 2, 0.5, "id-A", 2)
	c := Backoff(base, 2, 0.5, "id-B", 2)
	if a != b {
		t.Fatal("相同输入退避必须确定一致")
	}
	if a == c {
		t.Fatal("不同 requestID 应以高概率产生不同抖动")
	}
	noJitter := Backoff(base, 2, 0, "id-A", 2)
	if noJitter != 2*base {
		t.Fatalf("关闭抖动时应为纯指数退避, got %v", noJitter)
	}
}

// 熔断器：连续故障达到阈值后 Open，快速失败；冷却后 HalfOpen 探测成功则 Closed。
func TestCircuitBreakerLifecycle(t *testing.T) {
	b := NewBreaker(BreakerConfig{
		FailureThreshold:      3,
		OpenCoolDown:          20 * time.Millisecond,
		HalfOpenMaxConcurrent: 1,
	})
	for i := 0; i < 3; i++ {
		b.OnFailure()
	}
	if b.State() != BreakerOpen {
		t.Fatalf("3 次连续故障后应 Open, got %v", b.State())
	}
	if b.Allow() {
		t.Fatal("Open 态必须拒绝请求")
	}
	time.Sleep(25 * time.Millisecond)
	if b.State() != BreakerHalfOpen {
		t.Fatalf("冷却后应 HalfOpen, got %v", b.State())
	}
	if !b.Allow() {
		t.Fatal("HalfOpen 应放行探测请求")
	}
	b.OnSuccess()
	if b.State() != BreakerClosed {
		t.Fatalf("探测成功应回到 Closed, got %v", b.State())
	}
}

// 并发安全冒烟：多个 goroutine 同时跑请求不出现竞态（配合 -race）。
func TestConcurrentDo(t *testing.T) {
	cfg := testCfg()
	cfg.MinRetryConcurrency = 5
	e := NewEdge(cfg)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_ = e.Do(context.Background(), "c", time.Time{}, func(ctx context.Context) error {
				return errors.New("x")
			})
		}(i)
	}
	wg.Wait()
}

// 对冲在首次请求慢（但最终会成功）时触发：首个返回的成功分支取消其余分支。
func TestHedgeFastSuccessCancelsSlowPrimary(t *testing.T) {
	cfg := testCfg()
	cfg.HedgeDelay = 5 * time.Millisecond
	cfg.MinRetryConcurrency = 100
	e := NewEdge(cfg)

	var started int32
	res := e.Do(context.Background(), "req-hedge2", time.Time{}, func(ctx context.Context) error {
		n := atomic.AddInt32(&started, 1)
		if n == 1 {
			// 首尝试慢：等到被对冲分支取消或超时。
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(200 * time.Millisecond):
				return nil
			}
		}
		return nil // 对冲分支立即成功
	})
	if !res.Success {
		t.Fatalf("对冲分支应带来成功, err=%v", res.LastErr)
	}
	if atomic.LoadInt32(&started) != 2 {
		t.Fatalf("应恰好发出首尝试+1 个对冲分支, got %d", started)
	}
	if res.Hedges != 1 || res.Attempts != 2 {
		t.Fatalf("hedges=%d attempts=%d", res.Hedges, res.Attempts)
	}
}

// Open 熔断期间 Do 立即快速失败，不发起任何下游调用。
func TestDoFailsFastWhenBreakerOpen(t *testing.T) {
	cfg := testCfg()
	cfg.Breaker = BreakerConfig{FailureThreshold: 1, OpenCoolDown: time.Hour}
	e := NewEdge(cfg)
	e.breaker.OnFailure() // 直接打到 Open
	var calls int32
	res := e.Do(context.Background(), "req-open", time.Time{}, func(ctx context.Context) error {
		atomic.AddInt32(&calls, 1)
		return nil
	})
	if calls != 0 {
		t.Fatal("Open 态不得发起调用")
	}
	if res.Reject != RejectBreaker || res.LastErr != ErrBreakerOpen {
		t.Fatalf("应快速失败, got %v", res.Reject)
	}
}
