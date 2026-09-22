package retry

import (
	"context"
	"errors"

	"retrybudget/sim"
)

// ErrBudgetExceeded means the single shared request budget for the whole call
// is gone. It is a terminal condition: retrying would violate the budget.
var ErrBudgetExceeded = errors.New("retrybudget: shared request budget exhausted")

// RequestSpec describes one wire request handed to the deterministic Behavior.
type RequestSpec struct {
	ID             uint64
	Branch         int // proxy branch (hedge)
	ReplicaAttempt int // 1-based attempt within that branch's replica retry loop
	SentAt         int64
}

// Decision is the scripted fate of a request: a reply after Delay carrying Err
// (nil == success), or Silent (no reply at all until the request is canceled).
type Decision struct {
	Delay  int64
	Err    error
	Silent bool
}

// Behavior scripts the network/server. It is pure and deterministic.
type Behavior func(RequestSpec) Decision

// SendRecord is the ground-truth log of one request that actually left.
type SendRecord struct {
	ID             uint64
	Branch         int
	ReplicaAttempt int
	SentAt         int64
	ResolvedAt     int64
	Canceled       bool
	CancelReason   string
	ReplyErr       error
	resolved       bool
}

// Transport is the ONLY place a request can be emitted. Every layer and every
// branch goes through Send, so one budget and one deadline bound the total
// number of real requests regardless of how retries nest or fan out.
type Transport struct {
	s          *sim.Sim
	behave     Behavior
	remaining  int
	deadlineAt int64 // absolute virtual time; -1 disables the deadline

	// Sent counts requests actually emitted (budget consumed).
	Sent int
	// Denials were rejected before emission, so they never consume budget.
	DeniedBudget   int
	DeniedDeadline int
	DeniedCancel   int

	recs []*SendRecord
}

// NewTransport creates the chokepoint. budget <= 0 means no request may ever
// be sent. deadlineAt is absolute virtual time; pass -1 for no deadline.
func NewTransport(s *sim.Sim, budget int, deadlineAt int64, behave Behavior) *Transport {
	return &Transport{s: s, behave: behave, remaining: budget, deadlineAt: deadlineAt}
}

// Records returns the ordered log of emitted requests.
func (t *Transport) Records() []SendRecord {
	out := make([]SendRecord, len(t.recs))
	for i, r := range t.recs {
		out[i] = *r
	}
	return out
}

// Send tries to emit one request. It returns the unique attempt id and send
// time on success, with reply invoked exactly once later (a cancel hook
// guarantees the reply even when the peer is Silent). On rejection it returns
// one of ErrBudgetExceeded, context.DeadlineExceeded or context.Canceled and
// reply is never called.
func (t *Transport) Send(branch, replicaAttempt int, parent *sim.Ctl, reply func(error)) (uint64, int64, error) {
	now := t.s.Now()
	id := t.s.NextID()

	// Gate 1: the caller (or its parent call) is already canceled.
	if parent.Canceled() {
		t.DeniedCancel++
		return id, now, context.Canceled
	}
	// Gate 2: the shared deadline has elapsed. ">=" makes t==deadline too
	// late: nothing is ever emitted at or after the cutoff.
	if t.deadlineAt >= 0 && now >= t.deadlineAt {
		t.DeniedDeadline++
		return id, now, context.DeadlineExceeded
	}
	// Gate 3: shared budget across every layer and branch.
	if t.remaining <= 0 {
		t.DeniedBudget++
		return id, now, ErrBudgetExceeded
	}

	t.remaining--
	t.Sent++
	rec := &SendRecord{ID: id, Branch: branch, ReplicaAttempt: replicaAttempt, SentAt: now}
	t.recs = append(t.recs, rec)

	// The cancel hook is the sole resolver for a Silent peer and a backstop
	// for races: whichever of {reply event, cancel} happens first resolves the
	// request exactly once.
	ctl := parent.Child(func(reason string) {
		if rec.resolve(t.s.Now(), context.Canceled, true, reason) {
			reply(context.Canceled)
		}
	})

	d := t.behave(RequestSpec{ID: id, Branch: branch, ReplicaAttempt: replicaAttempt, SentAt: now})
	if d.Silent {
		return id, now, nil
	}
	// Deferred even at Delay==0, so emission never re-enters the caller.
	t.s.After(d.Delay, func(*sim.Sim) {
		if ctl.Canceled() {
			return // hook already resolved it
		}
		if rec.resolve(t.s.Now(), d.Err, false, "") {
			reply(d.Err)
		}
	})
	return id, now, nil
}

// resolve records the final fate of a request and reports whether this caller
// won the right to deliver the reply. Single-goroutine: no locking needed.
func (r *SendRecord) resolve(at int64, err error, canceled bool, reason string) bool {
	if r.resolved {
		return false
	}
	r.resolved = true
	r.ResolvedAt = at
	r.ReplyErr = err
	r.Canceled = canceled
	r.CancelReason = reason
	return true
}
