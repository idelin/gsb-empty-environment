package hlc

import (
	"context"
	"sync"
	"time"
)

func withOpID(ctx context.Context, id uint64) context.Context {
	return context.WithValue(ctx, opIDKey{}, id)
}

func opIDFrom(ctx context.Context) uint64 {
	if v, ok := ctx.Value(opIDKey{}).(uint64); ok {
		return v
	}
	return 0
}

// parkedOp identifies one goroutine visit blocked inside a scheduler Hook.
type parkedOp struct {
	opID uint64
	step string
}

// visit is one concrete Hook parking event; visitID disambiguates repeated
// visits of the same (opID, step), e.g. overflow retries.
type visit struct {
	key     parkedOp
	visitID uint64
}

// Controller is a deterministic scheduler for clock goroutines.
//
// Goroutines block at every step listed in ParkedSteps. Tests wait for a
// specific (opID, step) visit with WaitParkedOp and release it explicitly.
// Repeated visits of the same (opID, step), such as overflow retries, are
// tracked as distinct visits so no signal is ever overwritten.
type Controller struct {
	mu sync.Mutex

	parked   []visit
	waiters  map[visit]chan struct{}
	counters map[parkedOp]uint64

	ParkedSteps map[string]bool
}

// NewController builds a controller parking at the given hook steps.
func NewController(parkedSteps ...string) *Controller {
	ctl := &Controller{
		waiters:     make(map[visit]chan struct{}),
		counters:    make(map[parkedOp]uint64),
		ParkedSteps: make(map[string]bool),
	}
	for _, s := range parkedSteps {
		ctl.ParkedSteps[s] = true
	}
	return ctl
}

// Hook is the function to pass to Config.Hook.
func (c *Controller) Hook(ctx context.Context, _ uint64, step string) {
	if !c.ParkedSteps[step] {
		return
	}
	// Only operations explicitly tagged by the test are scheduled; untagged
	// calls (e.g. setup traffic) pass straight through.
	if _, ok := ctx.Value(opIDKey{}).(uint64); !ok {
		return
	}
	key := parkedOp{opID: opIDFrom(ctx), step: step}

	c.mu.Lock()
	c.counters[key]++
	v := visit{key: key, visitID: c.counters[key]}
	ch := make(chan struct{})
	c.waiters[v] = ch
	c.parked = append(c.parked, v)
	c.mu.Unlock()

	<-ch
}

// Release wakes the FIFO-head caller parked on step and reports whether one
// was woken.
func (c *Controller) Release(step string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i, p := range c.parked {
		if p.key.step == step {
			c.parked = append(c.parked[:i], c.parked[i+1:]...)
			ch := c.waiters[p]
			delete(c.waiters, p)
			close(ch)
			return true
		}
	}
	return false
}

// ReleaseOp wakes the specific parked (opID, step) caller and reports success.
func (c *Controller) ReleaseOp(opID uint64, step string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	key := parkedOp{opID: opID, step: step}
	for i, p := range c.parked {
		if p.key == key {
			c.parked = append(c.parked[:i], c.parked[i+1:]...)
			close(c.waiters[p])
			delete(c.waiters, p)
			return true
		}
	}
	return false
}

// DriveWait releases a parked StepBeforeWait caller so it enters the physical
// wait, then advances the fake clock by advanceMs once it is guaranteed to be
// suspended in WaitUntil (the StepAfterWait hook fires only after the wait
// returns). The caller must park StepAfterWait and release it afterwards.
func (c *Controller) DriveWait(fc *FakeClock, opID uint64, advanceMs int64) {
	if !c.ReleaseOp(opID, StepBeforeWait) {
		return
	}
	// Give the goroutine a moment to enter FakeClock.WaitUntil. The wait is
	// the only possible next action after the before_wait hook returns.
	deadline := time.Now().Add(time.Second)
	for !fc.HasWaiter() && time.Now().Before(deadline) {
		time.Sleep(100 * time.Microsecond)
	}
	fc.Advance(advanceMs)
}

// ReleaseAll wakes every parked call matching step and returns the count.
func (c *Controller) ReleaseAll(step string) int {
	n := 0
	for c.Release(step) {
		n++
	}
	return n
}

// Parked reports whether any call matching step is currently parked.
func (c *Controller) Parked(step string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, p := range c.parked {
		if p.key.step == step {
			return true
		}
	}
	return false
}

// ParkedOp reports whether (opID, step) is currently parked.
func (c *Controller) ParkedOp(opID uint64, step string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, p := range c.parked {
		if p.key.opID == opID && p.key.step == step {
			return true
		}
	}
	return false
}

// ParkedAt returns the (opID, step) pairs currently blocked, in FIFO order.
func (c *Controller) ParkedAt() []parkedOp {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]parkedOp, len(c.parked))
	for i, v := range c.parked {
		out[i] = v.key
	}
	return out
}

// WaitFor blocks until any call matching step parks, or times out.
func (c *Controller) WaitFor(step string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if c.Parked(step) {
			return true
		}
		time.Sleep(time.Millisecond)
	}
	return c.Parked(step)
}

// WaitParkedOp blocks until the specific (opID, step) call parks, or times out.
func (c *Controller) WaitParkedOp(opID uint64, step string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if c.ParkedOp(opID, step) {
			return true
		}
		time.Sleep(time.Millisecond)
	}
	return c.ParkedOp(opID, step)
}

// Quiescent reports whether no goroutine is parked.
func (c *Controller) Quiescent() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.parked) == 0
}

// WaitQuiescent blocks until Quiescent is true or the timeout elapses.
func (c *Controller) WaitQuiescent(timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if c.Quiescent() {
			return true
		}
		time.Sleep(time.Millisecond)
	}
	return c.Quiescent()
}
