package taskpkg

import (
	"sync"
)

// MonotonicCounter is the injected non-rollbackable sequence source.
//
// Implementations MUST persist the high-water mark durably in a medium that
// cannot be restored to an older state (for example a TPM/TEE NV index, an
// HSM counter, or an external monotonic service). The package treats any
// failure of this component as fail-closed.
type MonotonicCounter interface {
	// Next returns the smallest sequence that has not yet been consumed.
	// The first usable sequence is 1. A failure wraps ErrCounterUnavailable.
	Next() (uint64, error)

	// Consume atomically marks seq as consumed and advances the high-water
	// mark to seq+1. Re-consuming a previously consumed sequence MUST return
	// an error wrapping ErrCounterRolledBack; skipping over the expected
	// sequence wraps ErrCounterGap.
	Consume(seq uint64) error
}

// MemoryCounter is an in-process MonotonicCounter. It is intended for tests
// and as a reference implementation: it is durable only for the lifetime of
// the process and therefore cannot by itself defend against machine
// snapshots.
type MemoryCounter struct {
	mu   sync.Mutex
	next uint64
}

// NewMemoryCounter returns an empty counter whose first sequence is 1.
func NewMemoryCounter() *MemoryCounter {
	return &MemoryCounter{next: 1}
}

// Next implements MonotonicCounter.
func (c *MemoryCounter) Next() (uint64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.next, nil
}

// Consume implements MonotonicCounter.
func (c *MemoryCounter) Consume(seq uint64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch {
	case seq < c.next:
		return ErrCounterRolledBack
	case seq > c.next:
		return ErrCounterGap
	default:
		c.next = seq + 1
		return nil
	}
}

// FailCounter is a test double whose Next and Consume calls can be toggled
// between working and failing independently.
type FailCounter struct {
	inner       MonotonicCounter
	mu          sync.Mutex
	failNext    bool
	failConsume bool
	consumed    []uint64
}

// NewFailCounter wraps an existing counter with controllable failure modes.
func NewFailCounter(inner MonotonicCounter) *FailCounter {
	return &FailCounter{inner: inner}
}

// SetNextFailure toggles failures returned by Next.
func (c *FailCounter) SetNextFailure(fail bool) {
	c.mu.Lock()
	c.failNext = fail
	c.mu.Unlock()
}

// SetConsumeFailure toggles failures returned by Consume.
func (c *FailCounter) SetConsumeFailure(fail bool) {
	c.mu.Lock()
	c.failConsume = fail
	c.mu.Unlock()
}

// ConsumedSequences returns the sequences observed by successful Consume calls.
func (c *FailCounter) ConsumedSequences() []uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]uint64, len(c.consumed))
	copy(out, c.consumed)
	return out
}

// Next implements MonotonicCounter.
func (c *FailCounter) Next() (uint64, error) {
	c.mu.Lock()
	fail := c.failNext
	c.mu.Unlock()
	if fail {
		return 0, ErrCounterUnavailable
	}
	return c.inner.Next()
}

// Consume implements MonotonicCounter.
func (c *FailCounter) Consume(seq uint64) error {
	c.mu.Lock()
	fail := c.failConsume
	c.mu.Unlock()
	if fail {
		return ErrCounterUnavailable
	}
	if err := c.inner.Consume(seq); err != nil {
		return err
	}
	c.mu.Lock()
	c.consumed = append(c.consumed, seq)
	c.mu.Unlock()
	return nil
}
