package retry

import (
	"sync"
	"sync/atomic"
	"time"
)

// AbortReason explains why an attempt sequence was stopped.
type AbortReason int

// Abort reasons returned by DecideRetry / DecideHedge.
const (
	AbortNone            AbortReason = iota
	AbortMaxAttempts                 // configured retry/hedge count exhausted
	AbortBudgetExhausted             // retry budget refused the slot
	AbortDeadline                    // remaining deadline cannot cover the next attempt
	AbortBreakerOpen                 // circuit breaker refused traffic
	AbortNonRetryable                // failure was flagged non-retryable
)

// String renders an AbortReason for logs and test assertions.
func (a AbortReason) String() string {
	switch a {
	case AbortMaxAttempts:
		return "MaxAttempts"
	case AbortBudgetExhausted:
		return "BudgetExhausted"
	case AbortDeadline:
		return "Deadline"
	case AbortBreakerOpen:
		return "BreakerOpen"
	case AbortNonRetryable:
		return "NonRetryable"
	default:
		return "None"
	}
}

// EdgeConfig configures retry behaviour on one directed edge.
type EdgeConfig struct {
	// MaxAttempts is the total attempts per call (initial attempt included);
	// the classic "3 retries" setup uses MaxAttempts = 4.
	MaxAttempts int
	// MaxHedges is the number of extra hedged branches racing the initial
	// attempt; 0 disables hedging.
	MaxHedges int
	// HedgeDelay is how long to wait before firing the first hedged branch;
	// further branches are spaced by the same delay.
	HedgeDelay time.Duration
	// MinProcessing is the downstream's minimum processing time used in the
	// deadline pre-check.
	MinProcessing time.Duration
	Backoff       BackoffConfig
	Budget        RetryBudgetConfig
	Breaker       BreakerConfig
}

// AttemptDecision is the result of asking for another attempt.
type AttemptDecision struct {
	Allowed  bool
	Wait     time.Duration // backoff/hedge delay to wait before dispatching
	IsHedge  bool
	Reason   AbortReason
	Attempt  int // 1-based index of the attempt to dispatch
	Deadline time.Time
}

// EdgeStats is a point-in-time snapshot of edge counters.
type EdgeStats struct {
	Calls           uint64
	Successes       uint64
	Failures        uint64
	Retries         uint64
	Hedges          uint64
	RejectedBudget  uint64
	RejectedBreaker uint64
	DeadlineAborts  uint64
	BreakerState    BreakerState
	RetryInFlight   int
	ActiveNormal    int
	WindowCount     int
	AllowedRetries  int
}

// EdgeController is the per-edge runtime decision maker. All methods are
// safe for concurrent use; a single controller instance is shared by every
// call traversing the edge.
type EdgeController struct {
	cfg     EdgeConfig
	clock   Clock
	budget  *RetryBudget
	breaker *CircuitBreaker

	mu      sync.Mutex
	callSeq uint64

	calls           atomic.Uint64
	successes       atomic.Uint64
	failures        atomic.Uint64
	retries         atomic.Uint64
	hedges          atomic.Uint64
	rejectedBudget  atomic.Uint64
	rejectedBreaker atomic.Uint64
	deadlineAborts  atomic.Uint64
}

// NewEdgeController builds a controller for cfg using clock for every time
// read (inject SimScheduler in tests and the DES).
func NewEdgeController(cfg EdgeConfig, clock Clock) *EdgeController {
	if cfg.MaxAttempts < 1 {
		cfg.MaxAttempts = 1
	}
	if cfg.MaxHedges < 0 {
		cfg.MaxHedges = 0
	}
	return &EdgeController{
		cfg:     cfg,
		clock:   clock,
		budget:  NewRetryBudget(cfg.Budget),
		breaker: NewCircuitBreaker(cfg.Breaker),
	}
}

// Budget exposes the retry budget (used by tests and the DES).
func (e *EdgeController) Budget() *RetryBudget { return e.budget }

// Breaker exposes the circuit breaker.
func (e *EdgeController) Breaker() *CircuitBreaker { return e.breaker }

// Config returns the edge configuration.
func (e *EdgeController) Config() EdgeConfig { return e.cfg }

// BeginCall starts one logical call at now under deadline. It records the
// normal attempt, checks the breaker and returns the call ID plus the
// attempt index to dispatch (always 1). If the breaker is open the call is
// rejected up-front with AbortBreakerOpen.
func (e *EdgeController) BeginCall(now time.Time, deadline Deadline) (callID uint64, decision AttemptDecision) {
	e.calls.Add(1)
	e.mu.Lock()
	e.callSeq++
	callID = e.callSeq
	e.mu.Unlock()

	if !e.breaker.Allow(now) {
		e.rejectedBreaker.Add(1)
		return callID, AttemptDecision{Allowed: false, Reason: AbortBreakerOpen, Attempt: 1}
	}
	e.breaker.ProbeStarted()
	e.budget.NormalStarted(now)
	return callID, AttemptDecision{
		Allowed:  true,
		Attempt:  1,
		IsHedge:  false,
		Deadline: deadline.End,
	}
}

// AttemptFinished releases the bookkeeping of an attempt. retryClass must be
// true for retries/hedges (releases a budget slot) and false for the initial
// attempt (releases the normal counter). success drives the breaker; the
// breaker is only touched once per logical call sequence through
// RecordCallOutcome, so pass updateBreaker=false for branch outcomes and
// true only for the final outcome.
func (e *EdgeController) AttemptFinished(attempt int, retryClass, success, updateBreaker bool) {
	if retryClass {
		e.budget.Release()
	} else {
		e.budget.NormalEnded()
	}
	if updateBreaker {
		if success {
			e.breaker.RecordSuccess()
		} else {
			e.breaker.RecordFailure(e.clock.Now())
		}
		e.breaker.ProbeFinished()
	}
}

// RecordCallOutcome updates success/failure counters once per logical call.
func (e *EdgeController) RecordCallOutcome(success bool) {
	if success {
		e.successes.Add(1)
	} else {
		e.failures.Add(1)
	}
}

// CancelAttempt releases all bookkeeping held by an attempt that was
// abandoned because another branch won the hedge race (or the caller gave
// up). Cancelled branches are neither failures nor successes: they only
// return their budget slot / normal slot and any breaker probe token.
func (e *EdgeController) CancelAttempt(attempt int, retryClass bool) {
	if retryClass {
		e.budget.Release()
	} else {
		e.budget.NormalEnded()
	}
	e.breaker.ProbeFinished()
}

// FinishCallFailure records the final failure of a whole attempt sequence
// against the breaker (consecutive-failure streak and probe release).
func (e *EdgeController) FinishCallFailure() {
	e.breaker.RecordFailure(e.clock.Now())
	e.breaker.ProbeFinished()
}

// DecideRetry evaluates whether another sequential retry is allowed after
// attempt attemptNo (1-based) failed. It applies, in order: retryability,
// max-attempts, breaker, deadline pre-check (remaining > MinProcessing +
// backoff), then the retry budget.
func (e *EdgeController) DecideRetry(callID uint64, attemptNo int, deadline Deadline, retryable bool) AttemptDecision {
	now := e.clock.Now()
	if !retryable {
		return AttemptDecision{Allowed: false, Reason: AbortNonRetryable, Attempt: attemptNo + 1}
	}
	if attemptNo+1 > e.cfg.MaxAttempts {
		return AttemptDecision{Allowed: false, Reason: AbortMaxAttempts, Attempt: attemptNo + 1}
	}
	if !e.breaker.Allow(now) {
		e.rejectedBreaker.Add(1)
		return AttemptDecision{Allowed: false, Reason: AbortBreakerOpen, Attempt: attemptNo + 1}
	}
	wait := e.cfg.Backoff.Delay(callID, attemptNo)
	if !deadline.EnoughFor(now, e.cfg.MinProcessing, wait) {
		e.deadlineAborts.Add(1)
		return AttemptDecision{Allowed: false, Reason: AbortDeadline, Wait: wait, Attempt: attemptNo + 1}
	}
	if !e.budget.TryAcquire(now) {
		e.rejectedBudget.Add(1)
		return AttemptDecision{Allowed: false, Reason: AbortBudgetExhausted, Wait: wait, Attempt: attemptNo + 1}
	}
	e.retries.Add(1)
	return AttemptDecision{
		Allowed:  true,
		Wait:     wait,
		Attempt:  attemptNo + 1,
		Deadline: deadline.End,
	}
}

// DecideHedge evaluates a hedged branch after the current attempt has been
// outstanding for HedgeDelay. branchNo counts hedge branches starting at 1;
// the total in-flight attempts (initial + branches + sequential retries
// already used) must stay within MaxAttempts, and hedging stops once
// MaxHedges branches have been fired.
func (e *EdgeController) DecideHedge(callID uint64, branchesFired, totalAttemptsUsed int, deadline Deadline) AttemptDecision {
	now := e.clock.Now()
	if e.cfg.MaxHedges <= 0 || branchesFired >= e.cfg.MaxHedges {
		return AttemptDecision{Allowed: false, Reason: AbortMaxAttempts, IsHedge: true}
	}
	if totalAttemptsUsed+1 > e.cfg.MaxAttempts {
		return AttemptDecision{Allowed: false, Reason: AbortMaxAttempts, IsHedge: true}
	}
	if !e.breaker.Allow(now) {
		e.rejectedBreaker.Add(1)
		return AttemptDecision{Allowed: false, Reason: AbortBreakerOpen, IsHedge: true}
	}
	if !deadline.EnoughFor(now, e.cfg.MinProcessing, e.cfg.HedgeDelay) {
		e.deadlineAborts.Add(1)
		return AttemptDecision{Allowed: false, Reason: AbortDeadline, IsHedge: true, Wait: e.cfg.HedgeDelay}
	}
	if !e.budget.TryAcquire(now) {
		e.rejectedBudget.Add(1)
		return AttemptDecision{Allowed: false, Reason: AbortBudgetExhausted, IsHedge: true, Wait: e.cfg.HedgeDelay}
	}
	e.hedges.Add(1)
	return AttemptDecision{
		Allowed:  true,
		Wait:     e.cfg.HedgeDelay,
		IsHedge:  true,
		Attempt:  totalAttemptsUsed + 1,
		Deadline: deadline.End,
	}
}

// Stats returns a consistent snapshot of all edge counters and limits.
func (e *EdgeController) Stats() EdgeStats {
	now := e.clock.Now()
	return EdgeStats{
		Calls:           e.calls.Load(),
		Successes:       e.successes.Load(),
		Failures:        e.failures.Load(),
		Retries:         e.retries.Load(),
		Hedges:          e.hedges.Load(),
		RejectedBudget:  e.rejectedBudget.Load(),
		RejectedBreaker: e.rejectedBreaker.Load(),
		DeadlineAborts:  e.deadlineAborts.Load(),
		BreakerState:    e.breaker.State(now),
		RetryInFlight:   e.budget.RetryInFlight(),
		ActiveNormal:    e.budget.ActiveNormal(),
		WindowCount:     e.budget.WindowCount(now),
		AllowedRetries:  e.budget.AllowedRetries(now),
	}
}
