package retry

import (
	"context"
	"time"
)

// InvokeFunc performs one physical downstream attempt. The context is
// cancelled when the branch loses the hedge race or the call is abandoned,
// so implementations can tear down in-flight network requests promptly.
type InvokeFunc func(ctx context.Context, attempt int, isHedge bool) error

// Outcome summarizes one logical call executed by Executor.
type Outcome struct {
	Success      bool
	Attempts     int // total dispatched attempts (initial + retries + hedges)
	Hedges       int
	FinalError   error
	Abort        AbortReason
	DeadlineLeft time.Duration
}

type branchResult struct {
	attempt int
	hedge   bool
	err     error
}

type branch struct {
	attempt int
	hedge   bool
	cancel  context.CancelFunc
	done    bool
	err     error
}

// Executor drives an InvokeFunc through an EdgeController, implementing
// exponential-backoff retries plus racing hedged branches with first-success
// cancellation of every other in-flight branch.
type Executor struct {
	ctrl    *EdgeController
	sched   Scheduler
	invoke  InvokeFunc
	baseCtx context.Context
}

// NewExecutor binds a controller, a scheduler (real or simulated), the
// attempt function and a base context used to propagate external
// cancellation.
func NewExecutor(ctrl *EdgeController, sched Scheduler, invoke InvokeFunc, baseCtx context.Context) *Executor {
	return &Executor{ctrl: ctrl, sched: sched, invoke: invoke, baseCtx: baseCtx}
}

// Execute runs one logical call with the propagated deadline.
func (x *Executor) Execute(deadline Deadline) Outcome {
	now := x.sched.Now()
	callID, start := x.ctrl.BeginCall(now, deadline)
	if !start.Allowed {
		x.ctrl.RecordCallOutcome(false)
		return Outcome{Success: false, Abort: start.Reason, FinalError: ErrBreakerOpen}
	}

	ctx, cancel := context.WithCancel(x.baseCtx)
	defer cancel()

	var branches []*branch
	results := make(chan branchResult, x.ctrl.cfg.MaxAttempts+1)
	callFinished := false

	launch := func(attempt int, hedge bool) *branch {
		bctx, bcancel := context.WithCancel(ctx)
		b := &branch{attempt: attempt, hedge: hedge, cancel: bcancel}
		go func() {
			err := x.invoke(bctx, attempt, hedge)
			select {
			case results <- branchResult{attempt: attempt, hedge: hedge, err: err}:
			case <-ctx.Done():
			}
		}()
		return b
	}

	branches = append(branches, launch(1, false))
	attemptsUsed := 1
	hedgesFired := 0
	var lastErr error

	// hedgeTimer fires when the next hedged branch becomes due.
	var hedgeTimer <-chan time.Time
	armHedge := func() {
		if x.ctrl.cfg.MaxHedges > 0 && hedgesFired < x.ctrl.cfg.MaxHedges &&
			attemptsUsed < x.ctrl.cfg.MaxAttempts {
			hedgeTimer = x.sched.After(x.ctrl.cfg.HedgeDelay)
		} else {
			hedgeTimer = nil
		}
	}
	armHedge()

	// retryTimer fires when the backoff before a sequential retry elapses.
	var retryTimer <-chan time.Time

	finalizeFail := func(reason AbortReason) Outcome {
		x.ctrl.RecordCallOutcome(false)
		return Outcome{
			Success:      false,
			Attempts:     attemptsUsed,
			Hedges:       hedgesFired,
			FinalError:   lastErr,
			Abort:        reason,
			DeadlineLeft: deadline.Remaining(x.sched.Now()),
		}
	}

	for {
		select {
		case <-x.baseCtx.Done():
			cancel()
			callFinished = true
			return finalizeFail(AbortDeadline)
		case r := <-results:
			if callFinished {
				// Late result from a cancelled branch: release its slot and
				// drop it. The controller release paths are idempotent.
				for _, b := range branches {
					if b.attempt == r.attempt && !b.done {
						b.done = true
						x.ctrl.CancelAttempt(b.attempt, b.hedge || b.attempt > 1)
					}
				}
				continue
			}
			now := x.sched.Now()
			var done *branch
			for _, b := range branches {
				if b.attempt == r.attempt && !b.done {
					b.done = true
					b.err = r.err
					done = b
					break
				}
			}
			if done == nil {
				continue
			}
			retryClass := done.hedge || done.attempt > 1
			if r.err == nil {
				// First success wins: cancel every other in-flight branch.
				cancel()
				callFinished = true
				for _, b := range branches {
					if b.attempt != done.attempt {
						b.cancel()
					}
				}
				x.ctrl.AttemptFinished(done.attempt, retryClass, true, true)
				for _, b := range branches {
					if b.attempt != done.attempt && b.done {
						x.ctrl.AttemptFinished(b.attempt, b.hedge || b.attempt > 1, b.err == nil, false)
					}
				}
				for _, b := range branches {
					if b.attempt != done.attempt && !b.done {
						// Cancel before any result: free the slot immediately;
						// the late result will see callFinished and skip it.
						x.ctrl.CancelAttempt(b.attempt, b.hedge || b.attempt > 1)
						b.done = true
					}
				}
				x.ctrl.RecordCallOutcome(true)
				return Outcome{
					Success:      true,
					Attempts:     attemptsUsed,
					Hedges:       hedgesFired,
					DeadlineLeft: deadline.Remaining(now),
				}
			}
			lastErr = r.err
			x.ctrl.AttemptFinished(done.attempt, retryClass, false, false)

			// A sequential retry is considered only once every in-flight
			// branch has reported a failure.
			allDone := true
			for _, b := range branches {
				if !b.done {
					allDone = false
					break
				}
			}
			if allDone {
				d := x.ctrl.DecideRetry(callID, attemptsUsed, deadline, true)
				if !d.Allowed {
					// Settle breaker with the final failure of the sequence.
					x.ctrl.FinishCallFailure()
					callFinished = true
					return finalizeFail(d.Reason)
				}
				retryTimer = x.sched.After(d.Wait)
				hedgeTimer = nil
			} else {
				// While another branch is still racing, no new hedge timer.
				hedgeTimer = nil
			}

		case <-hedgeTimer:
			d := x.ctrl.DecideHedge(callID, hedgesFired, attemptsUsed, deadline)
			if d.Allowed {
				branches = append(branches, launch(d.Attempt, true))
				attemptsUsed = d.Attempt
				hedgesFired++
			}
			armHedge()

		case <-retryTimer:
			now := x.sched.Now()
			// Re-validate the deadline after sleeping.
			if !deadline.EnoughFor(now, x.ctrl.cfg.MinProcessing, 0) {
				x.ctrl.FinishCallFailure()
				callFinished = true
				return finalizeFail(AbortDeadline)
			}
			next := attemptsUsed + 1
			branches = append(branches, launch(next, false))
			attemptsUsed = next
			retryTimer = nil
			armHedge()
		}
	}
}

// Sentinel errors returned by the executor.
var (
	// ErrBreakerOpen is returned when the breaker rejects a call up-front.
	ErrBreakerOpen = breakerError{"circuit breaker is open"}
)

type breakerError struct{ msg string }

func (e breakerError) Error() string { return e.msg }
