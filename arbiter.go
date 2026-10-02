package leaseaudit

import (
	"fmt"
	"sync"
	"time"
)

// ArbiterNodeID identifies the arbiter in the audit trail.
const ArbiterNodeID = "arbiter"

// RejectReason explains why a lease request was denied.
type RejectReason string

const (
	RejectHeldByOther RejectReason = "LEASE_HELD_BY_OTHER"
	RejectStaleTerm   RejectReason = "STALE_TERM"
	RejectNotHolder   RejectReason = "NOT_HOLDER"
	RejectExpired     RejectReason = "LEASE_EXPIRED"
)

// Response is the arbiter's verdict for one request.
type Response struct {
	Granted  bool
	Reason   RejectReason // set when Granted == false
	Holder   string       // current holder after the decision
	Term     uint64       // current term after the decision
	Expiry   time.Time
	EventSeq int64 // audit event recording this decision
}

// Lease is the arbiter's internal lease state.
type Lease struct {
	Holder   string
	Term     uint64
	Expiry   time.Time
	GrantSeq int64 // audit Seq of the event that installed this holder
}

func (l Lease) held(now time.Time) bool {
	return l.Holder != "" && now.Before(l.Expiry)
}

// Arbiter serializes lease decisions and audits every step.
//
// Concurrency model (mirrors real coordinators): a request is logged
// as arrived *before* it contends for the arbiter's decision lock, so
// the true arrival order of racing requests is preserved even when
// they are processed serially.
type Arbiter struct {
	log   *AuditLog
	clock *LamportClock
	now   func() time.Time

	decisionMu sync.Mutex // the arbiter decision lock (audited)
	lease      Lease

	stateMu    sync.Mutex
	lockHeldBy int64 // Seq of the arrival event holding the decision lock, -1 when free

	// decideHook, if set, runs while the decision lock is held, right
	// before the lease state is evaluated. Tests use it to force a
	// deterministic interleaving of racing requests.
	decideHook func()
	// arrivalHook, if set, runs after each arrival event is recorded.
	arrivalHook func(Event)
}

// Option configures an Arbiter.
type Option func(*Arbiter)

// WithClock injects a time source (useful for deterministic tests).
func WithClock(now func() time.Time) Option {
	return func(a *Arbiter) { a.now = now }
}

// WithDecideHook installs a hook executed under the decision lock.
func WithDecideHook(h func()) Option {
	return func(a *Arbiter) { a.decideHook = h }
}

// WithArrivalHook installs a hook executed after each arrival event.
func WithArrivalHook(h func(Event)) Option {
	return func(a *Arbiter) { a.arrivalHook = h }
}

func NewArbiter(log *AuditLog, opts ...Option) *Arbiter {
	a := &Arbiter{
		log:        log,
		clock:      &LamportClock{},
		now:        time.Now,
		lockHeldBy: -1,
	}
	for _, o := range opts {
		o(a)
	}
	return a
}

// arrive records the arrival of a request and detects race conflicts.
// It runs before the decision lock is taken.
func (a *Arbiter) arrive(kind, nodeID, leaseID string, reqLamport uint64, detail string) Event {
	lm := a.clock.Witness(reqLamport)
	arrival := a.log.Append(EventRequestArrived, lm, nodeID, leaseID, 0,
		fmt.Sprintf("%s %s", kind, detail))

	a.stateMu.Lock()
	holder := a.lockHeldBy
	a.stateMu.Unlock()
	if holder != -1 {
		// Another request is being arbitrated right now: the two
		// requests raced. The conflict event points at both arrivals,
		// establishing the causal evidence chain.
		a.log.Append(EventRaceConflict, a.clock.Tick(), ArbiterNodeID, leaseID, 0,
			fmt.Sprintf("request #%d arrived while request #%d is under arbitration", arrival.Seq, holder),
			holder, arrival.Seq)
	}
	if a.arrivalHook != nil {
		a.arrivalHook(arrival)
	}
	return arrival
}

// decide takes the decision lock (auditing the transition), runs fn,
// and audits the release.
func (a *Arbiter) decide(leaseID string, arrival Event, fn func() Event) Event {
	a.decisionMu.Lock()
	a.stateMu.Lock()
	a.lockHeldBy = arrival.Seq
	a.stateMu.Unlock()

	a.log.Append(EventLockAcquired, a.clock.Tick(), ArbiterNodeID, leaseID, 0,
		fmt.Sprintf("decision lock taken for request #%d", arrival.Seq), arrival.Seq)

	if a.decideHook != nil {
		a.decideHook()
	}
	outcome := fn()

	a.stateMu.Lock()
	a.lockHeldBy = -1
	a.stateMu.Unlock()
	a.decisionMu.Unlock()

	a.log.Append(EventLockReleased, a.clock.Tick(), ArbiterNodeID, leaseID, 0,
		fmt.Sprintf("decision lock released after request #%d", arrival.Seq), outcome.Seq)
	return outcome
}

// Acquire requests the lease. reqLamport is the caller node's Lamport
// timestamp for the send event, letting the arbiter merge clocks.
func (a *Arbiter) Acquire(nodeID, leaseID string, reqLamport uint64, ttl time.Duration) Response {
	arrival := a.arrive("ACQUIRE", nodeID, leaseID, reqLamport, fmt.Sprintf("ttl=%s", ttl))
	outcome := a.decide(leaseID, arrival, func() Event {
		now := a.now()
		switch {
		case !a.lease.held(now):
			if a.lease.Holder != "" {
				a.log.Append(EventLeaseExpired, a.clock.Tick(), ArbiterNodeID, leaseID, a.lease.Term,
					fmt.Sprintf("lease of %q expired at term %d", a.lease.Holder, a.lease.Term), arrival.Seq)
			}
			a.lease = Lease{Holder: nodeID, Term: a.lease.Term + 1, Expiry: now.Add(ttl)}
			ev := a.log.Append(EventLeaseGranted, a.clock.Tick(), ArbiterNodeID, leaseID, a.lease.Term,
				fmt.Sprintf("granted to %q until %s", nodeID, a.lease.Expiry.Format(time.RFC3339Nano)), arrival.Seq)
			a.lease.GrantSeq = ev.Seq
			return ev
		case a.lease.Holder == nodeID:
			a.lease.Expiry = now.Add(ttl)
			return a.log.Append(EventLeaseRenewed, a.clock.Tick(), ArbiterNodeID, leaseID, a.lease.Term,
				fmt.Sprintf("renewed by holder %q", nodeID), arrival.Seq)
		default:
			return a.log.Append(EventLeaseRejected, a.clock.Tick(), ArbiterNodeID, leaseID, a.lease.Term,
				fmt.Sprintf("rejected %q: %s (holder=%q term=%d)", nodeID, RejectHeldByOther, a.lease.Holder, a.lease.Term),
				arrival.Seq, a.lease.GrantSeq)
		}
	})
	return a.responseFrom(outcome)
}

// Renew extends the lease; only the current holder with the current
// term may renew.
func (a *Arbiter) Renew(nodeID, leaseID string, reqLamport uint64, term uint64, ttl time.Duration) Response {
	arrival := a.arrive("RENEW", nodeID, leaseID, reqLamport, fmt.Sprintf("term=%d ttl=%s", term, ttl))
	outcome := a.decide(leaseID, arrival, func() Event {
		now := a.now()
		switch {
		case !a.lease.held(now):
			if a.lease.Holder != "" {
				a.log.Append(EventLeaseExpired, a.clock.Tick(), ArbiterNodeID, leaseID, a.lease.Term,
					fmt.Sprintf("lease of %q expired at term %d", a.lease.Holder, a.lease.Term), arrival.Seq)
			}
			return a.log.Append(EventLeaseRejected, a.clock.Tick(), ArbiterNodeID, leaseID, a.lease.Term,
				fmt.Sprintf("rejected renew from %q: %s", nodeID, RejectExpired), arrival.Seq)
		case a.lease.Holder != nodeID:
			return a.log.Append(EventLeaseRejected, a.clock.Tick(), ArbiterNodeID, leaseID, a.lease.Term,
				fmt.Sprintf("rejected renew from %q: %s (holder=%q)", nodeID, RejectNotHolder, a.lease.Holder), arrival.Seq)
		case term != a.lease.Term:
			return a.log.Append(EventLeaseRejected, a.clock.Tick(), ArbiterNodeID, leaseID, a.lease.Term,
				fmt.Sprintf("rejected renew from %q: %s (got term %d, want %d)", nodeID, RejectStaleTerm, term, a.lease.Term),
				arrival.Seq)
		default:
			a.lease.Expiry = now.Add(ttl)
			return a.log.Append(EventLeaseRenewed, a.clock.Tick(), ArbiterNodeID, leaseID, a.lease.Term,
				fmt.Sprintf("renewed by %q until %s", nodeID, a.lease.Expiry.Format(time.RFC3339Nano)), arrival.Seq)
		}
	})
	return a.responseFrom(outcome)
}

// Handover atomically transfers the lease from the current holder to
// another node, fencing with the term.
func (a *Arbiter) Handover(from, to, leaseID string, reqLamport uint64, term uint64, ttl time.Duration) Response {
	arrival := a.arrive("HANDOVER", from, leaseID, reqLamport, fmt.Sprintf("to=%q term=%d", to, term))
	outcome := a.decide(leaseID, arrival, func() Event {
		now := a.now()
		switch {
		case !a.lease.held(now):
			return a.log.Append(EventLeaseRejected, a.clock.Tick(), ArbiterNodeID, leaseID, a.lease.Term,
				fmt.Sprintf("rejected handover from %q: %s", from, RejectExpired), arrival.Seq)
		case a.lease.Holder != from:
			return a.log.Append(EventLeaseRejected, a.clock.Tick(), ArbiterNodeID, leaseID, a.lease.Term,
				fmt.Sprintf("rejected handover from %q: %s (holder=%q)", from, RejectNotHolder, a.lease.Holder), arrival.Seq)
		case term != a.lease.Term:
			return a.log.Append(EventLeaseRejected, a.clock.Tick(), ArbiterNodeID, leaseID, a.lease.Term,
				fmt.Sprintf("rejected handover from %q: %s", from, RejectStaleTerm), arrival.Seq)
		default:
			a.lease = Lease{Holder: to, Term: a.lease.Term + 1, Expiry: now.Add(ttl)}
			ev := a.log.Append(EventLeaseHandover, a.clock.Tick(), ArbiterNodeID, leaseID, a.lease.Term,
				fmt.Sprintf("handed over %q -> %q, term bumped to %d", from, to, a.lease.Term), arrival.Seq)
			a.lease.GrantSeq = ev.Seq
			return ev
		}
	})
	return a.responseFrom(outcome)
}

// Sweep expires the lease if its TTL has elapsed. Coordinators call
// this periodically; it is audited like any other transition.
func (a *Arbiter) Sweep(leaseID string) bool {
	a.decisionMu.Lock()
	defer a.decisionMu.Unlock()
	if a.lease.Holder == "" || a.now().Before(a.lease.Expiry) {
		return false
	}
	a.log.Append(EventLeaseExpired, a.clock.Tick(), ArbiterNodeID, leaseID, a.lease.Term,
		fmt.Sprintf("sweeper expired lease of %q at term %d", a.lease.Holder, a.lease.Term))
	a.lease = Lease{Term: a.lease.Term} // keep the term monotonic (fencing)
	return true
}

// Snapshot returns the current lease state (for tests and demos).
func (a *Arbiter) Snapshot() Lease {
	a.decisionMu.Lock()
	defer a.decisionMu.Unlock()
	return a.lease
}

func (a *Arbiter) responseFrom(outcome Event) Response {
	a.decisionMu.Lock()
	defer a.decisionMu.Unlock()
	r := Response{Holder: a.lease.Holder, Term: a.lease.Term, Expiry: a.lease.Expiry, EventSeq: outcome.Seq}
	switch outcome.Kind {
	case EventLeaseGranted, EventLeaseRenewed, EventLeaseHandover:
		r.Granted = true
	}
	return r
}
