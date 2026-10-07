// Package inbox implements the inbox pattern: business events may be
// delivered more than once, but their effects are applied exactly once.
//
// The dedup record, the business state mutation and the output messages
// are committed in a single local transaction. Dispatching outputs to the
// network is a separate, at-least-once phase: if the ack (MarkSent) is
// lost after a successful send, the output is resent with a STABLE id, so
// the receiver can dedup it.
package inbox

import (
	"fmt"
	"sync"
)

// Event is an incoming business event. ID must be unique per logical event
// and is reused by the transport on redelivery.
type Event struct {
	ID      string
	Payload []byte
}

// Output is a message produced as a side effect of processing an event.
// ID is stable: it is derived from the event id, so a resend after a lost
// ack carries the same id and the receiver can dedup it.
type Output struct {
	ID      string
	Payload []byte
	Sent    bool
}

// StableOutputID derives a deterministic output id from the event id and
// the output's position within that event's handler result.
func StableOutputID(eventID string, seq int) string {
	return fmt.Sprintf("%s#%d", eventID, seq)
}

// Tx is the local transaction in which dedup, state mutation and output
// enqueueing happen atomically.
type Tx interface {
	// Seen reports whether the event was already recorded by a committed tx.
	Seen(eventID string) bool
	// Record marks the event as processed within this tx.
	Record(eventID string) error
	// GetState reads business state, seeing this tx's own writes.
	GetState(key string) ([]byte, bool)
	// SetState buffers a business state mutation within this tx.
	SetState(key string, value []byte)
	// Enqueue buffers an output message within this tx.
	Enqueue(out Output)
	// Commit atomically persists: dedup record + state + outputs.
	Commit() error
	// Rollback discards everything buffered in this tx.
	Rollback() error
}

// Store is the persistence model behind the inbox. Two independent
// implementations exist: wal (append-only log) and snapshot (atomic
// whole-state replacement).
type Store interface {
	Begin() (Tx, error)
	// Pending returns committed outputs not yet marked sent, in commit order.
	Pending() ([]Output, error)
	// MarkSent durably records that an output was handed to the transport.
	MarkSent(outputID string) error
	Close() error
}

// Handler applies the business effect of an event inside the local
// transaction and returns the output messages to enqueue. The Processor
// assigns stable ids to them.
type Handler func(tx Tx, ev Event) ([]Output, error)

// Processor dedups events and commits effects atomically.
type Processor struct {
	store Store
}

func NewProcessor(s Store) *Processor { return &Processor{store: s} }

// Process applies the event's effect exactly once. It returns applied=false
// for duplicate deliveries. If Commit fails (e.g. crash before the commit
// record is durable), nothing was applied and the caller may retry.
func (p *Processor) Process(ev Event, h Handler) (applied bool, err error) {
	tx, err := p.store.Begin()
	if err != nil {
		return false, err
	}
	if tx.Seen(ev.ID) {
		tx.Rollback()
		return false, nil
	}
	if err := tx.Record(ev.ID); err != nil {
		tx.Rollback()
		return false, err
	}
	outs, err := h(tx, ev)
	if err != nil {
		tx.Rollback()
		return false, err
	}
	for i := range outs {
		outs[i].ID = StableOutputID(ev.ID, i)
		tx.Enqueue(outs[i])
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

// Transport delivers an output to the outside world (broker, HTTP, ...).
type Transport interface {
	Send(out Output) error
}

// Dispatcher sends pending outputs. Sending is at-least-once: a crash
// between Send and MarkSent causes a resend of the SAME output id.
type Dispatcher struct {
	store Store
	tr    Transport
	// AfterSend, if non-nil, runs between Send and MarkSent. Returning an
	// error aborts dispatch. It exists to simulate a crash in that window.
	AfterSend func(out Output) error
}

func NewDispatcher(s Store, tr Transport) *Dispatcher {
	return &Dispatcher{store: s, tr: tr}
}

// DispatchPending sends every pending output once, returning how many were
// durably marked sent.
func (d *Dispatcher) DispatchPending() (int, error) {
	outs, err := d.store.Pending()
	if err != nil {
		return 0, err
	}
	sent := 0
	for _, o := range outs {
		if err := d.tr.Send(o); err != nil {
			return sent, err
		}
		if d.AfterSend != nil {
			if err := d.AfterSend(o); err != nil {
				return sent, err
			}
		}
		if err := d.store.MarkSent(o.ID); err != nil {
			return sent, err
		}
		sent++
	}
	return sent, nil
}

// Receiver models the consumer side: the network may deliver the same
// output more than once, but the business effect is applied once per
// stable output id.
type Receiver struct {
	mu      sync.Mutex
	seen    map[string]bool
	Applied int
}

func NewReceiver() *Receiver { return &Receiver{seen: make(map[string]bool)} }

// Receive applies the output's effect unless this id was already applied.
// It returns true iff the effect was applied.
func (r *Receiver) Receive(out Output) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.seen[out.ID] {
		return false
	}
	r.seen[out.ID] = true
	r.Applied++
	return true
}
