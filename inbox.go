// Package inbox implements the inbox/outbox pattern for exactly-once
// business effects over at-least-once event delivery.
//
// Semantics:
//
//   - Business effect exactly once: the dedup record, the business state
//     change and the output messages commit in ONE local transaction.
//     A duplicate event delivery hits the dedup record and is a no-op.
//   - Network send at least once: output messages are sent after the
//     transaction commits. If the process crashes after sending but
//     before the sent-mark is persisted, the message is resent after
//     recovery. The output id is STABLE (derived from the event id), so
//     the receiver can dedup retries by id.
package inbox

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
)

// ErrDuplicate is returned by Tx.InsertDedup when the event was already
// recorded in a previously committed transaction.
var ErrDuplicate = errors.New("inbox: event already processed")

// Event is an inbound business event. Delivery is at-least-once: the same
// ID may arrive more than once.
type Event struct {
	ID   string
	Type string
	Body []byte
}

// OutboxMessage is an output message produced while handling an event.
// ID must be stable: it is derived from the event id (see OutputID), never
// regenerated, so a resend after a lost ack carries the same id.
type OutboxMessage struct {
	ID       string
	EventID  string
	Topic    string
	Payload  []byte
	Attempts int
}

// Tx is a single local transaction. Implementations must commit all
// mutations atomically and make them durable before Update returns.
type Tx interface {
	// InsertDedup records the event id. It returns ErrDuplicate if the
	// id was already committed by an earlier transaction.
	InsertDedup(eventID string) error
	// PutState applies a business state change.
	PutState(key string, value []byte) error
	// GetState reads business state.
	GetState(key string) ([]byte, bool, error)
	// EnqueueOutbox stages an output message in the same transaction.
	EnqueueOutbox(msg OutboxMessage) error
	// PendingOutbox lists committed but not yet sent output messages,
	// in enqueue order.
	PendingOutbox() ([]OutboxMessage, error)
	// MarkSent marks an output message as sent. It is idempotent.
	MarkSent(id string) error
}

// Store persists dedup records, business state and the outbox.
// Implementations must be write-through: a committed Update survives a
// process crash without requiring Close.
type Store interface {
	// Update runs fn inside one local transaction. If fn returns an
	// error, none of its mutations are applied.
	Update(ctx context.Context, fn func(Tx) error) error
	// View runs fn against a read-only snapshot of committed state.
	View(ctx context.Context, fn func(Tx) error) error
	// Close releases resources. Committed state remains durable.
	Close() error
}

// OutputID derives a stable output id from the event id and a per-event
// sequence number. The same (eventID, seq) always yields the same id, so
// retransmissions after a crash are idempotent at the receiver.
func OutputID(eventID string, seq int) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s:%d", eventID, seq)))
	return hex.EncodeToString(sum[:])
}

// Processor handles inbound events with exactly-once business effects.
type Processor struct {
	Store Store
}

// Handle records the dedup entry, runs the business effect fn (which may
// mutate state and enqueue output messages) and commits everything in one
// local transaction. If the event was already processed, Handle is a no-op
// and returns nil.
func (p *Processor) Handle(ctx context.Context, ev Event, fn func(tx Tx) error) error {
	return p.Store.Update(ctx, func(tx Tx) error {
		if err := tx.InsertDedup(ev.ID); err != nil {
			if err == ErrDuplicate {
				return nil
			}
			return err
		}
		return fn(tx)
	})
}

// Sender delivers an output message to the outside world (network send).
type Sender interface {
	Send(ctx context.Context, msg OutboxMessage) error
}

// Dispatcher sends pending outbox messages and records the sent-mark.
// Sending and marking are deliberately NOT in the inbox transaction: a
// crash between Send and MarkSent causes a resend of the same message
// (same stable id) after recovery.
type Dispatcher struct {
	Store  Store
	Sender Sender

	// BeforeMarkSent, if set, runs after a successful Send and before
	// the sent-mark is persisted. It is a crash-injection hook for tests.
	BeforeMarkSent func()
}

// DispatchPending sends every pending message once and returns the number
// of messages marked sent. On sender error it stops and returns the error;
// the failed message stays pending and will be retried with the same id.
func (d *Dispatcher) DispatchPending(ctx context.Context) (int, error) {
	var pending []OutboxMessage
	if err := d.Store.View(ctx, func(tx Tx) error {
		msgs, err := tx.PendingOutbox()
		pending = msgs
		return err
	}); err != nil {
		return 0, err
	}
	sent := 0
	for _, msg := range pending {
		if err := d.Sender.Send(ctx, msg); err != nil {
			return sent, err
		}
		if d.BeforeMarkSent != nil {
			d.BeforeMarkSent()
		}
		if err := d.Store.Update(ctx, func(tx Tx) error {
			return tx.MarkSent(msg.ID)
		}); err != nil {
			return sent, err
		}
		sent++
	}
	return sent, nil
}
