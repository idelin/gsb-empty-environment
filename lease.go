package netsec

import (
	"errors"
	"sync"
)

var (
	ErrPoolExhausted = errors.New("netsec: no free interrupt vectors")
	ErrLeaseClosed   = errors.New("netsec: lease is closing, new submissions forbidden")
	ErrBadConfirm    = errors.New("netsec: confirmation for unknown submission")
)

// VectorID identifies a physical interrupt vector in the pool.
type VectorID int

// Event is a hardware interrupt carrying the queue generation it belongs to.
type Event struct {
	Vector     VectorID
	Generation uint64
	Payload    uint64
}

// DropRecord is evidence for an interrupt that could not be delivered.
type DropRecord struct {
	Vector     VectorID
	Generation uint64
	Payload    uint64
	Reason     string
}

type vectorState struct {
	generation uint64
	lease      *Lease
}

// Pool is a fixed-size pool of interrupt vectors leased to tenants.
type Pool struct {
	mu      sync.Mutex
	cond    sync.Cond
	vectors []vectorState
	free    []VectorID
	drops   []DropRecord
}

func NewPool(size int) *Pool {
	if size <= 0 {
		panic("netsec: pool size must be positive")
	}
	p := &Pool{
		vectors: make([]vectorState, size),
		free:    make([]VectorID, 0, size),
	}
	p.cond.L = &p.mu
	for i := size - 1; i >= 0; i-- {
		p.free = append(p.free, VectorID(i))
	}
	return p
}

// Acquire leases a vector and bumps its generation so interrupts from
// previous tenants can never match the new lease.
func (p *Pool) Acquire() (*Lease, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.free) == 0 {
		return nil, ErrPoolExhausted
	}
	vec := p.free[len(p.free)-1]
	p.free = p.free[:len(p.free)-1]
	vs := &p.vectors[vec]
	vs.generation++
	l := &Lease{
		pool:    p,
		vec:     vec,
		gen:     vs.generation,
		pending: make(map[uint64]struct{}),
	}
	vs.lease = l
	return l, nil
}

// Interrupt routes a hardware event to the current tenant of the vector.
// Events whose generation does not match the live lease (late or unknown
// generations) are dropped and recorded as evidence.
func (p *Pool) Interrupt(ev Event) bool {
	p.mu.Lock()
	vs := &p.vectors[ev.Vector]
	l := vs.lease
	if l == nil || vs.generation != ev.Generation {
		reason := "unknown generation"
		if l == nil {
			reason = "vector not leased"
		}
		p.drops = append(p.drops, DropRecord{
			Vector:     ev.Vector,
			Generation: ev.Generation,
			Payload:    ev.Payload,
			Reason:     reason,
		})
		p.mu.Unlock()
		return false
	}
	handler := l.handler
	p.mu.Unlock()
	if handler != nil {
		handler(ev)
	}
	return true
}

// Drops returns the recorded evidence of dropped interrupts.
func (p *Pool) Drops() []DropRecord {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]DropRecord, len(p.drops))
	copy(out, p.drops)
	return out
}

// Lease is an exclusive grant of one interrupt vector for one generation.
type Lease struct {
	pool *Pool
	vec  VectorID
	gen  uint64

	handler func(Event)
	closing bool
	closed  bool
	nextSeq uint64
	pending map[uint64]struct{}
}

func (l *Lease) Vector() VectorID   { return l.vec }
func (l *Lease) Generation() uint64 { return l.gen }

// SetHandler installs the tenant's delivery callback for matching events.
func (l *Lease) SetHandler(h func(Event)) {
	l.pool.mu.Lock()
	defer l.pool.mu.Unlock()
	l.handler = h
}

// Submit registers an in-flight request; the returned sequence number must
// be confirmed by the driver. Submissions are forbidden once Close begins.
func (l *Lease) Submit() (uint64, error) {
	l.pool.mu.Lock()
	defer l.pool.mu.Unlock()
	if l.closing {
		return 0, ErrLeaseClosed
	}
	l.nextSeq++
	l.pending[l.nextSeq] = struct{}{}
	return l.nextSeq, nil
}

// Confirm acknowledges a submission; driver confirmations may arrive in any
// order.
func (l *Lease) Confirm(seq uint64) error {
	l.pool.mu.Lock()
	defer l.pool.mu.Unlock()
	if _, ok := l.pending[seq]; !ok {
		return ErrBadConfirm
	}
	delete(l.pending, seq)
	if l.closing && len(l.pending) == 0 {
		l.pool.cond.Broadcast()
	}
	return nil
}

// Close forbids new submissions, drains the old generation's in-flight
// interrupts, and only then returns the vector to the pool for reuse.
func (l *Lease) Close() {
	p := l.pool
	p.mu.Lock()
	defer p.mu.Unlock()
	if l.closed {
		return
	}
	l.closing = true
	for len(l.pending) > 0 {
		p.cond.Wait()
	}
	l.closed = true
	vs := &p.vectors[l.vec]
	vs.lease = nil
	p.free = append(p.free, l.vec)
}
