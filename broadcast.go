// Package bcast implements a bounded broadcast queue: one published payload
// is shared by every subscriber registered at commit time, and the slot is
// reclaimed only after all intended receivers have acknowledged it.
//
// Semantics:
//   - Publish is two-phase: Begin reserves a sequence number and a capacity
//     slot; Commit snapshots the current subscriber set as the receive set
//     (the ledger of outstanding acknowledgements) and makes the payload
//     visible.
//   - A subscriber that joins after a commit never sees that message: new
//     subscribers do not retroactively acknowledge or receive old messages.
//   - Unsubscribe counts as acknowledging every unread item of the leaving
//     subscriber, which may reclaim messages and free capacity.
//   - Cancel on a Publication that was already committed is a no-op but
//     still returns the committed sequence number; Cancel before Commit
//     discards the reservation and frees the slot.
package bcast

import (
	"errors"
	"sort"
	"sync"
)

// ErrFull is returned by Begin when every capacity slot is in use.
var ErrFull = errors.New("bcast: queue is full")

type message struct {
	seq     uint64
	payload interface{}
	// pending is the receive-set ledger: subscribers that still owe an ack.
	pending map[uint64]struct{}
}

// Publication is a reserved, not yet committed publish.
type Publication struct {
	q         *Queue
	seq       uint64
	payload   interface{}
	committed bool
	canceled  bool
}

// Queue is a bounded broadcast queue. It is safe for concurrent use.
type Queue struct {
	mu        sync.Mutex
	capacity  int
	used      int // reserved publications + live (committed, unreclaimed) messages
	nextSeq   uint64
	nextSubID uint64
	subs      map[uint64]*Subscriber
	live      map[uint64]*message
	reserved  map[uint64]*Publication
	reclaimed []uint64
}

// New returns a Queue holding at most capacity unreclaimed slots.
func New(capacity int) *Queue {
	if capacity <= 0 {
		panic("bcast: capacity must be positive")
	}
	return &Queue{
		capacity: capacity,
		subs:     make(map[uint64]*Subscriber),
		live:     make(map[uint64]*message),
		reserved: make(map[uint64]*Publication),
	}
}

// Begin reserves a sequence number and a capacity slot for payload.
func (q *Queue) Begin(payload interface{}) (*Publication, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.used == q.capacity {
		return nil, ErrFull
	}
	seq := q.nextSeq
	q.nextSeq++
	q.used++
	p := &Publication{q: q, seq: seq, payload: payload}
	q.reserved[seq] = p
	return p, nil
}

// Publish is a convenience for Begin followed by Commit.
func (q *Queue) Publish(payload interface{}) (uint64, error) {
	p, err := q.Begin(payload)
	if err != nil {
		return 0, err
	}
	return p.Commit(), nil
}

// Commit snapshots the current subscribers as the receive set and makes the
// payload visible to them. It is idempotent and returns the sequence number;
// it returns 0 if the publication was canceled.
func (p *Publication) Commit() uint64 {
	p.q.mu.Lock()
	defer p.q.mu.Unlock()
	if p.committed {
		return p.seq
	}
	if p.canceled {
		return 0
	}
	p.committed = true
	delete(p.q.reserved, p.seq)
	m := &message{seq: p.seq, payload: p.payload, pending: make(map[uint64]struct{})}
	for id := range p.q.subs {
		m.pending[id] = struct{}{}
	}
	if len(m.pending) == 0 {
		// No receivers: retire immediately and free the slot.
		p.q.reclaimLocked(p.seq)
	} else {
		p.q.live[p.seq] = m
	}
	return p.seq
}

// Cancel aborts the publication. If it was already committed the cancel is a
// no-op and the committed sequence number is still returned with ok=true.
// Otherwise the reservation is discarded, the slot freed, and ok is false.
func (p *Publication) Cancel() (seq uint64, ok bool) {
	p.q.mu.Lock()
	defer p.q.mu.Unlock()
	if p.committed {
		return p.seq, true
	}
	if p.canceled {
		return 0, false
	}
	p.canceled = true
	delete(p.q.reserved, p.seq)
	p.q.used--
	return 0, false
}

// Subscriber receives each message committed while it is registered, in
// sequence order, and acknowledges them explicitly.
type Subscriber struct {
	q      *Queue
	id     uint64
	active bool
}

// Subscribe registers a new subscriber. It does not see any message committed
// before this call.
func (q *Queue) Subscribe() *Subscriber {
	q.mu.Lock()
	defer q.mu.Unlock()
	id := q.nextSubID
	q.nextSubID++
	s := &Subscriber{q: q, id: id, active: true}
	q.subs[id] = s
	return s
}

// Recv returns the oldest message still awaiting this subscriber's ack.
// Receiving does not acknowledge; the same message is returned again until
// Ack is called.
func (s *Subscriber) Recv() (uint64, interface{}, bool) {
	q := s.q
	q.mu.Lock()
	defer q.mu.Unlock()
	if !s.active {
		return 0, nil, false
	}
	var best uint64
	found := false
	for seq, m := range q.live {
		if _, ok := m.pending[s.id]; !ok {
			continue
		}
		if !found || seq < best {
			best, found = seq, true
		}
	}
	if !found {
		return 0, nil, false
	}
	return best, q.live[best].payload, true
}

// Ack confirms receipt of seq. When the last pending subscriber acks, the
// message is reclaimed and its capacity slot freed.
func (s *Subscriber) Ack(seq uint64) bool {
	q := s.q
	q.mu.Lock()
	defer q.mu.Unlock()
	if !s.active {
		return false
	}
	m, ok := q.live[seq]
	if !ok {
		return false
	}
	if _, ok := m.pending[s.id]; !ok {
		return false
	}
	delete(m.pending, s.id)
	if len(m.pending) == 0 {
		q.reclaimLocked(seq)
	}
	return true
}

// Unsubscribe removes the subscriber. Leaving counts as acknowledging all of
// its unread items, which may reclaim messages and free capacity.
func (s *Subscriber) Unsubscribe() {
	q := s.q
	q.mu.Lock()
	defer q.mu.Unlock()
	if !s.active {
		return
	}
	s.active = false
	delete(q.subs, s.id)
	var drained []uint64
	for seq, m := range q.live {
		if _, ok := m.pending[s.id]; !ok {
			continue
		}
		delete(m.pending, s.id)
		if len(m.pending) == 0 {
			drained = append(drained, seq)
		}
	}
	// Reclaim in sequence order so the ledger is deterministic.
	sort.Slice(drained, func(i, j int) bool { return drained[i] < drained[j] })
	for _, seq := range drained {
		q.reclaimLocked(seq)
	}
}

func (q *Queue) reclaimLocked(seq uint64) {
	delete(q.live, seq)
	q.used--
	q.reclaimed = append(q.reclaimed, seq)
}

// Used reports the number of capacity slots currently held (reserved
// publications plus live messages).
func (q *Queue) Used() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.used
}

// Reclaimed returns the ledger of reclaimed sequence numbers, in reclaim
// order.
func (q *Queue) Reclaimed() []uint64 {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]uint64, len(q.reclaimed))
	copy(out, q.reclaimed)
	return out
}

// Live returns the sequence numbers of committed, not yet reclaimed messages,
// in ascending order.
func (q *Queue) Live() []uint64 {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]uint64, 0, len(q.live))
	for seq := range q.live {
		out = append(out, seq)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Pending returns the receive set of seq: subscribers that still owe an ack,
// in ascending subscriber-id order. ok is false if seq is not live.
func (q *Queue) Pending(seq uint64) (ids []uint64, ok bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	m, ok := q.live[seq]
	if !ok {
		return nil, false
	}
	ids = make([]uint64, 0, len(m.pending))
	for id := range m.pending {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids, true
}
