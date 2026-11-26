package retry

import (
	"context"
	"time"
)

// Result 汇总一次逻辑请求的执行结果。
type Result struct {
	// Attempts 实际发出的尝试次数（含首次与对冲分支）。
	Attempts int
	// Success 是否在截止期内取得成功。
	Success bool
	// LastErr 最终失败时最后一次尝试的错误，或准入拒绝错误。
	LastErr error
	// DeadlineRejected 是否因剩余截止期不足而提前截断重试。
	DeadlineRejected bool
	// Reject 最后一次准入拒绝原因（若有）。
	Reject RejectReason
	// Hedges 实际发出的对冲分支数。
	Hedges int
}

type attemptOutcome struct {
	ok  bool
	err error
}

// Do 在边 e 上执行一个带重试与对冲的逻辑请求。
//
// ctx 用于外部取消；deadline 为该请求在本跳的绝对截止时间（零值表示不设截止期），
// requestID 用于生成确定性抖动序列。首次请求立即发出；若开启对冲且在 HedgeDelay
// 内未返回，则竞速发出对冲分支，任一分支成功即取消其余在途分支。普通重试在每次
// 失败后按指数退避 + 确定性抖动等待，并在发出前重新做预算/熔断/截止期准入。
func (e *Edge) Do(ctx context.Context, requestID string, deadline time.Time, call Caller) Result {
	id := e.begin()
	defer e.end(id)

	maxAttempts := e.cfg.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 1
	}

	res := Result{Reject: RejectNone}

	// 首次请求也要经过熔断器（Open 态快速失败，HalfOpen 占用探测名额）。
	if !e.breaker.Allow() {
		res.Reject = RejectBreaker
		res.LastErr = ErrBreakerOpen
		return res
	}

	attemptCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	outcomes := make(chan attemptOutcome, 16)
	inFlight := 1
	attempts := 1
	hedges := 0
	var lastErr error

	spawn := func(hedged bool) {
		go func() {
			err := call(attemptCtx)
			// 被竞速取消或外部取消的分支不上报，避免污染结果与熔断统计。
			if attemptCtx.Err() != nil {
				e.breaker.Release()
				return
			}
			e.recordOutcome(id, err == nil)
			e.breaker.Release()
			outcomes <- attemptOutcome{ok: err == nil, err: err}
		}()
		if hedged {
			hedges++
			e.noteExtra(true)
		} else {
			e.noteExtra(false)
		}
	}

	spawn(false)

	// timerC 非 nil 表示已武装下一次额外尝试的等待计时器。
	var timerC <-chan time.Time
	var timer *time.Timer
	armTimer := func(d time.Duration) {
		if timer != nil {
			timer.Stop()
		}
		timer = time.NewTimer(d)
		timerC = timer.C
	}
	stopTimer := func() {
		if timer != nil {
			timer.Stop()
			timerC = nil
		}
	}

	// scheduleNext 在一次失败后决定下一次额外尝试的类型与等待时长。
	// 返回 false 表示已达次数上限或无法安排。
	scheduleNext := func() bool {
		if attempts >= maxAttempts {
			return false
		}
		if inFlight > 0 {
			// 仍有在途尝试：只能发对冲（每跳至多一个对冲分支）。
			if e.cfg.HedgeDelay > 0 && hedges == 0 {
				armTimer(e.cfg.HedgeDelay)
				return true
			}
			return false // 等在途尝试返回后再决策
		}
		delay := Backoff(e.cfg.BaseBackoff, e.cfg.BackoffMultiplier, e.cfg.Jitter, requestID, attempts)
		armTimer(delay)
		return true
	}

	if !scheduleNext() {
		stopTimer()
	}

	for {
		select {
		case oc := <-outcomes:
			inFlight--
			stopTimer()
			if oc.ok {
				res.Success = true
				res.Attempts = attempts
				res.Hedges = hedges
				cancel() // 首个成功：取消其余在途对冲/重试分支
				return res
			}
			lastErr = oc.err
			if inFlight == 0 && attempts >= maxAttempts {
				res.LastErr = lastErr
				res.Attempts = attempts
				res.Hedges = hedges
				return res
			}
			if !scheduleNext() {
				// 还有在途尝试时等待其结果；否则终止。
				if inFlight == 0 {
					res.LastErr = lastErr
					res.Attempts = attempts
					res.Hedges = hedges
					return res
				}
			}
		case <-timerC:
			timerC = nil
			hedged := inFlight > 0 && hedges == 0 && e.cfg.HedgeDelay > 0
			wait := Backoff(e.cfg.BaseBackoff, e.cfg.BackoffMultiplier, e.cfg.Jitter, requestID, attempts)
			if hedged {
				wait = e.cfg.HedgeDelay
			}
			reason := e.admit(attempts+1, deadline, wait)
			if reason != RejectNone {
				// 准入被拒：若仍有在途尝试（对冲场景），等它们决出结果。
				for inFlight > 0 {
					select {
					case pending := <-outcomes:
						inFlight--
						if pending.ok {
							res.Success = true
							res.Attempts = attempts
							res.Hedges = hedges
							cancel()
							return res
						}
						lastErr = pending.err
					case <-ctx.Done():
						res.LastErr = ctx.Err()
						return res
					}
				}
				res.Reject = reason
				res.LastErr = reason.Error()
				if reason == RejectDeadline {
					res.DeadlineRejected = true
				}
				if reason == RejectMaxAttempts && lastErr != nil {
					res.LastErr = lastErr
				}
				res.Attempts = attempts
				res.Hedges = hedges
				return res
			}
			attempts++
			inFlight++
			spawn(hedged)
			if !scheduleNext() {
				stopTimer()
			}
		case <-ctx.Done():
			res.LastErr = ctx.Err()
			return res
		}
	}
}
