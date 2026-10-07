// Package mq implements a durable FIFO mailbox queue: messages are claimed
// without being deleted, deleted only on confirm, requeued to the tail on
// reject, and redelivered after a visibility timeout — after which receipts
// from the older generation become invalid.
package mq

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

var (
	// ErrEmpty is returned by Take when no message is currently visible.
	ErrEmpty = errors.New("mq: queue is empty")
	// ErrStaleReceipt is returned when a receipt does not match the current
	// in-flight generation/token — e.g. a duplicate confirm, a confirm after
	// timeout redelivery, or a receipt that was never issued.
	ErrStaleReceipt = errors.New("mq: stale or unknown receipt")
)

// Message is a queued mailbox item. IDs are unique per queue instance.
type Message struct {
	ID   string
	Body []byte
}

// Receipt is the claim token returned by Take. It binds the message ID to a
// generation and a unique token; only the latest generation's receipt can
// confirm or reject the message.
type Receipt struct {
	id    string
	gen   uint64
	token uint64
}

// MessageID returns the ID of the claimed message.
func (r Receipt) MessageID() string { return r.id }

// Clock supplies the current time; injectable for tests.
type Clock func() time.Time

type inflight struct {
	msg      Message
	gen      uint64
	token    uint64
	deadline time.Time
}

// Queue is a durable FIFO mailbox. It is safe for concurrent use.
type Queue struct {
	mu         sync.Mutex
	wal        Persister
	clock      Clock
	visTimeout time.Duration
	instance   uint64

	ready    []Message // visible, FIFO
	inflight map[string]*inflight
	gens     map[string]uint64 // last issued generation per message ID
	tokenSeq uint64
	idSeq    uint64
}

// Option configures a Queue.
type Option func(*Queue)

// WithVisibilityTimeout sets how long a claimed message stays invisible
// before it becomes eligible for redelivery. Default: 30s.
func WithVisibilityTimeout(d time.Duration) Option {
	return func(q *Queue) { q.visTimeout = d }
}

// WithClock overrides the clock (tests).
func WithClock(c Clock) Option {
	return func(q *Queue) { q.clock = c }
}

// Open creates a queue on top of p, replaying any previously persisted
// records so state survives restarts.
func Open(p Persister, opts ...Option) (*Queue, error) {
	q := &Queue{
		wal:        p,
		clock:      time.Now,
		visTimeout: 30 * time.Second,
		instance:   randUint64(),
		inflight:   map[string]*inflight{},
		gens:       map[string]uint64{},
	}
	for _, o := range opts {
		o(q)
	}
	recs, err := p.Replay()
	if err != nil {
		return nil, err
	}
	for _, r := range recs {
		q.apply(r)
	}
	return q, nil
}

// apply mutates in-memory state from a persisted record (used during replay).
func (q *Queue) apply(r Record) {
	switch r.Op {
	case OpEnqueue:
		q.ready = append(q.ready, r.Msg)
	case OpInflight:
		q.removeReady(r.Msg.ID)
		q.inflight[r.Msg.ID] = &inflight{msg: r.Msg, gen: r.Gen, token: r.Token, deadline: r.Deadline}
		if r.Gen > q.gens[r.Msg.ID] {
			q.gens[r.Msg.ID] = r.Gen
		}
		if r.Token > q.tokenSeq {
			q.tokenSeq = r.Token
		}
	case OpAck:
		delete(q.inflight, r.Msg.ID)
		q.removeReady(r.Msg.ID)
	case OpRequeue:
		if fl, ok := q.inflight[r.Msg.ID]; ok {
			delete(q.inflight, r.Msg.ID)
			q.ready = append(q.ready, fl.msg)
		}
	}
}

func (q *Queue) removeReady(id string) {
	for i, m := range q.ready {
		if m.ID == id {
			q.ready = append(q.ready[:i], q.ready[i+1:]...)
			return
		}
	}
}

// Send transactionally enqueues all bodies: either every message becomes
// visible (in argument order, at the tail) or — if persistence fails — none
// does.
func (q *Queue) Send(bodies ...[]byte) ([]Message, error) {
	if len(bodies) == 0 {
		return nil, nil
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	msgs := make([]Message, len(bodies))
	recs := make([]Record, len(bodies))
	for i, b := range bodies {
		q.idSeq++
		msgs[i] = Message{
			ID:   fmt.Sprintf("%016x-%d", q.instance, q.idSeq),
			Body: append([]byte(nil), b...),
		}
		recs[i] = Record{Op: OpEnqueue, Msg: msgs[i]}
	}
	if err := q.wal.Apply(recs); err != nil {
		return nil, fmt.Errorf("mq: send not persisted, nothing enqueued: %w", err)
	}
	q.ready = append(q.ready, msgs...)
	return msgs, nil
}

// Take claims the head message without deleting it: it becomes invisible
// until the visibility deadline. The returned receipt must be used with
// Confirm or Reject.
func (q *Queue) Take() (Message, Receipt, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.sweepLocked(q.clock())
	if len(q.ready) == 0 {
		return Message{}, Receipt{}, ErrEmpty
	}
	msg := q.ready[0]
	gen := q.gens[msg.ID] + 1
	q.tokenSeq++
	rec := Record{
		Op:       OpInflight,
		Msg:      msg,
		Gen:      gen,
		Token:    q.tokenSeq,
		Deadline: q.clock().Add(q.visTimeout),
	}
	if err := q.wal.Apply([]Record{rec}); err != nil {
		return Message{}, Receipt{}, fmt.Errorf("mq: claim not persisted: %w", err)
	}
	q.ready = q.ready[1:]
	q.gens[msg.ID] = gen
	q.inflight[msg.ID] = &inflight{msg: msg, gen: gen, token: rec.Token, deadline: rec.Deadline}
	return msg, Receipt{id: msg.ID, gen: gen, token: rec.Token}, nil
}

// Confirm deletes the claimed message. It fails with ErrStaleReceipt if the
// receipt is not the latest generation — notably after a timeout redelivery
// or a previous confirm of the same receipt.
func (q *Queue) Confirm(r Receipt) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	fl, err := q.checkLocked(r)
	if err != nil {
		return err
	}
	if err := q.wal.Apply([]Record{{Op: OpAck, Msg: fl.msg, Gen: r.gen, Token: r.token}}); err != nil {
		return fmt.Errorf("mq: ack not persisted, message kept in flight: %w", err)
	}
	delete(q.inflight, r.id)
	return nil
}

// Reject returns the claimed message to the tail of the visible queue.
func (q *Queue) Reject(r Receipt) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	fl, err := q.checkLocked(r)
	if err != nil {
		return err
	}
	if err := q.wal.Apply([]Record{{Op: OpRequeue, Msg: fl.msg, Gen: r.gen, Token: r.token}}); err != nil {
		return fmt.Errorf("mq: requeue not persisted, message kept in flight: %w", err)
	}
	delete(q.inflight, r.id)
	q.ready = append(q.ready, fl.msg)
	return nil
}

func (q *Queue) checkLocked(r Receipt) (*inflight, error) {
	fl, ok := q.inflight[r.id]
	if !ok || fl.gen != r.gen || fl.token != r.token {
		return nil, ErrStaleReceipt
	}
	return fl, nil
}

// sweepLocked moves expired in-flight messages back to the visible tail,
// oldest deadline first. Their outstanding receipts become stale.
func (q *Queue) sweepLocked(now time.Time) {
	var expired []*inflight
	for _, fl := range q.inflight {
		if !fl.deadline.After(now) {
			expired = append(expired, fl)
		}
	}
	sort.Slice(expired, func(i, j int) bool {
		if expired[i].deadline.Equal(expired[j].deadline) {
			return expired[i].msg.ID < expired[j].msg.ID
		}
		return expired[i].deadline.Before(expired[j].deadline)
	})
	for _, fl := range expired {
		delete(q.inflight, fl.msg.ID)
		q.ready = append(q.ready, fl.msg)
	}
}

// Len reports the number of currently visible (ready) messages.
func (q *Queue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.sweepLocked(q.clock())
	return len(q.ready)
}

// InFlight reports the number of claimed, not-yet-expired messages.
func (q *Queue) InFlight() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.sweepLocked(q.clock())
	return len(q.inflight)
}

func randUint64() uint64 {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return binary.BigEndian.Uint64(b[:])
}
