package replay

import (
	"errors"
	"sync"
)

// ErrReceiptLost means the receiver applied (or deduplicated) the
// effect but the receipt back to the sender was lost.
var ErrReceiptLost = errors.New("receipt lost")

// Receiver is a virtual effect receiver with an idempotency table
// keyed by effect ID. Duplicate deliveries of the same ID are
// acknowledged without being applied again.
type Receiver struct {
	mu         sync.Mutex
	applied    map[string]Effect
	order      []Effect
	deliveries int
	// DropReceipt, when non-nil and returning true, simulates a lost
	// receipt: the effect is recorded but Send reports an error.
	DropReceipt func(e Effect) bool
}

func NewReceiver() *Receiver {
	return &Receiver{applied: make(map[string]Effect)}
}

func (r *Receiver) Send(e Effect) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.deliveries++
	if _, dup := r.applied[e.ID]; !dup {
		r.applied[e.ID] = e
		r.order = append(r.order, e)
	}
	if r.DropReceipt != nil && r.DropReceipt(e) {
		return ErrReceiptLost
	}
	return nil
}

// Applied returns each distinct effect exactly once, in arrival order.
func (r *Receiver) Applied() []Effect {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Effect, len(r.order))
	copy(out, r.order)
	return out
}

// Deliveries counts total Send calls, including duplicates.
func (r *Receiver) Deliveries() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.deliveries
}
