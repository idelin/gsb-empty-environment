package retry

import (
	"context"
	"errors"

	"retrybudget/sim"
)

// Policy configures the two nested retry layers.
//
// ProxyBranches is the UPPER layer: independent hedged proxy attempts. Each
// branch then runs the LOWER layer: up to ReplicaAttempts sequential tries.
// HedgeDelay staggers branch starts (0 launches all branches at t=0).
// ReplicaBackoff is the fixed delay between lower-layer tries.
type Policy struct {
	ProxyBranches   int
	ReplicaAttempts int
	HedgeDelay      int64
	ReplicaBackoff  int64
}

// AttemptInfo is one actual attempt made by either layer.
type AttemptInfo struct {
	ID             uint64
	Branch         int
	ReplicaAttempt int
}

// Result is the final, immutable outcome of a call.
type Result struct {
	Err         error
	OkAt        int64
	Success     AttemptInfo
	Attempts    []AttemptInfo
	ByBranch    map[int]int
	DeadlineHit bool

	// Ground-truth transport counters for the whole call.
	Sent           int
	DeniedBudget   int
	DeniedDeadline int
	DeniedCancel   int
}

type attempt struct {
	info AttemptInfo
}

type branch struct {
	ctl   *sim.Ctl
	live  bool
	tries int
	cur   *attempt
}

type call struct {
	s      *sim.Sim
	tr     *Transport
	pol    Policy
	deadAt int64
	root   *sim.Ctl

	done     bool
	finishAt int64
	res      *Result

	branches []*branch
	liveN    int
}

// Execute runs one call under a shared budget/deadline and returns once the
// simulation reaches its terminal state. budget is the total number of wire
// requests allowed for the ENTIRE call (all branches and all retries combined).
func Execute(s *sim.Sim, pol Policy, budget int, deadline int64, behave Behavior) *Result {
	tr := NewTransport(s, budget, deadline, behave)
	return executeWithTransport(s, pol, deadline, tr)
}

// executeWithTransport wires a call onto an externally provided chokepoint.
// Tests use it to inspect the transport's ground-truth send records.
func executeWithTransport(s *sim.Sim, pol Policy, deadline int64, tr *Transport) *Result {
	c := &call{
		s:        s,
		tr:       tr,
		pol:      pol,
		deadAt:   deadline,
		root:     sim.RootCtl(s),
		branches: make([]*branch, pol.ProxyBranches),
		res:      &Result{ByBranch: map[int]int{}, Err: errors.New("retrybudget: no attempt was started")},
	}

	for b := 0; b < pol.ProxyBranches; b++ {
		b := b
		s.After(int64(b)*pol.HedgeDelay, func(*sim.Sim) {
			if c.done {
				return
			}
			c.startBranch(b)
		})
	}
	if deadline >= 0 {
		s.After(max64(0, deadline-s.Now()), func(*sim.Sim) {
			if !c.done {
				c.finish(context.DeadlineExceeded, s.Now())
			}
		})
	}

	s.Run()

	// Liveness guard: if the queue drains while requests are still in flight
	// (e.g. a fully silent peer with no deadline), the call must still reach a
	// terminal state rather than hanging forever.
	if !c.done {
		c.finish(errors.New("retrybudget: queue drained with in-flight requests"), s.Now())
	}

	c.res.Sent = tr.Sent
	c.res.DeniedBudget = tr.DeniedBudget
	c.res.DeniedDeadline = tr.DeniedDeadline
	c.res.DeniedCancel = tr.DeniedCancel
	c.res.DeadlineHit = errors.Is(c.res.Err, context.DeadlineExceeded)
	return c.res
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func (c *call) startBranch(idx int) {
	b := &branch{ctl: c.root.Child(nil), live: true}
	c.branches[idx] = b
	c.liveN++
	c.nextTry(idx)
}

// nextTry starts the next lower-layer (replica) attempt of a branch.
func (c *call) nextTry(idx int) {
	b := c.branches[idx]
	if c.done || !b.live || b.tries >= c.pol.ReplicaAttempts {
		c.stopBranch(idx)
		return
	}
	b.tries++
	tryNum := b.tries

	att := &attempt{info: AttemptInfo{Branch: idx, ReplicaAttempt: tryNum}}
	b.cur = att

	id, sentAt, err := c.tr.Send(idx, tryNum, b.ctl, func(replyErr error) {
		c.onReply(idx, att, replyErr)
	})
	att.info.ID = id

	// Rejected at the chokepoint: the request never left.
	if err != nil {
		if errors.Is(err, ErrBudgetExceeded) {
			// Global resource exhausted: the shared budget protects the whole
			// call, so tear everything down now instead of burning more events.
			c.finish(ErrBudgetExceeded, sentAt)
			return
		}
		// Canceled / deadline: retire this branch exactly once.
		c.stopBranch(idx)
		return
	}

	c.res.Attempts = append(c.res.Attempts, att.info)
	c.res.ByBranch[idx]++
}

func (c *call) onReply(idx int, att *attempt, replyErr error) {
	// SAFETY: once the call is terminal, every late response is ignored and
	// cannot mutate the result.
	if c.done {
		return
	}
	b := c.branches[idx]
	if b == nil || !b.live || b.cur != att {
		return
	}
	b.cur = nil

	if replyErr == nil {
		c.res.Success = att.info
		c.finish(nil, c.s.Now())
		return
	}

	// Failure: lower layer retries after backoff until its local cap, then the
	// upper-layer branch gives up.
	if b.tries < c.pol.ReplicaAttempts {
		c.s.After(c.pol.ReplicaBackoff, func(*sim.Sim) {
			if c.done {
				return
			}
			// A canceled branch must not resume.
			if b := c.branches[idx]; b == nil || !b.live {
				return
			}
			c.nextTry(idx)
		})
	} else {
		c.stopBranch(idx)
	}
}

func (c *call) stopBranch(idx int) {
	b := c.branches[idx]
	if b == nil || !b.live {
		return
	}
	b.live = false
	if b.ctl != nil {
		b.ctl.Cancel("branch exhausted")
	}
	c.liveN--
	if c.liveN == 0 && !c.done {
		c.finish(errors.New("retrybudget: all attempts failed"), c.s.Now())
	}
}

// finish is the one and only terminal transition.
func (c *call) finish(err error, at int64) {
	if c.done {
		return
	}
	c.done = true
	c.finishAt = at
	c.res.Err = err
	c.res.OkAt = at

	reason := "call finished"
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			reason = "deadline exceeded"
		} else {
			reason = "call failed"
		}
	}
	// Synchronous propagation: cancel every still-live branch and in-flight
	// request. Their late replies become no-ops because c.done is already true.
	c.root.Cancel(reason)
}
