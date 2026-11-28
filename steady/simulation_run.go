package steady

import (
	"container/heap"
	"math"
	"time"

	"prescreen.local/ps-9f073c0c88/retry"
)

func (s *simulator) run() {
	warmDone := false
	for s.events.Len() > 0 {
		ev := heap.Pop(&s.events).(*simEvent)
		s.clock.Advance(time.Unix(0, ev.at))
		switch ev.kind {
		case evEnd:
			if ev.node == -1 && !warmDone {
				warmDone = true
				copy(s.warmNormal, s.totalNormal)
				copy(s.warmPhysical, s.totalPhysical)
				copy(s.warmFailure, s.totalFailure)
				copy(s.warmCalls, s.totalCalls)
				copy(s.warmRetries, s.totalRetries)
				copy(s.warmHedges, s.totalHedges)
				copy(s.warmEdgeAttempts, s.totalEdgeAttempts)
				copy(s.ingressWarm, s.ingressTotal)
				continue
			}
			if ev.node == -2 {
				return
			}
		case evArrive:
			s.handleArrive(ev)
		case evComplete:
			s.handleComplete(ev)
		case evBackoff:
			s.handleBackoff(ev)
		case evHedge:
			s.handleHedge(ev)
		}
	}
}

// scheduleIngress schedules the next Poisson arrival for every entry node.
func (s *simulator) scheduleIngress(fromNS int64) {
	for i, rate := range s.topo.ingress {
		if rate <= 0 {
			continue
		}
		gap := s.rng.ExpFloat64() / rate
		at := fromNS + int64(gap*float64(time.Second))
		s.push(&simEvent{at: at, kind: evArrive, node: i})
	}
}

func (s *simulator) handleArrive(ev *simEvent) {
	horizon := int64(s.cfg.Warmup + s.cfg.Duration)
	if ev.at < horizon {
		rate := s.topo.ingress[ev.node]
		gap := s.rng.ExpFloat64() / rate
		s.push(&simEvent{
			at:   ev.at + int64(gap*float64(time.Second)),
			kind: evArrive, node: ev.node,
		})
	}
	s.ingressTotal[ev.node]++
	s.reqSeq++
	req := &simRequest{
		id:      s.reqSeq,
		arrived: ev.at,
		cursor:  ev.node,
		calls:   map[int]*simCall{},
	}
	s.enterNode(req, ev.node)
}

// enterNode starts the top-level edge leaving node u, or completes the
// request at a sink.
func (s *simulator) enterNode(req *simRequest, u int) {
	req.cursor = u
	if len(s.topo.out[u]) == 0 {
		return
	}
	ei := s.topo.out[u][0]
	s.startNested(req, ei, -1)
}

func (s *simulator) edgeController(ei int) *retry.EdgeController {
	return s.controllers[s.edgeByIndex[ei]]
}

// startNested creates the single logical call object for edge ei. A
// rollback retry never recreates it: it re-dispatches attempts and replays
// the nested chain through resetNested.
func (s *simulator) startNested(req *simRequest, ei, parentEI int) {
	e := s.topo.edges[ei]
	ctrl := s.edgeController(ei)
	now := s.now()

	deadlineNS := int64(0)
	if e.RequestDeadline > 0 {
		deadlineNS = now.UnixNano() + int64(e.RequestDeadline)
	}
	dl := retry.Deadline{}
	if deadlineNS > 0 {
		dl = retry.Deadline{End: time.Unix(0, deadlineNS)}
	}
	_, decision := ctrl.BeginCall(now, dl)
	if !decision.Allowed {
		s.totalCalls[ei]++
		ctrl.RecordCallOutcome(false)
		if parentEI >= 0 {
			s.failNested(req, req.calls[parentEI], now.UnixNano())
		}
		return
	}

	s.totalCalls[ei]++
	call := &simCall{
		edge:         ei,
		parentEdge:   parentEI,
		childEdge:    -1,
		attemptsUsed: 1,
		pending:      map[int]*simAttempt{},
	}
	if parentEI >= 0 {
		parent := req.calls[parentEI]
		parent.childEdge = ei
		parent.childCall = call
	}
	req.calls[ei] = call
	s.dispatchAttempt(req, call, 1, false, deadlineNS)
}

func (s *simulator) dispatchAttempt(req *simRequest, call *simCall, attempt int, hedge bool, deadlineNS int64) {
	ei := call.edge
	e := s.topo.edges[ei]
	now := s.now().UnixNano()
	a := &simAttempt{
		edge: ei, attempt: attempt, hedge: hedge,
		startAt: now, deadline: deadlineNS,
	}
	call.pending[attempt] = a

	s.totalPhysical[e.v]++
	// Every physical attempt is also one logical processing of the target
	// node, matching the solver's normal-arrival accounting.
	s.totalNormal[e.v]++
	s.totalEdgeAttempts[ei]++

	mean := s.topo.nodes[e.v].MeanServiceTime.Seconds() * s.cfg.ServiceScale
	service := s.rng.ExpFloat64() * mean
	finishAt := now + int64(service*float64(time.Second))
	if deadlineNS > 0 && finishAt > deadlineNS {
		finishAt = deadlineNS
	}
	s.push(&simEvent{
		at: finishAt, kind: evComplete, node: e.v,
		req: req, attempt: a,
	})

	if e.MaxHedges > 0 && call.hedgesFired < e.MaxHedges &&
		call.attemptsUsed < e.MaxAttempts && !call.hedgeArmed {
		call.hedgeArmed = true
		s.push(&simEvent{
			at: now + int64(e.HedgeDelay), kind: evHedge,
			node: e.u, req: req, attempt: a,
		})
	}
}

func (s *simulator) handleComplete(ev *simEvent) {
	req := ev.req
	a := ev.attempt
	call := req.calls[a.edge]
	if call == nil || call.finished {
		return
	}
	if _, ok := call.pending[a.attempt]; !ok {
		return
	}
	ei := a.edge
	e := s.topo.edges[ei]
	ctrl := s.edgeController(ei)

	load := s.instantLoad(e.v)
	pFail := s.topo.nodes[e.v].Curve.PFail(load)
	localFailed := s.rng.Float64() < pFail
	if ev.at >= a.deadline && a.deadline > 0 {
		localFailed = true
	}

	retryClass := a.hedge || a.attempt > 1
	delete(call.pending, a.attempt)

	if !localFailed {
		for _, other := range call.pending {
			ctrl.CancelAttempt(other.attempt, other.hedge || other.attempt > 1)
		}
		call.pending = map[int]*simAttempt{}
		ctrl.AttemptFinished(a.attempt, retryClass, true, false)
		a.locallyDone = true
		s.advanceAfterEdge(req, ei)
		return
	}

	s.totalFailure[e.v]++
	if !a.locallyDone {
		ctrl.AttemptFinished(a.attempt, retryClass, false, false)
	}
	a.locallyDone = true

	if len(call.pending) > 0 {
		return
	}
	dl := retry.Deadline{}
	if a.deadline > 0 {
		dl = retry.Deadline{End: time.Unix(0, a.deadline)}
	}
	dec := ctrl.DecideRetry(req.id, call.attemptsUsed, dl, true)
	if !dec.Allowed {
		call.finished = true
		call.succeeded = false
		ctrl.FinishCallFailure()
		ctrl.RecordCallOutcome(false)
		s.failNested(req, call, ev.at)
		return
	}
	s.totalRetries[ei]++
	call.attemptsUsed = dec.Attempt
	call.hedgeArmed = false
	s.push(&simEvent{
		at: ev.at + int64(dec.Wait), kind: evBackoff,
		node: e.u, req: req, attempt: a,
	})
}

func (s *simulator) handleBackoff(ev *simEvent) {
	req := ev.req
	call := req.calls[ev.attempt.edge]
	if call == nil || call.finished {
		return
	}
	ei := call.edge
	deadlineNS := ev.attempt.deadline
	if deadlineNS > 0 && s.now().UnixNano() >= deadlineNS {
		call.finished = true
		call.succeeded = false
		s.edgeController(ei).FinishCallFailure()
		s.edgeController(ei).RecordCallOutcome(false)
		s.failNested(req, call, s.now().UnixNano())
		return
	}
	s.dispatchAttempt(req, call, call.attemptsUsed, false, deadlineNS)
}

func (s *simulator) handleHedge(ev *simEvent) {
	req := ev.req
	a := ev.attempt
	call := req.calls[a.edge]
	if call == nil || call.finished {
		return
	}
	if _, ok := call.pending[a.attempt]; !ok {
		return
	}
	ei := call.edge
	ctrl := s.edgeController(ei)
	dl := retry.Deadline{}
	if a.deadline > 0 {
		dl = retry.Deadline{End: time.Unix(0, a.deadline)}
	}
	dec := ctrl.DecideHedge(req.id, call.hedgesFired, call.attemptsUsed, dl)
	if !dec.Allowed {
		call.hedgeArmed = false
		return
	}
	s.totalHedges[ei]++
	call.hedgesFired++
	call.attemptsUsed = dec.Attempt
	s.dispatchAttempt(req, call, dec.Attempt, true, a.deadline)
}

// advanceAfterEdge runs the next nested edge after a locally successful
// attempt, or finalizes success at a sink.
func (s *simulator) advanceAfterEdge(req *simRequest, ei int) {
	v := s.topo.edges[ei].v
	if len(s.topo.out[v]) == 0 {
		s.succeedChain(req, ei)
		return
	}
	nextEI := s.topo.out[v][0]
	if call, ok := req.calls[ei]; ok && call.childCall != nil {
		// Replay after a parent rollback retry: reset the existing nested
		// call generation rather than counting a new logical call.
		s.replayNested(req, call.childCall)
		return
	}
	s.startNested(req, nextEI, ei)
}

// replayNested re-arms an existing nested call for another parent attempt.
// It reuses the same logical-call counters but emits fresh physical
// attempts, exactly mirroring a solver edge retry that re-runs the chain.
func (s *simulator) replayNested(req *simRequest, call *simCall) {
	if call.finished {
		// A previously failed nested call is given a fresh generation.
		call.finished = false
		call.succeeded = false
		call.attemptsUsed = 1
		call.hedgesFired = 0
		call.pending = map[int]*simAttempt{}
		call.generations++
	} else if len(call.pending) == 0 {
		// Defensive reset for a drained call being re-driven by its parent.
		call.attemptsUsed = 1
		call.hedgesFired = 0
	}
	deadlineNS := s.callDeadline(call)
	if deadlineNS == 0 {
		if e := s.topo.edges[call.edge]; e.RequestDeadline > 0 {
			deadlineNS = s.now().UnixNano() + int64(e.RequestDeadline)
		}
	}
	s.dispatchAttempt(req, call, 1, false, deadlineNS)
}

func (s *simulator) succeedChain(req *simRequest, ei int) {
	for ei >= 0 {
		c := req.calls[ei]
		if c == nil {
			return
		}
		c.finished = true
		c.succeeded = true
		s.edgeController(ei).RecordCallOutcome(true)
		ei = c.parentEdge
	}
}

// failNested propagates a nested failure to the parent edge. A top-level
// failure simply ends the request.
func (s *simulator) failNested(req *simRequest, call *simCall, at int64) {
	if call.parentEdge < 0 {
		return
	}
	parent := req.calls[call.parentEdge]
	if parent == nil || parent.finished {
		return
	}
	// The parent's locally successful attempt now counts as failed because
	// its downstream chain gave up. Reuse the parent retry path directly.
	s.parentDownstreamFailure(req, parent, at)
}

// parentDownstreamFailure applies the parent edge's retry/budget decision
// after a nested chain failure, without double-counting a local attempt.
func (s *simulator) parentDownstreamFailure(req *simRequest, parent *simCall, at int64) {
	ei := parent.edge
	e := s.topo.edges[ei]
	ctrl := s.edgeController(ei)
	s.totalFailure[e.v]++
	dl := retry.Deadline{}
	if deadlineNS := s.callDeadline(parent); deadlineNS > 0 {
		dl = retry.Deadline{End: time.Unix(0, deadlineNS)}
	}
	dec := ctrl.DecideRetry(req.id, parent.attemptsUsed, dl, true)
	if !dec.Allowed {
		parent.finished = true
		parent.succeeded = false
		ctrl.FinishCallFailure()
		ctrl.RecordCallOutcome(false)
		s.failNested(req, parent, at)
		return
	}
	s.totalRetries[ei]++
	parent.attemptsUsed = dec.Attempt
	parent.hedgeArmed = false
	s.push(&simEvent{
		at: at + int64(dec.Wait), kind: evBackoff,
		node: e.u, req: req,
		attempt: &simAttempt{edge: ei, attempt: dec.Attempt, deadline: s.callDeadline(parent)},
	})
}

func (s *simulator) callDeadline(call *simCall) int64 {
	for _, a := range call.pending {
		return a.deadline
	}
	return 0
}

// instantLoad estimates the current offered load of node v via Little's law
// over the accumulated physical-attempt count.
func (s *simulator) instantLoad(v int) float64 {
	elapsed := s.now().UnixNano()
	if elapsed <= 0 {
		return float64(s.totalPhysical[v])
	}
	rate := float64(s.totalPhysical[v]) / (float64(elapsed) / float64(time.Second))
	if math.IsNaN(rate) || math.IsInf(rate, 0) {
		return 0
	}
	return rate
}
