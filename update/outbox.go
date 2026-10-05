package update

import (
	"context"
	"sync"
)

// Effect is an external side effect produced by an update. Effects are
// delivered only after the write that produced them has committed.
type Effect struct {
	Topic   string
	Payload []byte
}

// Deliverer performs the actual external side effect.
type Deliverer interface {
	Deliver(ctx context.Context, e Effect) error
}

// DelivererFunc adapts a function to Deliverer.
type DelivererFunc func(ctx context.Context, e Effect) error

func (f DelivererFunc) Deliver(ctx context.Context, e Effect) error { return f(ctx, e) }

// Record is a pending, retryable side effect.
type Record struct {
	ID       uint64
	Effect   Effect
	Attempts int
	LastErr  error
}

// Outbox holds side effects of committed writes until they are successfully
// delivered. A failed delivery keeps the record pending so it can be
// retried with Flush.
type Outbox struct {
	mu      sync.Mutex
	nextID  uint64
	pending map[uint64]*Record
	d       Deliverer
}

func NewOutbox(d Deliverer) *Outbox {
	return &Outbox{pending: make(map[uint64]*Record), d: d}
}

// enqueue stages effects of a committed write and immediately tries to
// deliver them. Delivery failures leave the records pending.
func (o *Outbox) enqueue(ctx context.Context, effects []Effect) {
	if len(effects) == 0 {
		return
	}
	o.mu.Lock()
	ids := make([]uint64, 0, len(effects))
	for _, e := range effects {
		o.nextID++
		id := o.nextID
		o.pending[id] = &Record{ID: id, Effect: e}
		ids = append(ids, id)
	}
	o.mu.Unlock()
	for _, id := range ids {
		o.deliverOne(ctx, id)
	}
}

// Flush retries every pending record. Successfully delivered records are
// removed; failures stay pending with their last error recorded.
func (o *Outbox) Flush(ctx context.Context) {
	for _, id := range o.pendingIDs() {
		o.deliverOne(ctx, id)
	}
}

func (o *Outbox) deliverOne(ctx context.Context, id uint64) {
	o.mu.Lock()
	rec, ok := o.pending[id]
	o.mu.Unlock()
	if !ok {
		return
	}
	err := o.d.Deliver(ctx, rec.Effect)
	o.mu.Lock()
	defer o.mu.Unlock()
	if _, still := o.pending[id]; !still {
		return
	}
	if err == nil {
		delete(o.pending, id)
		return
	}
	rec.Attempts++
	rec.LastErr = err
}

func (o *Outbox) pendingIDs() []uint64 {
	o.mu.Lock()
	defer o.mu.Unlock()
	ids := make([]uint64, 0, len(o.pending))
	for id := range o.pending {
		ids = append(ids, id)
	}
	return ids
}

// Pending returns a snapshot of the retryable records.
func (o *Outbox) Pending() []Record {
	o.mu.Lock()
	defer o.mu.Unlock()
	out := make([]Record, 0, len(o.pending))
	for _, r := range o.pending {
		out = append(out, *r)
	}
	return out
}
