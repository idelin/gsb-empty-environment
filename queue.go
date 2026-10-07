package msgqueue

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

var (
	ErrQueueEmpty     = errors.New("queue: no pending message")
	ErrInvalidToken   = errors.New("queue: invalid ack token")
	ErrLeaseExpired   = errors.New("queue: lease already expired")
	ErrUnknownMessage = errors.New("queue: unknown message")
)

// Message is a unit of work. Deliveries counts real hand-outs to consumers
// and drives the dead-letter threshold.
type Message struct {
	ID         string
	Body       []byte
	Deliveries int
}

func (m *Message) clone() *Message {
	body := make([]byte, len(m.Body))
	copy(body, m.Body)
	return &Message{ID: m.ID, Body: body, Deliveries: m.Deliveries}
}

type lease struct {
	msg       *Message
	token     string
	expiresAt time.Time
}

// Queue is a consumer queue with explicit pending/in-flight/dead states.
type Queue struct {
	mu            sync.Mutex
	clock         Clock
	leaseDuration time.Duration
	maxDeliveries int

	pending  []*Message
	inflight map[string]*lease // ack token -> lease
	dead     []*Message
	tokenSeq uint64
}

func New(clock Clock, leaseDuration time.Duration, maxDeliveries int) *Queue {
	return &Queue{
		clock:         clock,
		leaseDuration: leaseDuration,
		maxDeliveries: maxDeliveries,
		inflight:      make(map[string]*lease),
	}
}

// Enqueue adds a message to the pending set.
func (q *Queue) Enqueue(m *Message) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.pending = append(q.pending, m.clone())
}

// Deliver moves the head pending message to in-flight, increments its real
// delivery count, and returns it with a one-shot ack token.
func (q *Queue) Deliver() (*Message, string, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.reapExpiredLocked(q.clock.Now())
	if len(q.pending) == 0 {
		return nil, "", ErrQueueEmpty
	}
	m := q.pending[0]
	q.pending = q.pending[1:]
	m.Deliveries++
	q.tokenSeq++
	token := fmt.Sprintf("lease-%d", q.tokenSeq)
	q.inflight[token] = &lease{
		msg:       m,
		token:     token,
		expiresAt: q.clock.Now().Add(q.leaseDuration),
	}
	return m.clone(), token, nil
}

// Ack completes a delivery. Tokens of expired or recovered-away leases are
// rejected.
func (q *Queue) Ack(token string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	l, ok := q.inflight[token]
	if !ok {
		return ErrInvalidToken
	}
	if !q.clock.Now().Before(l.expiresAt) {
		q.reapLocked(token, q.clock.Now())
		return ErrLeaseExpired
	}
	delete(q.inflight, token)
	return nil
}

// Nack returns an in-flight message to pending immediately, or dead-letters
// it when the real delivery count reached the threshold.
func (q *Queue) Nack(token string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if _, ok := q.inflight[token]; !ok {
		return ErrInvalidToken
	}
	q.reapLocked(token, q.clock.Now())
	return nil
}

// ReapExpired moves expired in-flight messages back to pending (or dead).
func (q *Queue) ReapExpired() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.reapExpiredLocked(q.clock.Now())
}

// reapLocked removes one lease and routes its message by delivery count.
func (q *Queue) reapLocked(token string, now time.Time) {
	l, ok := q.inflight[token]
	if !ok {
		return
	}
	delete(q.inflight, token)
	if l.msg.Deliveries >= q.maxDeliveries {
		q.dead = append(q.dead, l.msg)
		return
	}
	q.pending = append([]*Message{l.msg}, q.pending...)
}

func (q *Queue) reapExpiredLocked(now time.Time) {
	for token, l := range q.inflight {
		if !now.Before(l.expiresAt) {
			q.reapLocked(token, now)
		}
	}
}

// Stats reports the size of each state partition.
func (q *Queue) Stats() (pending, inflight, dead int) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.reapExpiredLocked(q.clock.Now())
	return len(q.pending), len(q.inflight), len(q.dead)
}

// Dead returns a copy of the dead-letter list.
func (q *Queue) Dead() []*Message {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]*Message, len(q.dead))
	for i, m := range q.dead {
		out[i] = m.clone()
	}
	return out
}

// Pending returns a copy of the pending list.
func (q *Queue) Pending() []*Message {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.reapExpiredLocked(q.clock.Now())
	out := make([]*Message, len(q.pending))
	for i, m := range q.pending {
		out[i] = m.clone()
	}
	return out
}
