// Package retry 为服务网格代理提供逐跳（per-edge）的运行时重试准入与对冲控制，
// 包含滑动窗口重试预算、连续故障熔断器、剩余截止期传播、确定性抖动指数退避，
// 以及对冲请求（hedging）的竞速取消。
//
// 同一套语义在 steady（稳态不动点求解器）与 sim（离散事件仿真）包中以速率/
// 虚拟时间形式复现，保证运行时决策与稳态分析相互一致。
package retry

import (
	"context"
	"errors"
	"sync"
	"time"
)

// 决策器主动拒绝一次重试/对冲时返回的原因。
var (
	// ErrBudgetExhausted 滑动窗口重试预算已用尽。
	ErrBudgetExhausted = errors.New("retry: retry budget exhausted")
	// ErrBreakerOpen 熔断器处于 Open 态，快速失败。
	ErrBreakerOpen = errors.New("retry: circuit breaker open")
	// ErrDeadlineExceeded 剩余截止期不足以覆盖下游最小处理耗时与退避等待。
	ErrDeadlineExceeded = errors.New("retry: remaining deadline insufficient for another attempt")
	// ErrMaxAttempts 已达最大尝试次数。
	ErrMaxAttempts = errors.New("retry: max attempts reached")
)

// RejectReason 是准入拒绝的结构化原因，便于指标上报。
type RejectReason int

const (
	RejectNone RejectReason = iota
	RejectBudget
	RejectBreaker
	RejectDeadline
	RejectMaxAttempts
)

// Error 将拒绝原因映射为哨兵错误。
func (r RejectReason) Error() error {
	switch r {
	case RejectBudget:
		return ErrBudgetExhausted
	case RejectBreaker:
		return ErrBreakerOpen
	case RejectDeadline:
		return ErrDeadlineExceeded
	case RejectMaxAttempts:
		return ErrMaxAttempts
	default:
		return nil
	}
}

// Config 是一条边上的重试/对冲/预算/熔断/截止期配置。
type Config struct {
	// MaxAttempts 最大尝试次数（含首次），<=0 视为 1（不重试）。
	MaxAttempts int
	// BudgetRatio 重试预算比例：额外尝试并发不得超过 floor(BudgetRatio*活跃常规流量)。
	BudgetRatio float64
	// MinRetryConcurrency 低流量保底：无论窗口多小至少允许的在途重试数。
	MinRetryConcurrency int
	// Window 滑动窗口长度，统计窗口内的常规请求与在途重试。
	Window time.Duration
	// BaseBackoff 指数退避基数。
	BaseBackoff time.Duration
	// BackoffMultiplier 退避乘数，<=0 时按 2 处理。
	BackoffMultiplier float64
	// Jitter 确定性全抖动因子（相对退避时长的比例，0 表示关闭抖动）。
	Jitter float64
	// HedgeDelay 对冲延迟：首次请求发出后经过该时长仍未返回则发对冲分支；<=0 关闭对冲。
	HedgeDelay time.Duration
	// MinProcessingTime 下游最小处理耗时；剩余截止期必须大于它加退避等待才允许下一次尝试。
	MinProcessingTime time.Duration
	// Breaker 连续故障熔断器配置。
	Breaker BreakerConfig
}

// Caller 执行一次实际的下游调用。返回的 error 非 nil 视为失败（可重试）。
// ctx 会在对冲竞速中被取消，实现需尊重 ctx.Done()。
type Caller func(ctx context.Context) error

type windowEntry struct {
	id      uint64
	start   time.Time
	retries int
}

// Edge 是一条有向边的线程安全运行时控制器。
type Edge struct {
	cfg Config
	now func() time.Time

	mu       sync.Mutex
	entries  []windowEntry
	seq      uint64
	inflight int // 当前在途的额外尝试（重试 + 对冲）并发
	breaker  *Breaker

	// 累计指标。
	statRequests   int64
	statRetries    int64
	statHedges     int64
	statRejections map[RejectReason]int64
}

// NewEdge 构造边控制器。
func NewEdge(cfg Config) *Edge {
	if cfg.Window <= 0 {
		cfg.Window = 10 * time.Second
	}
	return &Edge{
		cfg:            cfg,
		now:            time.Now,
		breaker:        NewBreaker(cfg.Breaker),
		statRejections: map[RejectReason]int64{},
	}
}

// withClock 注入时钟（仿真/确定性测试使用）。
func (e *Edge) withClock(now func() time.Time) *Edge {
	e.now = now
	e.breaker.now = now
	return e
}

func (e *Edge) evictLocked(now time.Time) {
	cutoff := now.Add(-e.cfg.Window)
	kept := e.entries[:0]
	for _, en := range e.entries {
		if en.start.After(cutoff) {
			kept = append(kept, en)
		}
	}
	e.entries = kept
}

// ActiveRequests 返回滑动窗口内仍被计入的常规请求数（每个逻辑请求计 1）。
func (e *Edge) ActiveRequests() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.evictLocked(e.now())
	return len(e.entries)
}

// InFlightRetries 返回当前在途的额外尝试（重试与对冲）并发。
func (e *Edge) InFlightRetries() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.inflight
}

// AllowedRetries 实现预算公式：
//
//	allowedRetries = max(MinRetryConcurrency, floor(BudgetRatio * activeRequests))
func (e *Edge) AllowedRetries() int {
	active := e.ActiveRequests()
	ratio := e.cfg.BudgetRatio
	if ratio < 0 {
		ratio = 0
	}
	budget := int(ratio * float64(active))
	if budget < e.cfg.MinRetryConcurrency {
		budget = e.cfg.MinRetryConcurrency
	}
	return budget
}

// Breaker 暴露熔断器以读取状态。
func (e *Edge) Breaker() *Breaker { return e.breaker }

// begin 在窗口内登记一个新的逻辑请求，返回其窗口内 ID。
func (e *Edge) begin() uint64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.evictLocked(e.now())
	e.seq++
	e.entries = append(e.entries, windowEntry{id: e.seq, start: e.now()})
	e.statRequests++
	return e.seq
}

func (e *Edge) removeEntryLocked(id uint64) {
	for i := range e.entries {
		if e.entries[i].id == id {
			e.entries = append(e.entries[:i], e.entries[i+1:]...)
			return
		}
	}
}

// end 移除窗口登记（请求完成后调用）。
func (e *Edge) end(id uint64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.removeEntryLocked(id)
}

// admit 在发起额外尝试前做预算与熔断准入。通过则占用一个在途重试名额。
// attempt 为即将发起的尝试序号（1 表示首次之后的第一次额外尝试）。
func (e *Edge) admit(attempt int, deadline time.Time, backoff time.Duration) RejectReason {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.cfg.MaxAttempts > 0 && attempt > e.cfg.MaxAttempts {
		e.statRejections[RejectMaxAttempts]++
		return RejectMaxAttempts
	}

	e.evictLocked(e.now())
	allowed := e.cfg.MinRetryConcurrency
	if b := int(e.cfg.BudgetRatio * float64(len(e.entries))); b > allowed {
		allowed = b
	}
	if e.inflight >= allowed {
		e.statRejections[RejectBudget]++
		return RejectBudget
	}

	if !e.breaker.Allow() {
		e.statRejections[RejectBreaker]++
		return RejectBreaker
	}

	// 剩余截止期必须覆盖退避等待 + 下游最小处理耗时，否则再试也是无用功。
	if !deadline.IsZero() {
		remaining := deadline.Sub(e.now())
		if remaining <= backoff+e.cfg.MinProcessingTime {
			e.statRejections[RejectDeadline]++
			return RejectDeadline
		}
	}

	e.inflight++
	return RejectNone
}

// release 归还一个在途额外尝试名额（未真正发出或尝试结束时调用）。
func (e *Edge) release() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.inflight > 0 {
		e.inflight--
	}
}

// noteExtra 在额外尝试（重试或对冲分支）真正发出时计数。
func (e *Edge) noteExtra(hedged bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if hedged {
		e.statHedges++
	} else {
		e.statRetries++
	}
}

// recordOutcome 反馈一次尝试结果给熔断器，并累计该逻辑请求消耗的尝试数。
func (e *Edge) recordOutcome(id uint64, ok bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for i := range e.entries {
		if e.entries[i].id == id {
			e.entries[i].retries++
			break
		}
	}
	if ok {
		e.breaker.OnSuccess()
	} else {
		e.breaker.OnFailure()
	}
}

// Stats 是边的累计运行时指标快照。
type Stats struct {
	Requests       int64
	Retries        int64
	Hedges         int64
	InFlight       int64
	ActiveRequests int64
	BreakerState   BreakerState
	Rejections     map[RejectReason]int64
}

// Stats 返回指标快照。
func (e *Edge) Stats() Stats {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.evictLocked(e.now())
	rejs := make(map[RejectReason]int64, len(e.statRejections))
	for k, v := range e.statRejections {
		rejs[k] = v
	}
	return Stats{
		Requests:       e.statRequests,
		Retries:        e.statRetries,
		Hedges:         e.statHedges,
		InFlight:       int64(e.inflight),
		ActiveRequests: int64(len(e.entries)),
		BreakerState:   e.breaker.State(),
		Rejections:     rejs,
	}
}
