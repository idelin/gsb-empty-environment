// Package queue implements a consumer queue that distinguishes pending
// messages from in-flight (leased) ones, supports crash recovery via
// snapshots, and dead-letters messages after too many real deliveries.
package queue

import (
	"errors"
	"fmt"
	"time"
)

// Message is the unit of work. Deliveries counts how many times the
// message has actually been handed to a consumer; it travels with the
// message across pending/in-flight transitions and snapshots.
type Message struct {
	ID         string
	Payload    []byte
	Deliveries int
}

// Delivery is a leased message plus the token required to ack it.
type Delivery struct {
	Message   Message
	Token     string
	ExpiresAt time.Time
}

var (
	ErrEmpty          = errors.New("queue: no message available")
	ErrInvalidToken   = errors.New("queue: ack token is invalid or expired")
	ErrMessageMissing = errors.New("queue: message not found")
)

type lease struct {
	msg      Message
	token    string
	deadline time.Time
}

// Queue holds messages in three disjoint states: pending, in-flight
// (leased to a consumer) and dead-lettered.
type Queue struct {
	clock         Clock
	leaseDuration time.Duration
	maxDeliveries int

	pending  []Message
	inflight map[string]lease // keyed by message ID
	dlq      []Message
	dlqSet   map[string]bool

	seq uint64 // monotonic token sequence, persisted in snapshots
}

func New(clock Clock, leaseDuration time.Duration, maxDeliveries int) *Queue {
	return &Queue{
		clock:         clock,
		leaseDuration: leaseDuration,
		maxDeliveries: maxDeliveries,
		inflight:      make(map[string]lease),
		dlqSet:        make(map[string]bool),
	}
}

// Enqueue adds a new message to the pending set.
func (q *Queue) Enqueue(id string, payload []byte) {
	q.pending = append(q.pending, Message{ID: id, Payload: payload})
}

// nextToken mints a unique ack token. The sequence is persisted in
// snapshots so tokens never collide across restore cycles.
func (q *Queue) nextToken() string {
	q.seq++
	return fmt.Sprintf("tok-%d", q.seq)
}

// requeueExpired moves leases whose deadline has passed back to pending.
// Their tokens are dropped, which invalidates any outstanding ack token.
func (q *Queue) requeueExpired() {
	now := q.clock.Now()
	for id, l := range q.inflight {
		if !l.deadline.After(now) {
			delete(q.inflight, id)
			q.pending = append(q.pending, l.msg)
		}
	}
}

// deadLetter moves a message to the DLQ exactly once.
func (q *Queue) deadLetter(msg Message) {
	if q.dlqSet[msg.ID] {
		return
	}
	q.dlqSet[msg.ID] = true
	q.dlq = append(q.dlq, msg)
}

// Dequeue hands out the next pending message as a leased delivery.
// Messages that reached the delivery threshold are dead-lettered instead.
func (q *Queue) Dequeue() (Delivery, error) {
	q.requeueExpired()
	for len(q.pending) > 0 {
		msg := q.pending[0]
		q.pending = q.pending[1:]
		if msg.Deliveries >= q.maxDeliveries {
			q.deadLetter(msg)
			continue
		}
		msg.Deliveries++
		d := Delivery{
			Message:   msg,
			Token:     q.nextToken(),
			ExpiresAt: q.clock.Now().Add(q.leaseDuration),
		}
		q.inflight[msg.ID] = lease{msg: msg, token: d.Token, deadline: d.ExpiresAt}
		return d, nil
	}
	return Delivery{}, ErrEmpty
}

// Ack completes a delivery. Only the token of the current lease is
// accepted; stale tokens (e.g. from before a restore) are rejected.
func (q *Queue) Ack(token string) error {
	for id, l := range q.inflight {
		if l.token == token {
			delete(q.inflight, id)
			return nil
		}
	}
	return ErrInvalidToken
}

// Nack returns a leased message to pending without counting a new delivery.
func (q *Queue) Nack(token string) error {
	for id, l := range q.inflight {
		if l.token == token {
			delete(q.inflight, id)
			q.pending = append(q.pending, l.msg)
			return nil
		}
	}
	return ErrInvalidToken
}

// DLQ returns a copy of the dead-lettered messages.
func (q *Queue) DLQ() []Message {
	out := make([]Message, len(q.dlq))
	copy(out, q.dlq)
	return out
}

// PendingLen reports the number of messages waiting to be delivered.
func (q *Queue) PendingLen() int {
	q.requeueExpired()
	return len(q.pending)
}

// InFlightLen reports the number of messages currently leased.
func (q *Queue) InFlightLen() int {
	q.requeueExpired()
	return len(q.inflight)
}
