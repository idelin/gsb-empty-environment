package updatekv

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
)

// Effect is an external side effect produced by an update. Effects are
// never delivered while the update is still retrying: they are handed to
// the Outbox only after the conditional write succeeds.
type Effect struct {
	Name    string
	Payload []byte
}

// Deliverer performs the actual external side effect (send a message,
// call a webhook, ...). Deliver must return an error when the effect was
// not applied; the record is then kept for a later retry.
type Deliverer interface {
	Deliver(ctx context.Context, e Effect) error
}

// DelivererFunc adapts a plain function to Deliverer.
type DelivererFunc func(ctx context.Context, e Effect) error

func (f DelivererFunc) Deliver(ctx context.Context, e Effect) error { return f(ctx, e) }

// PendingEffect is a retryable record for an effect whose delivery has
// not been confirmed yet.
type PendingEffect struct {
	ID       int64
	Effect   Effect
	Attempts int
	LastErr  string
}

// Outbox collects the effects of committed updates and delivers them
// exactly once. Effects whose delivery fails stay pending and can be
// retried with Flush. It is safe for concurrent use.
type Outbox struct {
	deliverer Deliverer
	nextID    atomic.Int64

	mu      sync.Mutex
	pending []PendingEffect
}

func NewOutbox(d Deliverer) *Outbox {
	return &Outbox{deliverer: d}
}

// enqueue stores effects as pending records.
func (o *Outbox) enqueue(effects []Effect) {
	if len(effects) == 0 {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, e := range effects {
		o.pending = append(o.pending, PendingEffect{ID: o.nextID.Add(1), Effect: e})
	}
}

// Pending returns a snapshot of the retryable records.
func (o *Outbox) Pending() []PendingEffect {
	o.mu.Lock()
	defer o.mu.Unlock()
	out := make([]PendingEffect, len(o.pending))
	copy(out, o.pending)
	return out
}

// Flush attempts to deliver every pending record once. Records that
// deliver successfully are removed; records that fail stay pending with
// their error recorded. Flush returns a *DeliveryError if any record
// could not be delivered.
func (o *Outbox) Flush(ctx context.Context) error {
	for {
		o.mu.Lock()
		if len(o.pending) == 0 {
			o.mu.Unlock()
			return nil
		}
		rec := o.pending[0]
		o.mu.Unlock()

		err := o.deliverer.Deliver(ctx, rec.Effect)

		o.mu.Lock()
		if len(o.pending) == 0 || o.pending[0].ID != rec.ID {
			// Concurrent mutation; restart from the head.
			o.mu.Unlock()
			continue
		}
		if err != nil {
			o.pending[0].Attempts++
			o.pending[0].LastErr = err.Error()
			failed := o.pending[0]
			// Move the failed record to the back so one poison record
			// does not block the rest of the queue.
			o.pending = append(o.pending[1:], failed)
			remaining := len(o.pending)
			o.mu.Unlock()
			return &DeliveryError{Pending: remaining, Err: err}
		}
		o.pending = o.pending[1:]
		o.mu.Unlock()
	}
}

// DeliveryError reports that the write succeeded but one or more side
// effects could not be delivered. The failed effects are retained as
// retryable records in the Outbox.
type DeliveryError struct {
	Pending int
	Err     error
}

func (e *DeliveryError) Error() string {
	return fmt.Sprintf("updatekv: %d effect(s) pending delivery: %v", e.Pending, e.Err)
}

func (e *DeliveryError) Unwrap() error { return e.Err }
