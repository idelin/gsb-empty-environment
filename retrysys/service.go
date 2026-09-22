package retrysys

import (
	"fmt"

	"example/retrysim/sim"
)

// Service is the LOWER retry layer. On behalf of one upper attempt it may
// start up to LowerAttempts attempts, each a physical request to a replica.
// Every physical request is drawn from the caller's shared Budget, so upper
// retries and lower retries compete for one request pool instead of
// multiplying.
type Service struct {
	name       string
	world      *World
	attempts   int
	perTimeout int
	calls      map[int]*callState
}

type inflight struct {
	physical AttemptID
	replica  string
}

type callState struct {
	callNo   int
	proxy    int
	proxyID  AttemptID
	budget   *Budget
	serviceN int
	done     bool
	respTo   string
	inflight map[int]inflight // keyed by lower attempt number
}

// NewService builds the lower retry layer.
func NewService(world *World, attempts, perAttemptTimeout int) *Service {
	return &Service{
		name:       "service",
		world:      world,
		attempts:   attempts,
		perTimeout: perAttemptTimeout,
		calls:      map[int]*callState{},
	}
}

// Receive implements sim.Actor.
func (s *Service) Receive(m sim.Msg, send func(sim.Msg), after func(any, int)) {
	switch b := m.Body.(type) {
	case CallReq:
		s.onCall(b, send, after)
	case ReplicaResp:
		s.onReplicaResp(b, send)
	case ServiceAttemptTimeout:
		s.onTimeout(b, send)

	case CancelReqs:
		// Upper layer cancelled this attempt (its attempt timer fired).
		st := s.calls[b.Call]
		if st != nil && !st.done {
			s.cancelInflight(st, send)
			s.finish(st, false, WhyCancelled, send)
		}

	case DeadlineFire:
		s.world.expireBudget()
		s.world.Ledger.Record(s.world.Now(), KindExpire, AttemptID{Call: b.Call},
			WhyExpired, "shared-deadline")
		s.CancelAll(b.Call, send)
	}
}

func (s *Service) onCall(r CallReq, send func(sim.Msg), after func(any, int)) {
	if _, ok := s.calls[r.Call]; ok {
		return
	}
	st := &callState{
		callNo:   r.Call,
		proxy:    r.Proxy,
		proxyID:  r.ProxyID,
		budget:   r.Budget,
		respTo:   "proxy",
		inflight: map[int]inflight{},
	}
	s.calls[r.Call] = st
	s.world.Ledger.Record(s.world.Now(), KindAttempt, r.ProxyID, "", "lower-loop-start")
	s.startNext(st, send, after)
}

// startNext launches another lower attempt or terminates the lower loop.
// The budget/deadline check and the decrement are a single simulation step:
// this funnel is what prevents an uncontrolled 3x3 fan-out.
func (s *Service) startNext(st *callState, send func(sim.Msg), after func(any, int)) {
	w := s.world
	if st.done {
		return
	}
	if st.serviceN >= s.attempts {
		s.finish(st, false, WhyAttemptBudget, send)
		return
	}
	st.serviceN++
	lowerID := AttemptID{Call: st.callNo, Proxy: st.proxy, Service: st.serviceN}
	w.Ledger.Record(w.Now(), KindAttempt, lowerID, "", "lower-attempt")

	if w.Now() >= st.budget.Deadline() {
		w.Ledger.Record(w.Now(), KindReject, lowerID, WhyDeadline, "service")
		s.finish(st, false, WhyDeadline, send)
		return
	}
	if !st.budget.Acquire(w.Now()) {
		why := WhySharedBudget
		if st.budget.Expired() {
			why = WhyDeadline
		}
		w.Ledger.Record(w.Now(), KindReject, lowerID, why, "service")
		s.finish(st, false, why, send)
		return
	}

	ordinal := w.Ledger.PhysicalSends(st.callNo) + 1
	replica := w.pickReplica(ordinal)
	physID := AttemptID{Call: st.callNo, Proxy: st.proxy, Service: st.serviceN, Replica: replica}
	st.inflight[st.serviceN] = inflight{physical: physID, replica: replica}
	w.Ledger.Record(w.Now(), KindSend, physID, "",
		fmt.Sprintf("ordinal=%d remaining=%d", ordinal, st.budget.Remaining()))

	send(sim.Msg{From: s.name, To: replica, Body: ReplicaReq{
		Call:     st.callNo,
		Service:  st.serviceN,
		Replica:  replica,
		Physical: physID,
		Ordinal:  ordinal,
	}})
	// The attempt timeout is a real virtual-timer; cancellation propagates
	// when it fires before the (delayed) replica reply.
	after(ServiceAttemptTimeout{Call: st.callNo, Service: st.serviceN}, s.perTimeout)
}

func (s *Service) onReplicaResp(r ReplicaResp, send func(sim.Msg)) {
	w := s.world
	st := s.calls[r.Call]
	w.Ledger.Record(w.Now(), KindReply, r.Physical, replyWhy(r.OK, r.Late),
		fmt.Sprintf("from=%s", r.Replica))
	if st == nil {
		// Call already terminal: the late reply is evidence in the ledger and
		// nothing else. It cannot resurrect or change the terminal state.
		w.Ledger.Record(w.Now(), KindReject, r.Physical, WhyLate, "reply-after-terminal")
		return
	}
	inf, ok := st.inflight[r.Service]
	if st.done || !ok || inf.physical != r.Physical {
		w.Ledger.Record(w.Now(), KindReject, r.Physical, WhyLate, "stale-reply-ignored")
		return
	}
	s.suppressOthers(st, r.Service)
	delete(st.inflight, r.Service)

	if r.OK {
		s.finish(st, true, WhyOK, send)
		return
	}
	lowerID := AttemptID{Call: r.Call, Proxy: st.proxy, Service: r.Service}
	w.Ledger.Record(w.Now(), KindAttempt, lowerID, WhyReplicaFail, "failed-will-retry")
	s.startNext(st, send, w.afterTo(s.name))
}

func (s *Service) onTimeout(t ServiceAttemptTimeout, send func(sim.Msg)) {
	w := s.world
	st := s.calls[t.Call]
	if st == nil || st.done {
		return
	}
	inf, ok := st.inflight[t.Service]
	if !ok {
		return // settled first; the timer fired against a superseded attempt.
	}
	w.Ledger.Record(w.Now(), KindTimeout, inf.physical, WhyTimeout, "attempt-timer")
	delete(st.inflight, t.Service)
	s.cancelInflight(st, send)
	w.Ledger.Record(w.Now(), KindCancel, inf.physical, WhyCancelled, "timeout-cancels-request")
	s.startNext(st, send, w.afterTo(s.name))
}

// CancelAll is invoked when the shared deadline fires. In-flight requests are
// cancelled (cancellation propagates) and the loop terminates as deadline.
func (s *Service) CancelAll(callNo int, send func(sim.Msg)) {
	st := s.calls[callNo]
	if st == nil || st.done {
		return
	}
	s.cancelInflight(st, send)
	s.finish(st, false, WhyDeadline, send)
}

// suppressOthers asks the still-in-flight sibling replicas to stop without
// recording a cancellation race against the attempt that just settled.
func (s *Service) suppressOthers(st *callState, keepService int) {
	w := s.world
	for n, inf := range st.inflight {
		if n == keepService {
			continue
		}
		w.Ledger.Record(w.Now(), KindCancel, inf.physical, WhyCancelled, "sibling-on-settle")
		w.sendTo(inf.replica, ReplicaCancel{Call: st.callNo, Physical: inf.physical})
		delete(st.inflight, n)
	}
}

func (s *Service) cancelInflight(st *callState, send func(sim.Msg)) {
	w := s.world
	for n, inf := range st.inflight {
		w.Ledger.Record(w.Now(), KindCancel, inf.physical, WhyCancelled, "inflight")
		if send != nil {
			send(sim.Msg{From: s.name, To: inf.replica, Body: ReplicaCancel{Call: st.callNo, Physical: inf.physical}})
		}
		delete(st.inflight, n)
	}
}

func (s *Service) finish(st *callState, ok bool, why Why, send func(sim.Msg)) {
	if st.done {
		return
	}
	st.done = true
	// Ensure nothing in flight can still influence the settled loop.
	s.cancelInflight(st, send)
	s.world.Ledger.Record(s.world.Now(), KindTerminal, st.proxyID, why, "service")
	send(sim.Msg{From: s.name, To: st.respTo, Body: CallResp{
		Call: st.callNo, Proxy: st.proxy, OK: ok, Why: why,
	}})
	delete(s.calls, st.callNo)
}

func replyWhy(ok, late bool) Why {
	switch {
	case late:
		return WhyLate
	case ok:
		return WhyOK
	default:
		return WhyReplicaFail
	}
}
