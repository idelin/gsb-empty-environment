package thermosim

import "time"

// Arbiter caps how many controllers may be On at once.
//
// The arbiter never forces a controller Off: capacity only frees when a
// controller's own thermostat switches Off, so an individual controller's
// minimum dwell time can never be interrupted by global arbitration. A
// controller that cannot be granted immediately stays pending; as capacity
// recovers, pending controllers are granted by Priority (smaller first) and
// then by wait sequence number (earlier first).
type Arbiter struct {
	MaxOn int

	seq  int64
	on   map[*Controller]bool
	wait []*waiter
}

type waiter struct {
	ctrl *Controller
	seq  int64
}

// NewArbiter returns an Arbiter allowing at most maxOn controllers On.
func NewArbiter(maxOn int) *Arbiter {
	return &Arbiter{MaxOn: maxOn, on: make(map[*Controller]bool)}
}

// OnCount reports how many controllers currently hold a grant.
func (a *Arbiter) OnCount() int { return len(a.on) }

// Waiting lists the pending controllers in the order they would be granted.
func (a *Arbiter) Waiting() []*Controller {
	sorted := make([]*waiter, len(a.wait))
	copy(sorted, a.wait)
	for i := 1; i < len(sorted); i++ {
		for j := i; j > 0 && less(sorted[j], sorted[j-1]); j-- {
			sorted[j], sorted[j-1] = sorted[j-1], sorted[j]
		}
	}
	out := make([]*Controller, len(sorted))
	for i, w := range sorted {
		out[i] = w.ctrl
	}
	return out
}

// request asks for a grant on behalf of c at time t. It grants immediately
// when capacity is free, otherwise it queues c as pending.
func (a *Arbiter) request(c *Controller, t time.Time) {
	if a.on[c] || c.pending {
		return
	}
	if len(a.on) < a.MaxOn {
		a.on[c] = true
		c.switchTo(On, t, ReasonGrant)
		return
	}
	a.seq++
	a.wait = append(a.wait, &waiter{ctrl: c, seq: a.seq})
	c.pending = true
}

// release frees c's grant and drains the wait queue at time t.
func (a *Arbiter) release(c *Controller, t time.Time) {
	if !a.on[c] {
		return
	}
	delete(a.on, c)
	a.drain(t)
}

// cancel removes a pending request. A pending controller holds no grant, so
// cancelling never frees capacity.
func (a *Arbiter) cancel(c *Controller) {
	for i, w := range a.wait {
		if w.ctrl == c {
			a.wait = append(a.wait[:i], a.wait[i+1:]...)
			c.pending = false
			return
		}
	}
}

// drain grants waiting controllers while capacity is free, in
// (Priority, wait sequence) order.
func (a *Arbiter) drain(t time.Time) {
	for len(a.on) < a.MaxOn && len(a.wait) > 0 {
		best := 0
		for i := 1; i < len(a.wait); i++ {
			if less(a.wait[i], a.wait[best]) {
				best = i
			}
		}
		w := a.wait[best]
		a.wait = append(a.wait[:best], a.wait[best+1:]...)
		a.on[w.ctrl] = true
		w.ctrl.pending = false
		w.ctrl.switchTo(On, t, ReasonGrantWait)
	}
}

func less(x, y *waiter) bool {
	if x.ctrl.Priority != y.ctrl.Priority {
		return x.ctrl.Priority < y.ctrl.Priority
	}
	return x.seq < y.seq
}
