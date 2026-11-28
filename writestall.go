package lsm

import (
	"context"
	"errors"
	"sync"
	"time"
)

// WriteStallState is the admission state for new writes.
type WriteStallState int

// StallState is a compatibility alias for WriteStallState.
type StallState = WriteStallState

const (
	// StallNormal admits writes immediately.
	StallNormal WriteStallState = iota
	// StallDelayed admits writes after a soft-rate-limit delay.
	StallDelayed
	// StallStopped blocks writes until pressure falls below the hard limit.
	StallStopped
)

// Compatibility aliases.
const (
	WriteStallNormal       = StallNormal
	WriteStallDelayed      = StallDelayed
	WriteStallStopped      = StallStopped
	Normal                 = StallNormal
	Delayed                = StallDelayed
	Stopped                = StallStopped
	StateNormal            = StallNormal
	StateDelayed           = StallDelayed
	StateStopped           = StallStopped
	StallNormalState       = StallNormal
	StallDelayedState      = StallDelayed
	StallStoppedState      = StallStopped
	WriteStallStateNormal  = StallNormal
	WriteStallStateDelayed = StallDelayed
	WriteStallStateStopped = StallStopped
)

func (s WriteStallState) String() string {
	switch s {
	case StallNormal:
		return "Normal"
	case StallDelayed:
		return "Delayed"
	case StallStopped:
		return "Stopped"
	default:
		return "Unknown"
	}
}

// WriteStallConfig configures the two watermarks. A zero value selects
// conservative defaults. The alias fields allow callers to use either the
// RocksDB trigger names or the limit names.
type WriteStallConfig struct {
	L0SlowdownTrigger              int
	L0StopTrigger                  int
	PendingCompactionSlowdownBytes uint64
	PendingCompactionStopBytes     uint64
	SoftDelay                      time.Duration

	// Aliases. When set, these take precedence over the corresponding
	// trigger/bytes fields above.
	L0SoftLimit                     int
	L0HardLimit                     int
	L0FilesSoftLimit                int
	L0FilesHardLimit                int
	PendingSoftLimit                uint64
	PendingHardLimit                uint64
	PendingCompactionBytesSoftLimit uint64
	PendingCompactionBytesHardLimit uint64
	DelayedWriteDelay               time.Duration
	Delay                           time.Duration
}

// DefaultWriteStallConfig returns RocksDB-like defaults.
func DefaultWriteStallConfig() WriteStallConfig {
	return WriteStallConfig{
		L0SlowdownTrigger:              20,
		L0StopTrigger:                  36,
		PendingCompactionSlowdownBytes: 2 << 30,
		PendingCompactionStopBytes:     4 << 30,
		SoftDelay:                      time.Millisecond,
	}
}

func (c WriteStallConfig) normalized() WriteStallConfig {
	d := DefaultWriteStallConfig()
	if c.L0SoftLimit <= 0 {
		c.L0SoftLimit = c.L0FilesSoftLimit
	}
	if c.L0HardLimit <= 0 {
		c.L0HardLimit = c.L0FilesHardLimit
	}
	if c.L0SoftLimit <= 0 {
		c.L0SoftLimit = c.L0SlowdownTrigger
	}
	if c.L0HardLimit <= 0 {
		c.L0HardLimit = c.L0StopTrigger
	}
	if c.PendingSoftLimit == 0 {
		c.PendingSoftLimit = c.PendingCompactionBytesSoftLimit
	}
	if c.PendingHardLimit == 0 {
		c.PendingHardLimit = c.PendingCompactionBytesHardLimit
	}
	if c.PendingSoftLimit == 0 {
		c.PendingSoftLimit = c.PendingCompactionSlowdownBytes
	}
	if c.PendingHardLimit == 0 {
		c.PendingHardLimit = c.PendingCompactionStopBytes
	}

	l0Configured := c.L0SoftLimit > 0 || c.L0HardLimit > 0
	pendingConfigured := c.PendingSoftLimit > 0 || c.PendingHardLimit > 0
	switch {
	case !l0Configured && !pendingConfigured:
		c.L0SoftLimit = d.L0SlowdownTrigger
		c.L0HardLimit = d.L0StopTrigger
		c.PendingSoftLimit = d.PendingCompactionSlowdownBytes
		c.PendingHardLimit = d.PendingCompactionStopBytes
	case !pendingConfigured:
		c.PendingSoftLimit = 0
		c.PendingHardLimit = 0
	case !l0Configured:
		c.L0SoftLimit = 0
		c.L0HardLimit = 0
	default:
		if c.L0SoftLimit <= 0 {
			c.L0SoftLimit = d.L0SlowdownTrigger
		}
		if c.L0HardLimit <= 0 {
			c.L0HardLimit = d.L0StopTrigger
		}
		if c.PendingSoftLimit == 0 {
			c.PendingSoftLimit = d.PendingCompactionSlowdownBytes
		}
		if c.PendingHardLimit == 0 {
			c.PendingHardLimit = d.PendingCompactionStopBytes
		}
	}
	if c.L0SoftLimit > 0 && c.L0HardLimit > 0 && c.L0HardLimit < c.L0SoftLimit {
		c.L0HardLimit = c.L0SoftLimit
	}
	if c.PendingSoftLimit > 0 && c.PendingHardLimit > 0 && c.PendingHardLimit < c.PendingSoftLimit {
		c.PendingHardLimit = c.PendingSoftLimit
	}
	if c.SoftDelay <= 0 {
		c.SoftDelay = d.SoftDelay
	}
	if c.DelayedWriteDelay <= 0 {
		c.DelayedWriteDelay = c.Delay
	}
	if c.DelayedWriteDelay <= 0 {
		c.DelayedWriteDelay = c.SoftDelay
	}
	return c
}

// WriteStallSnapshot is a point-in-time view of the controller.
type WriteStallSnapshot struct {
	State        WriteStallState
	L0Files      int
	PendingBytes uint64
	Delay        time.Duration
}

// StallTransition records a state change for observability.
type StallTransition struct {
	From         WriteStallState
	To           WriteStallState
	L0Files      int
	PendingBytes uint64
	At           time.Time
}

// ErrWriteStallClosed is returned by waiters after Close.
var ErrWriteStallClosed = errors.New("lsm: write stall controller closed")

// WriteStallController drives Normal -> Delayed -> Stopped and back. Every
// Update recomputes the state from current pressure, so recovery is automatic
// as soon as compaction drains the relevant watermark.
type WriteStallController struct {
	mu         sync.Mutex
	cfg        WriteStallConfig
	state      WriteStallState
	l0Files    int
	pending    uint64
	changed    chan struct{}
	closed     bool
	closeCh    chan struct{}
	history    []StallTransition
	historyCap int
	onChange   func(from, to WriteStallState)
}

// WriteStall is a compatibility alias for WriteStallController.
type WriteStall = WriteStallController

// NewWriteStallController constructs a controller in Normal state.
func NewWriteStallController(configs ...WriteStallConfig) *WriteStallController {
	cfg := WriteStallConfig{}
	if len(configs) > 0 {
		cfg = configs[0]
	}
	return &WriteStallController{
		cfg:        cfg.normalized(),
		state:      StallNormal,
		changed:    make(chan struct{}),
		closeCh:    make(chan struct{}),
		historyCap: 256,
	}
}

// NewWriteStall is an alias for NewWriteStallController.
func NewWriteStall(configs ...WriteStallConfig) *WriteStallController {
	return NewWriteStallController(configs...)
}

// Config returns the effective configuration.
func (c *WriteStallController) Config() WriteStallConfig {
	if c == nil {
		return WriteStallConfig{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cfg
}

// Update supplies both watermarks and returns the resulting state.
func (c *WriteStallController) Update(l0Files int, pendingCompactionBytes uint64) WriteStallState {
	if c == nil {
		return StallNormal
	}
	c.mu.Lock()
	if l0Files < 0 {
		l0Files = 0
	}
	c.l0Files = l0Files
	c.pending = pendingCompactionBytes
	old := c.state
	next := c.computeStateLocked()
	c.state = next
	if next != old {
		c.history = append(c.history, StallTransition{
			From:         old,
			To:           next,
			L0Files:      l0Files,
			PendingBytes: pendingCompactionBytes,
			At:           time.Now(),
		})
		if len(c.history) > c.historyCap {
			c.history = append([]StallTransition(nil), c.history[len(c.history)-c.historyCap:]...)
		}
		c.signalLocked()
	}
	hook := c.onChange
	c.mu.Unlock()
	if next != old && hook != nil {
		hook(old, next)
	}
	return next
}

// Evaluate is an alias for Update.
func (c *WriteStallController) Evaluate(l0Files int, pendingCompactionBytes uint64) WriteStallState {
	return c.Update(l0Files, pendingCompactionBytes)
}

// Check is an alias for Update.
func (c *WriteStallController) Check(l0Files int, pendingCompactionBytes uint64) WriteStallState {
	return c.Update(l0Files, pendingCompactionBytes)
}

// Observe is an alias for Update.
func (c *WriteStallController) Observe(l0Files int, pendingCompactionBytes uint64) WriteStallState {
	return c.Update(l0Files, pendingCompactionBytes)
}

// ObserveScheduler feeds the scheduler's current L0 count and global pending
// compaction bytes into the state machine.
func (c *WriteStallController) ObserveScheduler(s *CompactionScheduler) WriteStallState {
	if s == nil {
		return c.Update(0, 0)
	}
	return c.Update(s.L0FileCount(), s.PendingCompactionBytes())
}

// SetL0Files updates only the L0 watermark and recomputes the state.
func (c *WriteStallController) SetL0Files(n int) WriteStallState {
	if c == nil {
		return StallNormal
	}
	c.mu.Lock()
	pending := c.pending
	c.mu.Unlock()
	return c.Update(n, pending)
}

// UpdateL0Files is an alias for SetL0Files.
func (c *WriteStallController) UpdateL0Files(n int) WriteStallState { return c.SetL0Files(n) }

// SetPendingBytes updates only the pending-compaction watermark.
func (c *WriteStallController) SetPendingBytes(n uint64) WriteStallState {
	if c == nil {
		return StallNormal
	}
	c.mu.Lock()
	l0 := c.l0Files
	c.mu.Unlock()
	return c.Update(l0, n)
}

// UpdatePendingBytes is an alias for SetPendingBytes.
func (c *WriteStallController) UpdatePendingBytes(n uint64) WriteStallState {
	return c.SetPendingBytes(n)
}

// SetPendingCompactionBytes is an alias for SetPendingBytes.
func (c *WriteStallController) SetPendingCompactionBytes(n uint64) WriteStallState {
	return c.SetPendingBytes(n)
}

// Recompute recalculates the state without changing the observed watermarks.
func (c *WriteStallController) Recompute() WriteStallState {
	if c == nil {
		return StallNormal
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.state = c.computeStateLocked()
	return c.state
}

func (c *WriteStallController) computeStateLocked() WriteStallState {
	if (c.cfg.L0HardLimit > 0 && c.l0Files >= c.cfg.L0HardLimit) ||
		(c.cfg.PendingHardLimit > 0 && c.pending >= c.cfg.PendingHardLimit) {
		return StallStopped
	}
	if (c.cfg.L0SoftLimit > 0 && c.l0Files >= c.cfg.L0SoftLimit) ||
		(c.cfg.PendingSoftLimit > 0 && c.pending >= c.cfg.PendingSoftLimit) {
		return StallDelayed
	}
	return StallNormal
}

// State returns the current state.
func (c *WriteStallController) State() WriteStallState {
	if c == nil {
		return StallNormal
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state
}

// CurrentState is an alias for State.
func (c *WriteStallController) CurrentState() WriteStallState { return c.State() }

// Snapshot returns the current state and observed watermarks.
func (c *WriteStallController) Snapshot() WriteStallSnapshot {
	if c == nil {
		return WriteStallSnapshot{State: StallNormal}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return WriteStallSnapshot{
		State:        c.state,
		L0Files:      c.l0Files,
		PendingBytes: c.pending,
		Delay:        c.cfg.DelayedWriteDelay,
	}
}

// L0Files returns the last observed L0 file count.
func (c *WriteStallController) L0Files() int {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.l0Files
}

// PendingBytes returns the last observed pending compaction bytes.
func (c *WriteStallController) PendingBytes() uint64 {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.pending
}

// PendingCompactionBytes is an alias for PendingBytes.
func (c *WriteStallController) PendingCompactionBytes() uint64 { return c.PendingBytes() }

// L0FileCount is an alias for L0Files.
func (c *WriteStallController) L0FileCount() int { return c.L0Files() }

// AllowWrite reports whether a write may proceed without waiting (Normal or
// after the rate-limit sleep for Delayed).
func (c *WriteStallController) AllowWrite() bool {
	return c.State() != StallStopped
}

// IsStopped reports whether writes are hard-blocked.
func (c *WriteStallController) IsStopped() bool { return c.State() == StallStopped }

// IsDelayed reports whether writes are soft-limited.
func (c *WriteStallController) IsDelayed() bool { return c.State() == StallDelayed }

// ShouldDelay reports whether writes are in the soft-limited state.
func (c *WriteStallController) ShouldDelay() bool { return c.IsDelayed() }

// ShouldStop reports whether writes are hard-blocked.
func (c *WriteStallController) ShouldStop() bool { return c.IsStopped() }

// WriteAllowed is an alias for AllowWrite.
func (c *WriteStallController) WriteAllowed() bool { return c.AllowWrite() }

// Delay returns the configured soft-rate-limit sleep.
func (c *WriteStallController) Delay() time.Duration {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cfg.DelayedWriteDelay
}

// WaitForWrite blocks while the controller is Stopped and sleeps once for
// Delayed. It returns nil when the caller may proceed.
func (c *WriteStallController) WaitForWrite(ctx context.Context) error {
	if c == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		c.mu.Lock()
		if c.closed {
			c.mu.Unlock()
			return ErrWriteStallClosed
		}
		state := c.state
		delay := c.cfg.DelayedWriteDelay
		changed := c.changed
		c.mu.Unlock()

		switch state {
		case StallNormal:
			return nil
		case StallDelayed:
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-c.closeCh:
				timer.Stop()
				return ErrWriteStallClosed
			case <-timer.C:
			}
			c.mu.Lock()
			next := c.state
			c.mu.Unlock()
			if next != StallStopped {
				return nil
			}
		case StallStopped:
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-c.closeCh:
				return ErrWriteStallClosed
			case <-changed:
			}
		}
	}
}

// Wait is an alias for WaitForWrite.
func (c *WriteStallController) Wait(ctx context.Context) error { return c.WaitForWrite(ctx) }

// Acquire is an alias for WaitForWrite.
func (c *WriteStallController) Acquire(ctx context.Context) error { return c.WaitForWrite(ctx) }

// History returns the most recent state transitions, oldest first.
func (c *WriteStallController) History(limit int) []StallTransition {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	n := len(c.history)
	if limit > 0 && limit < n {
		n = limit
	}
	return append([]StallTransition(nil), c.history[len(c.history)-n:]...)
}

// SetTransitionHook installs a callback invoked after a state change.
func (c *WriteStallController) SetTransitionHook(fn func(from, to WriteStallState)) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.onChange = fn
	c.mu.Unlock()
}

// Close wakes all waiters and makes subsequent waits return
// ErrWriteStallClosed. It is safe to call more than once.
func (c *WriteStallController) Close() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	c.closed = true
	close(c.closeCh)
	c.signalLocked()
}

func (c *WriteStallController) signalLocked() {
	if c.changed != nil {
		close(c.changed)
	}
	c.changed = make(chan struct{})
}
