package retrysys

import (
	"example/retrysim/sim"
)

// Proxy is the UPPER retry layer. It may start up to UpperAttempts attempts;
// each attempt delegates to the lower layer. It owns no private request
// budget: every physical request its attempts cause is acquired from the one
// shared Budget held by the call.
type Proxy struct {
	world      *World
	attempts   int
	perTimeout int
	backoff    int
	st         *proxyState
}

type proxyState struct {
	callNo   int
	proxyN   int
	done     bool
	why      Why
	awaiting bool
}

// NewProxy builds the upper retry layer.
func NewProxy(w *World, attempts, perAttemptTimeout, backoff int) *Proxy {
	return &Proxy{world: w, attempts: attempts, perTimeout: perAttemptTimeout, backoff: backoff}
}

// Receive implements sim.Actor.
func (p *Proxy) Receive(m sim.Msg, send func(sim.Msg), after func(any, int)) {
	switch b := m.Body.(type) {
	case StartCall:
		p.st = &proxyState{callNo: b.Call}
		p.start(send, after)
	case CallResp:
		p.onResp(b, send, after)
	case ProxyRetry:
		if p.st == nil || p.st.done {
			return
		}
		if p.world.Now() >= p.world.cfg.Deadline {
			p.finish(false, WhyDeadline)
			return
		}
		p.start(send, after)
	case ProxyAttemptTimeout:
		p.onAttemptTimeout(b, send, after)
	case DeadlineFire:
		if p.st != nil && !p.st.done {
			p.finish(false, WhyDeadline)
		}
	}
}

func (p *Proxy) start(send func(sim.Msg), after func(any, int)) {
	w := p.world
	st := p.st
	if st.done {
		return
	}
	if st.proxyN >= p.attempts {
		p.finish(false, WhyAttemptBudget)
		return
	}
	// Never even open a new attempt at/after the deadline.
	if w.Now() >= w.cfg.Deadline {
		p.finish(false, WhyDeadline)
		return
	}
	st.proxyN++
	id := AttemptID{Call: st.callNo, Proxy: st.proxyN}
	w.Ledger.Record(w.Now(), KindAttempt, id, "", "upper-attempt")
	st.awaiting = true
	send(sim.Msg{From: "proxy", To: "service", Body: CallReq{
		Call:    st.callNo,
		Proxy:   st.proxyN,
		ProxyID: id,
		Budget:  w.budget,
	}})
	after(ProxyAttemptTimeout{Call: st.callNo, Proxy: st.proxyN}, p.perTimeout)
}

func (p *Proxy) onResp(r CallResp, send func(sim.Msg), after func(any, int)) {
	w := p.world
	st := p.st
	id := AttemptID{Call: r.Call, Proxy: r.Proxy}
	if st == nil || st.done || r.Proxy != st.proxyN {
		w.Ledger.Record(w.Now(), KindReject, id, WhyLate, "lower-response-after-move-on")
		return
	}
	if !st.awaiting {
		// We initiated this cancellation (upper attempt timer fired) and are
		// already waiting for the retry timer: the terminal response of the
		// cancelled lower loop carries no new information.
		if r.Why == WhyCancelled {
			return
		}
		w.Ledger.Record(w.Now(), KindReject, id, WhyLate, "lower-response-after-move-on")
		return
	}
	st.awaiting = false
	if r.OK {
		p.finish(true, WhyOK)
		return
	}
	// A deadline result is terminal: the shared clock forbids new attempts.
	if r.Why == WhyDeadline {
		p.finish(false, WhyDeadline)
		return
	}

	w.Ledger.Record(w.Now(), KindAttempt, id, r.Why, "upper-failed-will-retry")
	after(ProxyRetry{Call: r.Call}, p.backoff)
}

func (p *Proxy) onAttemptTimeout(t ProxyAttemptTimeout, _ func(sim.Msg), after func(any, int)) {
	w := p.world
	st := p.st
	id := AttemptID{Call: t.Call, Proxy: t.Proxy}
	if st == nil || st.done || t.Proxy != st.proxyN || !st.awaiting {
		return
	}
	st.awaiting = false
	// Cancellation propagates to the lower loop and its in-flight replica.

	w.Ledger.Record(w.Now(), KindCancel, id, WhyTimeout, "upper-attempt-timer")
	// Local ordering: the cancellation is acted on by the lower layer before
	// the cancelled attempt's terminal response is delivered back to us.
	p.world.localTo("service", CancelReqs{Call: t.Call})
	after(ProxyRetry{Call: t.Call}, p.backoff)
}

func (p *Proxy) finish(ok bool, why Why) {
	st := p.st
	if st == nil || st.done {
		return
	}
	st.done = true
	st.why = why
	id := AttemptID{Call: st.callNo, Proxy: st.proxyN}
	p.world.Ledger.Record(p.world.Now(), KindTerminal, id, why, "proxy")
}
