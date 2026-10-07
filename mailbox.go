// Package mailbox implements a durable FIFO message queue with
// claim-based delivery.
//
// Receiving a message does not delete it; it hands out a ClaimToken
// bound to the message's current delivery generation. The message is
// deleted only by a matching Ack. A Nack requeues the message at the
// tail of the queue. If neither happens before the visibility timeout
// expires, the message is redelivered with a new generation and any
// token from the older generation becomes invalid.
//
// Every mutation is appended to a Store before it is applied in memory,
// so a persistence failure leaves the queue state untouched and a
// multi-message send is all-or-nothing.
package mailbox

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

var (
	// ErrEmpty is returned by Receive when no message is currently visible.
	ErrEmpty = errors.New("mailbox: no message available")
	// ErrStaleToken is returned when a claim token does not match the
	// message's current delivery generation (for example after a
	// visibility-timeout redelivery) or the message is not claimed.
	ErrStaleToken = errors.New("mailbox: stale or invalid claim token")
)

const defaultVisibilityTimeout = 30 * time.Second

// Message is a dequeued message awaiting acknowledgement.
type Message struct {
	ID         string
	Body       []byte
	EnqueuedAt time.Time
	// Generation is the delivery generation this claim belongs to.
	Generation uint64
}

// ClaimToken authorizes exactly one Ack or Nack for a specific delivery
// generation of a message. It becomes invalid once the message is
// redelivered (which bumps the generation) or settled.
type ClaimToken struct {
	MessageID  string
	Generation uint64
	Deadline   time.Time
}

// Options configures a Queue.
type Options struct {
	// VisibilityTimeout is how long a received message stays invisible
	// to other receivers before it is redelivered.
	VisibilityTimeout time.Duration
	// Now is an optional clock override used by tests.
	Now func() time.Time
}

type message struct {
	id         string
	body       []byte
	enqueuedAt time.Time
	claimed    bool
	generation uint64
	deadline   time.Time
}

// Queue is a durable FIFO mailbox. Use New to create one.
type Queue struct {
	mu         sync.Mutex
	store      Store
	visTimeout time.Duration
	now        func() time.Time

	order     []string // live message IDs in FIFO order
	msgs      map[string]*message
	idCounter uint64
}

// New opens a Queue on top of store, replaying any previously persisted
// operations so that state survives restarts.
func New(store Store, opts Options) (*Queue, error) {
	if store == nil {
		return nil, errors.New("mailbox: nil Store")
	}
	if opts.VisibilityTimeout <= 0 {
		opts.VisibilityTimeout = defaultVisibilityTimeout
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	q := &Queue{
		store:      store,
		visTimeout: opts.VisibilityTimeout,
		now:        opts.Now,
		msgs:       make(map[string]*message),
	}
	ops, err := store.Load()
	if err != nil {
		return nil, fmt.Errorf("mailbox: load: %w", err)
	}
	for _, op := range ops {
		q.apply(op)
	}
	return q, nil
}

// Send enqueues a single message and returns its ID.
func (q *Queue) Send(body []byte) (string, error) {
	ids, err := q.SendBatch(body)
	if err != nil {
		return "", err
	}
	return ids[0], nil
}

// SendBatch atomically enqueues all bodies as a single durable record:
// either every message becomes visible (in argument order) or, if
// persistence fails, none of them do.
func (q *Queue) SendBatch(bodies ...[]byte) ([]string, error) {
	if len(bodies) == 0 {
		return nil, nil
	}
	q.mu.Lock()
	defer q.mu.Unlock()

	now := q.now()
	recs := make([]MessageRecord, len(bodies))
	ids := make([]string, len(bodies))
	for i, body := range bodies {
		seq := q.idCounter + uint64(i) + 1
		id := fmt.Sprintf("msg-%08d", seq)
		cp := make([]byte, len(body))
		copy(cp, body)
		recs[i] = MessageRecord{Seq: seq, ID: id, Body: cp, EnqueuedAt: now}
		ids[i] = id
	}
	op := Op{Kind: OpEnqueue, Messages: recs}
	if err := q.store.Append(op); err != nil {
		return nil, fmt.Errorf("mailbox: send: %w", err)
	}
	q.apply(op)
	return ids, nil
}

// Receive claims the first visible message in FIFO order without
// deleting it, and returns it together with the ClaimToken that
// authorizes settling this delivery. Messages whose claim has expired
// become visible again and are redelivered with a new generation.
// It returns ErrEmpty when nothing is currently visible.
func (q *Queue) Receive() (Message, ClaimToken, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	now := q.now()
	for _, id := range q.order {
		m := q.msgs[id]
		if m.claimed && now.Before(m.deadline) {
			continue
		}
		op := Op{
			Kind:       OpClaim,
			MessageID:  id,
			Generation: m.generation + 1,
			Deadline:   now.Add(q.visTimeout),
		}
		if err := q.store.Append(op); err != nil {
			return Message{}, ClaimToken{}, fmt.Errorf("mailbox: receive: %w", err)
		}
		q.apply(op)
		body := make([]byte, len(m.body))
		copy(body, m.body)
		msg := Message{
			ID:         m.id,
			Body:       body,
			EnqueuedAt: m.enqueuedAt,
			Generation: m.generation,
		}
		tok := ClaimToken{MessageID: m.id, Generation: m.generation, Deadline: m.deadline}
		return msg, tok, nil
	}
	return Message{}, ClaimToken{}, ErrEmpty
}

// Ack deletes the message identified by tok. It is idempotent: acking a
// message that is already gone returns nil, so duplicate confirmations
// are safe. Acking with a token from an older generation (after a
// timeout redelivery) or for a message that is not claimed returns
// ErrStaleToken.
func (q *Queue) Ack(tok ClaimToken) error {
	q.mu.Lock()
	defer q.mu.Unlock()

	m, ok := q.msgs[tok.MessageID]
	if !ok {
		return nil
	}
	if !m.claimed || m.generation != tok.Generation {
		return ErrStaleToken
	}
	op := Op{Kind: OpAck, MessageID: tok.MessageID, Generation: tok.Generation}
	if err := q.store.Append(op); err != nil {
		return fmt.Errorf("mailbox: ack: %w", err)
	}
	q.apply(op)
	return nil
}

// Nack releases the claim identified by tok and requeues the message at
// the tail of the queue, making it immediately visible behind all
// currently queued messages.
func (q *Queue) Nack(tok ClaimToken) error {
	q.mu.Lock()
	defer q.mu.Unlock()

	m, ok := q.msgs[tok.MessageID]
	if !ok || !m.claimed || m.generation != tok.Generation {
		return ErrStaleToken
	}
	op := Op{Kind: OpNack, MessageID: tok.MessageID, Generation: tok.Generation}
	if err := q.store.Append(op); err != nil {
		return fmt.Errorf("mailbox: nack: %w", err)
	}
	q.apply(op)
	return nil
}

// Len reports the number of live (not yet acked) messages, claimed or not.
func (q *Queue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.order)
}

// apply mutates the in-memory state for an already-persisted op.
// Callers must hold q.mu (or be replaying during New).
func (q *Queue) apply(op Op) {
	switch op.Kind {
	case OpEnqueue:
		for _, rec := range op.Messages {
			q.msgs[rec.ID] = &message{
				id:         rec.ID,
				body:       rec.Body,
				enqueuedAt: rec.EnqueuedAt,
			}
			q.order = append(q.order, rec.ID)
			if rec.Seq > q.idCounter {
				q.idCounter = rec.Seq
			}
		}
	case OpClaim:
		if m, ok := q.msgs[op.MessageID]; ok {
			m.claimed = true
			m.generation = op.Generation
			m.deadline = op.Deadline
		}
	case OpAck:
		delete(q.msgs, op.MessageID)
		q.removeFromOrder(op.MessageID)
	case OpNack:
		if m, ok := q.msgs[op.MessageID]; ok {
			m.claimed = false
			m.deadline = time.Time{}
			q.removeFromOrder(op.MessageID)
			q.order = append(q.order, op.MessageID)
		}
	}
}

func (q *Queue) removeFromOrder(id string) {
	for i, x := range q.order {
		if x == id {
			q.order = append(q.order[:i], q.order[i+1:]...)
			return
		}
	}
}
