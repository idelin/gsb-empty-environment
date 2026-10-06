package thermosim

// Arbiter shares a global maximum number of simultaneously-On
// controllers. It only grants and releases capacity; it never forces a
// controller Off, so a controller's minimum dwell time can never be
// interrupted by global arbitration. Demand beyond capacity waits,
// ordered by priority and then by request sequence number.
type Arbiter struct {
	MaxOn int

	onCount int
	seq     int64
	waiting []*Controller // sorted by (Priority, reqSeq), head is served first
}

// NewArbiter creates an arbiter allowing at most maxOn controllers On.
func NewArbiter(maxOn int) *Arbiter { return &Arbiter{MaxOn: maxOn} }

// Attach registers c with the arbiter using the given priority
// (smaller values are served first; ties break by request order).
func (a *Arbiter) Attach(c *Controller, priority int) {
	c.arbiter = a
	c.Priority = priority
}

// OnCount returns how many controllers currently hold a grant.
func (a *Arbiter) OnCount() int { return a.onCount }

// Waiting returns the names of pending controllers in service order.
func (a *Arbiter) Waiting() []string {
	names := make([]string, len(a.waiting))
	for i, c := range a.waiting {
		names[i] = c.Name
	}
	return names
}

// request asks for capacity to switch c On at time now. It grants
// immediately when a slot is free, otherwise it queues c (once) and
// reports that the request stays pending.
func (a *Arbiter) request(c *Controller, now int64) bool {
	if c.hasGrant {
		return true
	}
	if a.onCount < a.MaxOn {
		a.grant(c, now)
		return true
	}
	if !c.pending {
		c.pending = true
		c.reqSeq = a.seq
		a.seq++
		a.enqueue(c)
	}
	return false
}

func (a *Arbiter) enqueue(c *Controller) {
	i := 0
	for i < len(a.waiting) && less(a.waiting[i], c) {
		i++
	}
	a.waiting = append(a.waiting, nil)
	copy(a.waiting[i+1:], a.waiting[i:])
	a.waiting[i] = c
}

func less(x, y *Controller) bool {
	if x.Priority != y.Priority {
		return x.Priority < y.Priority
	}
	return x.reqSeq < y.reqSeq
}

// withdraw cancels a pending request (local safety: the latest sample
// no longer demands On).
func (a *Arbiter) withdraw(c *Controller) {
	if !c.pending {
		return
	}
	c.pending = false
	for i, w := range a.waiting {
		if w == c {
			a.waiting = append(a.waiting[:i], a.waiting[i+1:]...)
			return
		}
	}
}

// release returns c's grant and serves waiting requests in order.
func (a *Arbiter) release(c *Controller, now int64) {
	if !c.hasGrant {
		return
	}
	c.hasGrant = false
	a.onCount--
	a.drain(now)
}

// drain grants freed slots to waiting controllers in (priority, seq)
// order. Waiting entries always belong to controllers whose latest
// sample still demands On, so granting is locally safe.
func (a *Arbiter) drain(now int64) {
	for a.onCount < a.MaxOn && len(a.waiting) > 0 {
		c := a.waiting[0]
		a.waiting = a.waiting[1:]
		a.grant(c, now)
	}
}

func (a *Arbiter) grant(c *Controller, now int64) {
	c.pending = false
	c.hasGrant = true
	a.onCount++
	c.switchTo(On, now)
}
