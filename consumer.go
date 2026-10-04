package logtrunc

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// ErrRollback is returned when a consumer tries to move its confirmed
// continuous prefix backwards. Confirmations are monotonic.
var ErrRollback = errors.New("logtrunc: confirmation rollback rejected")

// consumer tracks every confirmed sequence number. The effective
// confirmation horizon is the longest confirmed *continuous prefix*:
// a gap below a large confirmed sequence number never advances it.
type consumer struct {
	confirmed map[uint64]bool
	horizon   uint64
}

// Registry tracks the confirmation state of all active consumers.
type Registry struct {
	mu        sync.Mutex
	consumers map[string]*consumer
	// maxSeq, when set, bounds confirmable sequence numbers (the log end).
	maxSeq func() uint64
}

// NewRegistry creates a Registry. maxSeq may be nil to disable
// upper-bound validation.
func NewRegistry(maxSeq func() uint64) *Registry {
	return &Registry{consumers: map[string]*consumer{}, maxSeq: maxSeq}
}

// Register adds a consumer with horizon 0 (nothing confirmed).
func (r *Registry) Register(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.consumers[id]; ok {
		return fmt.Errorf("logtrunc: consumer %q already registered", id)
	}
	r.consumers[id] = &consumer{confirmed: map[uint64]bool{}}
	return nil
}

// Exit deregisters a consumer. Its confirmations are discarded, so it
// stops blocking truncation.
func (r *Registry) Exit(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.consumers[id]; !ok {
		return fmt.Errorf("logtrunc: consumer %q not registered", id)
	}
	delete(r.consumers, id)
	return nil
}

// Confirm records confirmation of a single sequence number. It is
// idempotent: re-confirming an already-covered number is a no-op.
// The horizon only advances across a gap-free prefix, so confirming a
// large sequence number never masks a hole below it.
func (r *Registry) Confirm(id string, seq uint64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	c, ok := r.consumers[id]
	if !ok {
		return fmt.Errorf("logtrunc: consumer %q not registered", id)
	}
	if seq == 0 {
		return errors.New("logtrunc: sequence numbers start at 1")
	}
	if r.maxSeq != nil && seq > r.maxSeq() {
		return fmt.Errorf("logtrunc: seq %d beyond log end %d", seq, r.maxSeq())
	}
	if seq <= c.horizon {
		return nil // already covered by the continuous prefix
	}
	c.confirmed[seq] = true
	for c.confirmed[c.horizon+1] {
		delete(c.confirmed, c.horizon+1)
		c.horizon++
	}
	return nil
}

// ConfirmPrefix claims a confirmed continuous prefix [1, n]. Moving the
// prefix backwards is rejected with ErrRollback.
func (r *Registry) ConfirmPrefix(id string, n uint64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	c, ok := r.consumers[id]
	if !ok {
		return fmt.Errorf("logtrunc: consumer %q not registered", id)
	}
	if r.maxSeq != nil && n > r.maxSeq() {
		return fmt.Errorf("logtrunc: seq %d beyond log end %d", n, r.maxSeq())
	}
	if n < c.horizon {
		return fmt.Errorf("%w: consumer %q horizon %d, got %d", ErrRollback, id, c.horizon, n)
	}
	for seq := c.horizon + 1; seq <= n; seq++ {
		c.confirmed[seq] = true
	}
	for c.confirmed[c.horizon+1] {
		delete(c.confirmed, c.horizon+1)
		c.horizon++
	}
	return nil
}

// Horizon returns the consumer's confirmed continuous prefix.
func (r *Registry) Horizon(id string) (uint64, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	c, ok := r.consumers[id]
	if !ok {
		return 0, false
	}
	return c.horizon, true
}

// Horizons returns a snapshot of every consumer's horizon.
func (r *Registry) Horizons() map[string]uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]uint64, len(r.consumers))
	for id, c := range r.consumers {
		out[id] = c.horizon
	}
	return out
}

// ConsumerIDs returns the registered consumer IDs in sorted order.
func (r *Registry) ConsumerIDs() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	ids := make([]string, 0, len(r.consumers))
	for id := range r.consumers {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
