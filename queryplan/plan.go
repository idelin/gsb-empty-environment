package queryplan

import (
	"context"
	"errors"
	"sync"
	"time"
)

// plan tracks every candidate attempt belonging to one Lookup.
type plan struct {
	planner    *Planner
	query      Query
	delegation Delegation
	result     *Result
	start      time.Time

	// ctx is canceled once the plan has an answer, its parent is canceled, or
	// every candidate has terminated. Canceling it unblocks any upstream call.
	ctx    context.Context
	cancel context.CancelFunc

	// outcomes is buffered for every candidate so a slow collector can never
	// block a candidate goroutine; it is never closed, because every sender is
	// waited on via done before run returns.
	outcomes chan outcome

	mu      sync.Mutex
	pending int
	// recorded ensures each candidate's outcome mutates plan state exactly
	// once, even when finish drains outcomes the collector already processed.
	recorded []bool
	// decided means the collector already picked a winner or terminal error.
	decided bool

	// done lets run block until all candidate goroutines have exited, which is
	// what guarantees the host never observes a dangling waiter after Lookup.
	done sync.WaitGroup
}

type outcome struct {
	index int
	state State
	resp  *Response
	err   error
}

func (p *Planner) startPlan(parent context.Context, q Query, d Delegation, result *Result, start time.Time) *plan {
	ctx, cancel := context.WithCancel(parent)
	pl := &plan{
		planner:    p,
		query:      q,
		delegation: d,
		result:     result,
		start:      start,
		ctx:        ctx,
		cancel:     cancel,
		outcomes:   make(chan outcome, len(d.Candidates)),
		pending:    len(d.Candidates),
		recorded:   make([]bool, len(d.Candidates)),
	}
	return pl
}

// run launches all candidates and collects their outcomes in the calling
// goroutine. It returns the first usable answer; when the context ends or all
// candidates fail, every candidate goroutine is drained before returning.
func (pl *plan) run() (*Result, error) {
	defer pl.cancel()

	for i, candidate := range pl.delegation.Candidates {
		pl.result.Candidates[i] = CandidateStatus{Candidate: candidate, State: StatePending}
		pl.done.Add(1)
		go pl.runCandidate(i, candidate)
	}

	for {
		select {
		case oc := <-pl.outcomes:
			pl.record(oc)
			if oc.state == StateSuccess {
				return pl.finish(oc.resp, nil)
			}
			if pl.remaining() == 0 {
				return pl.finish(nil, pl.terminalError())
			}
		case <-pl.ctx.Done():
			// Parent cancellation (or our own cancel) ends the decision loop;
			// still drain in-flight candidates so nothing is left waiting.
			return pl.finish(nil, pl.ctx.Err())
		}
	}
}

// finish cancels stragglers and waits for every candidate goroutine before
// handing the result back to the host.
func (pl *plan) finish(resp *Response, err error) (*Result, error) {
	pl.cancel()
	pl.done.Wait()
	pl.result.Response = resp
	pl.result.Elapsed = time.Since(pl.start)

	// Drain any outcomes delivered after the decision so the channel is empty
	// when the plan becomes garbage; record() keeps statuses consistent too.
	for {
		select {
		case oc := <-pl.outcomes:
			pl.record(oc)
		default:
			return pl.result, err
		}
	}
}

// runCandidate performs one upstream attempt with its own deadline and reports
// exactly one terminal outcome. It never outlives run(): finish waits on done.
func (pl *plan) runCandidate(index int, candidate Candidate) {
	defer pl.done.Done()

	start := time.Now()
	ctx, cancel := pl.candidateContext()
	defer cancel()

	resp, err := pl.planner.upstream.Query(ctx, candidate, pl.query)
	oc := outcome{
		index: index,
		state: pl.classify(ctx.Err(), err),
		resp:  resp,
		err:   err,
	}
	if oc.state == StateSuccess && resp == nil {
		// A well-behaved upstream returns either a response or an error; treat
		// a nil response as an upstream failure rather than a nil answer.
		oc.state = StateFailed
		oc.err = errors.New("queryplan: upstream returned nil response")
	}
	pl.outcomes <- oc

	pl.mu.Lock()
	pl.result.Candidates[index].Duration = time.Since(start)
	pl.mu.Unlock()
}

// candidateContext derives the per-candidate deadline, bounded by both the
// plan context and the configured candidate timeout.
func (pl *plan) candidateContext() (context.Context, context.CancelFunc) {
	timeout := pl.planner.candidateTimeout
	if timeout < 0 {
		return pl.ctx, func() {}
	}
	if timeout == 0 {
		timeout = DefaultCandidateTimeout
	}
	ctx, cancel := context.WithTimeout(pl.ctx, timeout)
	return ctx, cancel
}

// classify maps an upstream error onto a tracked terminal state.
//
// candidateErr is the derived per-candidate context's error after Query
// returns: DeadlineExceeded means this candidate's own deadline fired, while
// Canceled means the plan/parent context canceled it first. Checking the
// derived context (rather than only the upstream error) keeps a candidate
// timeout distinguishable even when a later plan cancellation races in.
func (pl *plan) classify(candidateErr, err error) State {
	if err == nil {
		return StateSuccess
	}
	// An upstream that explicitly reports a deadline (e.g. a forwarding
	// server's own RTO) is a timeout regardless of our local context state.
	if errors.Is(err, context.DeadlineExceeded) {
		return StateTimedOut
	}
	if errors.Is(candidateErr, context.DeadlineExceeded) {
		return StateTimedOut
	}
	if errors.Is(candidateErr, context.Canceled) || errors.Is(err, context.Canceled) {
		return StateCanceled
	}
	return StateFailed
}

func (pl *plan) record(oc outcome) {
	pl.mu.Lock()
	defer pl.mu.Unlock()

	if pl.recorded[oc.index] {
		return
	}
	pl.recorded[oc.index] = true
	pl.result.Candidates[oc.index].State = oc.state
	pl.result.Candidates[oc.index].Err = oc.err
	pl.pending--

	if oc.state == StateSuccess && !pl.decided {
		pl.decided = true
		// Cache commit is generation-gated: an old plan whose delegation was
		// replaced while in flight cannot overwrite the new generation's cache.
		pl.planner.commitAnswer(cacheKey(pl.query), pl.delegation.Generation, oc.resp)
	}
}

func (pl *plan) remaining() int {
	pl.mu.Lock()
	defer pl.mu.Unlock()
	return pl.pending
}

// terminalError builds the error returned when no candidate can answer. A
// parent cancellation takes precedence; otherwise the host gets the full
// per-candidate breakdown.
func (pl *plan) terminalError() error {
	if err := pl.ctx.Err(); err != nil {
		return err
	}
	pl.mu.Lock()
	statuses := make([]CandidateStatus, len(pl.result.Candidates))
	copy(statuses, pl.result.Candidates)
	pl.mu.Unlock()
	return &AllFailedError{Statuses: statuses}
}
