package execctx

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

// Config describes a request's resource envelope.
type Config struct {
	// Budget is the total amount that may remain charged at any moment.
	Budget int64
	// Deadline, when non-zero, cancels every frame at the given instant.
	Deadline time.Time
	// MaxDepth bounds synchronous nesting. Zero means unbounded.
	MaxDepth int
	// Clock defaults to the wall clock.
	Clock Clock
}

// FrameState labels the lifecycle stage of a frame.
type FrameState int

const (
	StateActive FrameState = iota
	StateCompleted
	StateFailed
	StateDetached
)

func (s FrameState) String() string {
	switch s {
	case StateActive:
		return "active"
	case StateCompleted:
		return "completed"
	case StateFailed:
		return "failed"
	case StateDetached:
		return "detached"
	default:
		return "unknown"
	}
}

// EndEvent is published exactly once per Controller, when Finish runs.
type EndEvent struct {
	Charged      int64
	Refunded     int64
	Inflight     int
	ActiveLeases int
	Canceled     bool
	Cause        error
	At           time.Time
}

// Controller is the shared execution context for one request. It is safe for
// concurrent use.
type Controller struct {
	mu        sync.Mutex
	clock     Clock
	budget    int64
	charged   int64
	refunded  int64
	entries   int64
	rejected  int64
	maxDepth  int
	depth     int
	deadline  time.Time
	deadTimer Timer

	done       chan struct{}
	cancelOnce sync.Once
	canceled   bool
	cause      error

	finished bool
	end      chan EndEvent
	inflight int
	leases   map[int64]*Lease
	leaseSeq int64
	frameSeq int64
}

// New builds the request Controller. An already-past deadline leaves the
// controller born canceled with ErrDeadlineExceeded.
func New(cfg Config) *Controller {
	clock := cfg.Clock
	if clock == nil {
		clock = wallClock{}
	}
	c := &Controller{
		clock:    clock,
		budget:   cfg.Budget,
		maxDepth: cfg.MaxDepth,
		deadline: cfg.Deadline,
		done:     make(chan struct{}),
		end:      make(chan EndEvent, 1),
		leases:   make(map[int64]*Lease),
	}
	if !cfg.Deadline.IsZero() {
		if !clock.Now().Before(cfg.Deadline) {
			c.markCancelLocked(ErrDeadlineExceeded)
		} else {
			c.deadTimer = clock.AfterFunc(cfg.Deadline.Sub(clock.Now()), func() {
				c.Cancel(ErrDeadlineExceeded)
			})
		}
	}
	return c
}

// Cancel marks the request canceled. It is idempotent and host-side only; no
// frame API can undo it. A nil cause becomes ErrCanceled.
func (c *Controller) Cancel(cause error) {
	c.cancelOnce.Do(func() {
		if cause == nil {
			cause = ErrCanceled
		}
		c.mu.Lock()
		c.markCancelLocked(cause)
		c.mu.Unlock()
	})
}

func (c *Controller) markCancelLocked(cause error) {
	if c.canceled {
		return
	}
	c.canceled = true
	c.cause = cause
	if c.deadTimer != nil {
		c.deadTimer.Stop()
		c.deadTimer = nil
	}
	close(c.done)
}

// Enter opens a top-level (parent == nil) or nested frame, charging amount
// before any plugin code runs.
func (c *Controller) Enter(parent *Frame, amount int64) (*Frame, error) {
	if parent != nil && parent.ctrl != c {
		return nil, ErrReentered
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.admitLocked(parent, amount); err != nil {
		c.rejected++
		return nil, err
	}
	c.charged += amount
	c.inflight++
	c.depth++
	c.entries++
	c.frameSeq++
	return &Frame{
		id:     c.frameSeq,
		ctrl:   c,
		parent: parent,
		charge: amount,
		depth:  c.depth,
	}, nil
}

func (c *Controller) admitLocked(parent *Frame, amount int64) error {
	if amount <= 0 {
		return ErrInvalidCharge
	}
	if c.finished {
		return ErrFinished
	}
	if c.canceled {
		return mapEntryError(c.cause)
	}
	if !c.deadline.IsZero() && !c.clock.Now().Before(c.deadline) {
		c.markCancelLocked(ErrDeadlineExceeded)
		return ErrDeadlineExceeded
	}
	if parent != nil {
		if parent.state != StateActive || parent.lease != nil {
			return ErrReentered
		}
	}
	if c.maxDepth > 0 && c.depth >= c.maxDepth {
		return ErrMaxDepth
	}
	if c.charged-c.refunded+amount > c.budget {
		return ErrBudgetExhausted
	}
	return nil
}

func mapEntryError(err error) error {
	if errors.Is(err, ErrDeadlineExceeded) {
		return ErrDeadlineExceeded
	}
	return ErrCanceled
}

// settleFrame performs the single allowed state transition of a frame. Every
// settlement path is funneled here so refunds and counters stay consistent.
func (c *Controller) settleFrame(f *Frame, state FrameState, cause error, refund int64) error {
	if refund < 0 || refund > f.charge {
		return ErrBadRefund
	}
	if !f.claimed.CompareAndSwap(false, true) {
		return ErrSettled
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	f.state = state
	f.cause = cause
	f.refund = refund
	c.refunded += refund
	c.inflight--
	if f.lease == nil {
		c.depth--
	}
	return nil
}

// Finish ends the request. Outstanding leases are revoked and their
// reservations refunded; in-flight frames keep running but every new entry is
// rejected. The EndEvent is delivered exactly once.
func (c *Controller) Finish() EndEvent {
	c.mu.Lock()
	if c.finished {
		ev := <-c.end
		c.end <- ev
		c.mu.Unlock()
		return ev
	}
	c.finished = true
	for _, l := range c.leases {
		if l.active.Load() {
			l.active.Store(false)
			c.refunded += l.reserve
			delete(c.leases, l.id)
		}
	}
	ev := EndEvent{
		Charged:      c.charged,
		Refunded:     c.refunded,
		Inflight:     c.inflight,
		ActiveLeases: len(c.leases),
		Canceled:     c.canceled,
		Cause:        c.cause,
		At:           c.clock.Now(),
	}
	c.mu.Unlock()

	c.end <- ev
	return ev
}

// End returns the buffered channel carrying the single EndEvent.
func (c *Controller) End() <-chan EndEvent { return c.end }

// Budget returns the configured total budget.
func (c *Controller) Budget() int64 { return c.budget }

// Charged is the cumulative amount charged across all admitted entries.
func (c *Controller) Charged() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.charged
}

// Refunded is the cumulative amount returned by failures, refunds and leases.
func (c *Controller) Refunded() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.refunded
}

// Consumed returns the amount still held: Charged minus Refunded.
func (c *Controller) Consumed() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.charged - c.refunded
}

// Entries counts admitted frames; Rejected counts entry attempts turned away.
func (c *Controller) Entries() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.entries
}

// Rejected counts entry attempts that failed admission.
func (c *Controller) Rejected() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.rejected
}

// Inflight returns the number of currently open frames.
func (c *Controller) Inflight() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.inflight
}

// ActiveLeases returns the number of leases that still authorize callbacks.
func (c *Controller) ActiveLeases() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.leases)
}

// Canceled reports whether the request has been canceled.
func (c *Controller) Canceled() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.canceled
}

// CancelCause returns the cancellation cause, or nil.
func (c *Controller) CancelCause() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cause
}

// Finished reports whether Finish has run.
func (c *Controller) Finished() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.finished
}

// Deadline returns the shared deadline, or the zero time.
func (c *Controller) Deadline() (time.Time, bool) {
	return c.deadline, !c.deadline.IsZero()
}

// Run executes fn inside a new child frame. Panics are contained and the frame
// failed with *PanicError and fully refunded.
func (c *Controller) Run(parent *Frame, amount int64, fn func(*Frame) error) (err error) {
	f, err := c.Enter(parent, amount)
	if err != nil {
		return err
	}
	defer func() {
		if r := recover(); r != nil {
			pe := &PanicError{Value: r}
			_ = f.Fail(pe, f.charge)
			err = pe
		}
	}()
	if err = fn(f); err != nil {
		_ = f.Fail(err, f.charge)
		return err
	}
	if f.State() == StateActive {
		_ = f.Complete(0)
	}
	return nil
}

// Frame is one charged invocation of a plugin. Frames implement context.Context
// but deliberately expose no cancellation method.
type Frame struct {
	id      int64
	ctrl    *Controller
	parent  *Frame
	lease   *Lease
	charge  int64
	refund  int64
	depth   int
	state   FrameState
	cause   error
	claimed atomic.Bool
}

var _ context.Context = (*Frame)(nil)

// Complete settles a successful frame. refund may return part of the charge;
// it must not exceed the charged amount.
func (f *Frame) Complete(refund int64) error {
	return f.ctrl.settleFrame(f, StateCompleted, nil, refund)
}

// Fail settles a failed frame, refunding up to f.Charge(). A nil cause becomes
// ErrPluginFailed.
func (f *Frame) Fail(cause error, refund int64) error {
	if cause == nil {
		cause = ErrPluginFailed
	}
	return f.ctrl.settleFrame(f, StateFailed, cause, refund)
}

// Done returns the shared request cancellation channel.
func (f *Frame) Done() <-chan struct{} { return f.ctrl.done }

// Err maps the shared cancellation cause onto context package errors. It
// reports nil while the request is alive even after the frame settles.
func (f *Frame) Err() error {
	f.ctrl.mu.Lock()
	defer f.ctrl.mu.Unlock()
	if !f.ctrl.canceled {
		return nil
	}
	return mapContextError(f.ctrl.cause)
}

// Deadline reports the shared request deadline.
func (f *Frame) Deadline() (time.Time, bool) {
	return f.ctrl.Deadline()
}

// Value returns nil; plugin frames carry no scoped values by design.
func (f *Frame) Value(any) any { return nil }

func mapContextError(err error) error {
	switch err {
	case ErrDeadlineExceeded:
		return context.DeadlineExceeded
	default:
		return context.Canceled
	}
}

// ID identifies the frame within its controller.
func (f *Frame) ID() int64 { return f.id }

// Charge is the amount deducted on entry.
func (f *Frame) Charge() int64 { return f.charge }

// Depth is the synchronous nesting level, starting at one.
func (f *Frame) Depth() int { return f.depth }

// State reports the current lifecycle state.
func (f *Frame) State() FrameState {
	f.ctrl.mu.Lock()
	defer f.ctrl.mu.Unlock()
	return f.state
}

// Cause reports the failure cause, or nil.
func (f *Frame) Cause() error {
	f.ctrl.mu.Lock()
	defer f.ctrl.mu.Unlock()
	return f.cause
}
