package compaction

import (
	"context"
	"sync"
	"time"
)

// StallState is the write admission state of the engine.
type StallState int

const (
	// StateNormal accepts writes without throttling.
	StateNormal StallState = iota
	// StateDelayed accepts writes but sleeps before each one (soft limit).
	StateDelayed
	// StateStopped rejects/blocks writes until pressure recovers (hard limit).
	StateStopped
)

// String renders the state for logging and test assertions.
func (s StallState) String() string {
	switch s {
	case StateDelayed:
		return "Delayed"
	case StateStopped:
		return "Stopped"
	default:
		return "Normal"
	}
}

// StallThresholds configures the soft/hard watermarks. A zero value means the
// corresponding trigger never fires.
type StallThresholds struct {
	// L0SoftDelayFiles: entering Delayed when L0 file count reaches it.
	L0SoftDelayFiles int
	// L0HardStopFiles: entering Stopped when L0 file count reaches it.
	L0HardStopFiles int
	// PendingSoftDelayBytes: soft threshold on global pending compaction bytes.
	PendingSoftDelayBytes int64
	// PendingHardStopBytes: hard threshold on global pending compaction bytes.
	PendingHardStopBytes int64
	// DelayStep is the sleep applied to each delayed write.
	DelayStep time.Duration
}

// DefaultStallThresholds mirrors typical RocksDB write-stall knobs.
func DefaultStallThresholds() StallThresholds {
	return StallThresholds{
		L0SoftDelayFiles:      20,
		L0HardStopFiles:       36,
		PendingSoftDelayBytes: 64 << 20,
		PendingHardStopBytes:  128 << 20,
		DelayStep:             time.Millisecond,
	}
}

// StallMetrics is the live pressure signal consumed by the controller.
type StallMetrics struct {
	L0FileCount            int
	PendingCompactionBytes int64
}

// WriteController is the Normal <-> Delayed <-> Stopped state machine. It is
// safe for concurrent use by writer goroutines and the compaction loop.
type WriteController struct {
	thresholds StallThresholds

	mu      sync.Mutex
	state   StallState
	cond    *sync.Cond
	metrics StallMetrics

	// OnTransition, when set, is invoked after every state change. It runs
	// outside the controller lock so tests can block on it safely.
	OnTransition func(from, to StallState)
}

// NewWriteController builds a controller starting in StateNormal.
func NewWriteController(thresholds StallThresholds) *WriteController {
	if thresholds.DelayStep <= 0 {
		thresholds.DelayStep = time.Millisecond
	}
	ctl := &WriteController{thresholds: thresholds, state: StateNormal}
	ctl.cond = sync.NewCond(&ctl.mu)
	return ctl
}

// decide maps pressure metrics to a target state. Hard stops take precedence;
// within a level the L0 and pending-byte triggers are OR-combined. Releasing
// pressure simply maps back to the lower state (exact three-state switching).
func (c *WriteController) decide(m StallMetrics) StallState {
	t := c.thresholds
	hard := false
	soft := false

	if t.L0HardStopFiles > 0 && m.L0FileCount >= t.L0HardStopFiles {
		hard = true
	}
	if t.PendingHardStopBytes > 0 && m.PendingCompactionBytes >= t.PendingHardStopBytes {
		hard = true
	}
	if t.L0SoftDelayFiles > 0 && m.L0FileCount >= t.L0SoftDelayFiles {
		soft = true
	}
	if t.PendingSoftDelayBytes > 0 && m.PendingCompactionBytes >= t.PendingSoftDelayBytes {
		soft = true
	}

	switch {
	case hard:
		return StateStopped
	case soft:
		return StateDelayed
	default:
		return StateNormal
	}
}

// Evaluate recomputes the target state from fresh metrics and performs the
// transition, waking writers waiting on a hard stop. It returns the new state.
func (c *WriteController) Evaluate(m StallMetrics) StallState {
	c.mu.Lock()
	from := c.state
	c.metrics = m
	to := c.decide(m)
	c.state = to
	c.mu.Unlock()

	if from != to {
		// Broadcast outside the critical section semantics: writers waiting in
		// WaitWritable re-check; a downgrade unblocks them.
		c.cond.Broadcast()
		if c.OnTransition != nil {
			c.OnTransition(from, to)
		}
	}
	return to
}

// State returns the current admission state.
func (c *WriteController) State() StallState {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state
}

// Metrics returns the most recently observed pressure metrics.
func (c *WriteController) Metrics() StallMetrics {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.metrics
}

// WaitWritable blocks while the engine is in StateStopped or until ctx is
// cancelled. In StateDelayed it applies DelayStep once and returns; in
// StateNormal it returns immediately. The returned error comes only from ctx.
func (c *WriteController) WaitWritable(ctx context.Context) error {
	// A cancelled context must win even before the first blocking wait.
	if err := ctx.Err(); err != nil {
		return err
	}
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		select {
		case <-ctx.Done():
			c.cond.Broadcast()
		case <-stop:
		}
	}()

	c.mu.Lock()
	for c.state == StateStopped {
		c.cond.Wait()
		if err := ctx.Err(); err != nil {
			c.mu.Unlock()
			return err
		}
	}
	state := c.state
	step := c.thresholds.DelayStep
	c.mu.Unlock()

	if state == StateDelayed {
		select {
		case <-time.After(step):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}
