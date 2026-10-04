// Package pausecoord implements a pause coordinator: workers stop at
// safe points (checkpoints), and only after every worker has confirmed
// the current pause round may a consistent snapshot be read.
//
// Protocol overview:
//   - Each Pause call starts a new round identified by a monotonically
//     increasing epoch number.
//   - A worker confirms a round by reaching Checkpoint; the confirmation
//     is tagged with the epoch it observed, so a stale confirmation from
//     an older (completed or abandoned) round never counts toward the
//     current one.
//   - Workers may exit (Close) while paused; they simply drop out of the
//     set of confirmations the round is waiting for.
//   - New workers registering during a pause block until Resume.
//   - If Pause times out, the round is abandoned: every blocked worker
//     is released and a later Pause starts a fresh epoch.
package pausecoord

import (
	"errors"
	"sync"
	"time"
)

// ErrPauseTimeout is returned by Pause when the timeout elapses before
// all workers confirmed the round. The pause is abandoned and all
// blocked workers are released.
var ErrPauseTimeout = errors.New("pausecoord: pause timed out, round abandoned")

// Coordinator orchestrates pause rounds across a set of workers.
type Coordinator struct {
	mu   sync.Mutex
	cond *sync.Cond

	// epoch identifies the current (or most recent) pause round.
	// Confirmations are only valid for the epoch they were made in.
	epoch uint64
	// pausing is true while a round is in progress: from the start of
	// Pause until Resume (success) or abandonment (timeout).
	pausing bool
	workers map[*Worker]struct{}
}

// New returns an empty Coordinator in the running (non-paused) state.
func New() *Coordinator {
	c := &Coordinator{workers: make(map[*Worker]struct{})}
	c.cond = sync.NewCond(&c.mu)
	return c
}

// Register adds a new worker. If a pause round is in progress, Register
// blocks until the round is resumed or abandoned, so a new worker can
// never run (and mutate state) while a snapshot is being read.
func (c *Coordinator) Register() *Worker {
	c.mu.Lock()
	for c.pausing {
		c.cond.Wait()
	}
	w := &Worker{coord: c, confirmedEpoch: c.epoch}
	c.workers[w] = struct{}{}
	c.mu.Unlock()
	return w
}

// Pause starts a new round and blocks until every registered worker has
// confirmed this round's epoch by reaching a Checkpoint (or exited).
// While Pause holds, all confirmed workers stay parked at their
// checkpoints and new registrations block, so the caller may read a
// consistent snapshot. Call Resume to release everyone.
//
// If timeout > 0 elapses first, the round is abandoned (blocked workers
// are released as if Resume were called) and ErrPauseTimeout is
// returned. A timeout of 0 waits indefinitely.
func (c *Coordinator) Pause(timeout time.Duration) error {
	c.mu.Lock()
	// Serialize rounds: wait for a previous pause to be resumed/abandoned.
	for c.pausing {
		c.cond.Wait()
	}
	c.epoch++
	epoch := c.epoch
	c.pausing = true

	expired := false
	var timer *time.Timer
	if timeout > 0 {
		timer = time.AfterFunc(timeout, func() {
			c.mu.Lock()
			expired = true
			c.cond.Broadcast()
			c.mu.Unlock()
		})
	}
	for !expired && !c.allConfirmedLocked(epoch) {
		c.cond.Wait()
	}
	if timer != nil {
		timer.Stop()
	}

	if !c.allConfirmedLocked(epoch) {
		// Abandon the round: release parked workers and let a future
		// Pause start a fresh epoch. Confirmations tagged with the
		// abandoned epoch are now stale and will never be counted again.
		c.pausing = false
		c.cond.Broadcast()
		c.mu.Unlock()
		return ErrPauseTimeout
	}
	c.mu.Unlock()
	return nil
}

// Resume ends the current pause round, releasing parked workers and
// blocked Register calls. It is a no-op if no round is in progress.
func (c *Coordinator) Resume() {
	c.mu.Lock()
	c.pausing = false
	c.cond.Broadcast()
	c.mu.Unlock()
}

// WithSnapshot pauses, runs fn while the system is consistently paused,
// and resumes afterwards. It returns ErrPauseTimeout if the pause round
// could not be completed within timeout.
func (c *Coordinator) WithSnapshot(timeout time.Duration, fn func()) error {
	if err := c.Pause(timeout); err != nil {
		return err
	}
	defer c.Resume()
	fn()
	return nil
}

// Paused reports whether a pause round is currently in progress.
func (c *Coordinator) Paused() bool {
	c.mu.Lock()
	p := c.pausing
	c.mu.Unlock()
	return p
}

// allConfirmedLocked reports whether every live worker has confirmed
// the given epoch. Callers must hold c.mu.
func (c *Coordinator) allConfirmedLocked(epoch uint64) bool {
	for w := range c.workers {
		if w.confirmedEpoch != epoch {
			return false
		}
	}
	return true
}

// Worker is a participant in the pause protocol. It must call
// Checkpoint at safe points and Close when it exits.
type Worker struct {
	coord *Coordinator
	// confirmedEpoch is the newest pause round this worker has
	// confirmed. Guarded by coord.mu.
	confirmedEpoch uint64
}

// Checkpoint marks a safe point. If a pause round is in progress the
// worker confirms the round's epoch and parks until Resume (or
// abandonment); otherwise it returns immediately.
func (w *Worker) Checkpoint() {
	c := w.coord
	c.mu.Lock()
	for c.pausing {
		if w.confirmedEpoch != c.epoch {
			// Confirm the current round. This happens inside the wait
			// loop so that a worker parked across an abandon/re-pause
			// boundary confirms the new epoch instead of relying on
			// its stale one.
			w.confirmedEpoch = c.epoch
			c.cond.Broadcast()
		}
		c.cond.Wait()
	}
	c.mu.Unlock()
}

// Close deregisters the worker. It is safe to call while paused; the
// worker simply drops out of the set the current round is waiting for.
func (w *Worker) Close() {
	c := w.coord
	c.mu.Lock()
	delete(c.workers, w)
	c.cond.Broadcast()
	c.mu.Unlock()
}
