package execctx

import "sync/atomic"

// Lease is an explicit authorization for asynchronous work to reenter the
// request after its originating synchronous frame has detached. Callbacks
// without a live lease are rejected once the request finishes.
type Lease struct {
	id      int64
	ctrl    *Controller
	frame   *Frame
	reserve int64
	active  atomic.Bool
}

// Detach hands the frame's remaining work to an asynchronous Lease. The frame
// is settled without returning its charge: the lease inherits the full charge
// as its reservation. reserve additionally budgets later lease entries and is
// charged immediately.
func (f *Frame) Detach(reserve int64) (*Lease, error) {
	if reserve < 0 {
		return nil, ErrInvalidCharge
	}
	c := f.ctrl
	c.mu.Lock()
	defer c.mu.Unlock()
	if f.state != StateActive {
		return nil, ErrSettled
	}
	if c.finished {
		return nil, ErrFinished
	}
	if c.canceled {
		return nil, mapEntryError(c.cause)
	}
	if f.lease != nil {
		return nil, ErrInvalidLease
	}
	if c.charged-c.refunded+reserve > c.budget {
		c.rejected++
		return nil, ErrBudgetExhausted
	}
	if !f.claimed.CompareAndSwap(false, true) {
		return nil, ErrSettled
	}
	c.charged += reserve
	c.leaseSeq++
	lease := &Lease{
		id:      c.leaseSeq,
		ctrl:    c,
		frame:   f,
		reserve: f.charge + reserve,
	}
	lease.active.Store(true)
	c.leases[lease.id] = lease

	f.state = StateDetached
	f.lease = lease
	f.refund = 0
	c.inflight--
	c.depth--
	return lease, nil
}

// Enter opens a frame on behalf of an asynchronous callback. The entry is
// charged first, exactly like a synchronous reentry, and is refused if the
// lease was released or the request finished.
func (l *Lease) Enter(amount int64) (*Frame, error) {
	c := l.ctrl
	c.mu.Lock()
	defer c.mu.Unlock()
	if !l.active.Load() {
		c.rejected++
		return nil, ErrInvalidLease
	}
	if err := c.admitLocked(nil, amount); err != nil {
		c.rejected++
		return nil, err
	}
	c.charged += amount
	c.inflight++
	c.entries++
	c.frameSeq++
	f := &Frame{
		id:     c.frameSeq,
		ctrl:   c,
		lease:  l,
		charge: amount,
		depth:  1,
	}
	return f, nil
}

// Release returns the reservation and invalidates the lease. It is idempotent:
// a second release returns ErrInvalidLease and moves no money.
func (l *Lease) Release() error {
	c := l.ctrl
	c.mu.Lock()
	defer c.mu.Unlock()
	if !l.active.Load() {
		return ErrInvalidLease
	}
	l.active.Store(false)
	c.refunded += l.reserve
	delete(c.leases, l.id)
	return nil
}

// ID identifies the lease within its controller.
func (l *Lease) ID() int64 { return l.id }

// Reserve is the amount held until the lease is released or revoked.
func (l *Lease) Reserve() int64 { return l.reserve }

// Active reports whether the lease still authorizes callbacks.
func (l *Lease) Active() bool { return l.active.Load() }
