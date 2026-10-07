package replay

import "sync"

// Receiver is a virtual external receiver with an idempotency table.
// Delivering an effect whose ID was already applied is a no-op, which
// makes re-sends after a lost receipt safe.
type Receiver struct {
	mu      sync.Mutex
	seen    map[string]Effect // idempotency table: effect ID -> effect
	applied []Effect          // effects actually applied, in order
	dups    int               // deliveries ignored as duplicates
}

func NewReceiver() *Receiver {
	return &Receiver{seen: map[string]Effect{}}
}

// Execute implements Sink.
func (r *Receiver) Execute(e Effect) error {
	r.Deliver(e)
	return nil
}

// Deliver records the effect if its ID is new. It reports whether the
// delivery was a duplicate.
func (r *Receiver) Deliver(e Effect) (duplicate bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.seen[e.ID]; ok {
		r.dups++
		return true
	}
	r.seen[e.ID] = e
	r.applied = append(r.applied, e)
	return false
}

// Applied returns the effects actually applied, in delivery order.
func (r *Receiver) Applied() []Effect {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Effect, len(r.applied))
	copy(out, r.applied)
	return out
}

// AppliedIDs returns the IDs of applied effects, in delivery order.
func (r *Receiver) AppliedIDs() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	ids := make([]string, len(r.applied))
	for i, e := range r.applied {
		ids[i] = e.ID
	}
	return ids
}

// Duplicates returns how many deliveries were ignored as duplicates.
func (r *Receiver) Duplicates() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.dups
}

// Has reports whether an effect ID is in the idempotency table.
func (r *Receiver) Has(effectID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.seen[effectID]
	return ok
}
