// Package pausecoord implements a pause/resume coordination protocol.
//
// A Coordinator lets a controller pause a set of workers at safe points so
// that a consistent snapshot can be taken. The protocol is epoch-based:
// every Pause starts a new epoch, and confirmations are only valid for the
// epoch they were issued in, so stale confirmations from an older pause can
// never be mistaken for a complete confirmation of the current one.
//
// Workers may leave at any time (including while paused); a leaving worker
// counts as confirmed. New workers that Join while a pause is in effect
// block until Resume.
package pausecoord

import (
	"context"
	"errors"
	"sync"
)

// ErrPauseInProgress is returned by Pause when a pause is already in effect.
var ErrPauseInProgress = errors.New("pausecoord: pause already in progress")

// Coordinator coordinates pausing and resuming a dynamic set of workers.
type Coordinator struct {
	mu      sync.Mutex
	changed *sync.Cond

	epoch   uint64 // current pause epoch; incremented on every Pause
	pausing bool   // a pause is in effect (workers must stop at safe points)

	workers map[uint64]struct{} // registered workers
	pending map[uint64]struct{} // workers that have not confirmed this epoch

	nextID uint64
}

// New returns a ready-to-use Coordinator.
func New() *Coordinator {
	c := &Coordinator{
		workers: make(map[uint64]struct{}),
		pending: make(map[uint64]struct{}),
	}
	c.changed = sync.NewCond(&c.mu)
	return c
}

// Join registers a new worker and returns its ID. If a pause is in effect,
// Join blocks until Resume (or until the pause is aborted).
func (c *Coordinator) Join() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	for c.pausing {
		c.changed.Wait()
	}
	c.nextID++
	id := c.nextID
	c.workers[id] = struct{}{}
	return id
}

// Leave deregisters a worker. If a pause is waiting on this worker, leaving
// counts as its confirmation.
func (c *Coordinator) Leave(id uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.workers, id)
	delete(c.pending, id)
	c.changed.Broadcast()
}

// SafePoint is called by a worker at a point where it is safe to pause. If
// no pause is in effect it returns immediately. Otherwise it confirms the
// worker's participation in the current epoch and blocks until Resume (or
// until the pause is aborted).
func (c *Coordinator) SafePoint(id uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.pausing {
		return
	}
	c.confirmLocked(id, c.epoch)
	for c.pausing {
		c.changed.Wait()
	}
}

// Confirm records an asynchronous confirmation for the given epoch, e.g. one
// delivered over a channel from a remote worker. Confirmations for anything
// other than the current, in-progress epoch are stale and ignored.
func (c *Coordinator) Confirm(id uint64, epoch uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.confirmLocked(id, epoch)
}

func (c *Coordinator) confirmLocked(id uint64, epoch uint64) {
	if !c.pausing || epoch != c.epoch {
		return // stale or no pause in progress
	}
	if _, ok := c.pending[id]; !ok {
		return // unknown worker or already confirmed
	}
	delete(c.pending, id)
	c.changed.Broadcast()
}

// Pause starts a new pause epoch and blocks until every registered worker
// has confirmed (via SafePoint, Confirm, or Leave) or ctx is done. On
// success it returns the epoch number; the caller may then read a consistent
// snapshot and must call Resume to release the workers. If ctx is done
// first, the pause is aborted, blocked workers are released, and ctx.Err()
// is returned.
func (c *Coordinator) Pause(ctx context.Context) (uint64, error) {
	c.mu.Lock()
	if c.pausing {
		c.mu.Unlock()
		return 0, ErrPauseInProgress
	}
	c.epoch++
	c.pausing = true
	c.pending = make(map[uint64]struct{}, len(c.workers))
	for id := range c.workers {
		c.pending[id] = struct{}{}
	}
	epoch := c.epoch

	// Wake the wait loop when ctx is done. The goroutine exits when Pause
	// returns and closes done.
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			c.mu.Lock()
			c.changed.Broadcast()
			c.mu.Unlock()
		case <-done:
		}
	}()

	for len(c.pending) > 0 && ctx.Err() == nil {
		c.changed.Wait()
	}
	if err := ctx.Err(); err != nil {
		// Abandon the pause: release workers blocked in SafePoint/Join.
		c.pausing = false
		c.pending = make(map[uint64]struct{})
		c.changed.Broadcast()
		c.mu.Unlock()
		return epoch, err
	}
	c.mu.Unlock()
	return epoch, nil
}

// Resume ends the pause and releases all blocked and joining workers.
func (c *Coordinator) Resume() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pausing = false
	c.pending = make(map[uint64]struct{})
	c.changed.Broadcast()
}

// Paused reports whether a pause is currently in effect.
func (c *Coordinator) Paused() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.pausing
}

// Epoch returns the current pause epoch.
func (c *Coordinator) Epoch() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.epoch
}
