// Package jobq provides an in-memory priority job queue with explicit-tick
// priority aging, atomic batch claiming, and generation-checked leases.
package jobq

import (
	"container/heap"
	"errors"
	"sync"
	"time"
)

// ID identifies an enqueued job.
type ID uint64

// Receipt proves ownership of a particular lease generation.
type Receipt struct {
	ID         ID
	Generation uint64
}

// Lease is a claimed job held by a worker until ExpiresAt.
type Lease struct {
	ID        ID
	Payload   interface{}
	Receipt   Receipt
	ExpiresAt time.Time
}

var (
	// ErrUnknownJob is returned when the receipt names a job the queue no
	// longer tracks (never existed, or already completed).
	ErrUnknownJob = errors.New("jobq: unknown job")
	// ErrStaleReceipt is returned when the receipt's generation does not
	// match the job's current lease generation (expired or superseded).
	ErrStaleReceipt = errors.New("jobq: stale receipt")
)

type jobState int

const (
	stateReady jobState = iota
	stateLeased
)

type job struct {
	id         ID
	payload    interface{}
	base       int // base priority, higher is more urgent
	aged       int // aging boost accumulated via Tick
	seq        uint64
	state      jobState
	generation uint64
	expiresAt  time.Time
	heapIndex  int
}

func (j *job) effectivePriority() int { return j.base + j.aged }

// readyHeap orders by effective priority (desc), then enqueue sequence (asc).
type readyHeap []*job

func (h readyHeap) Len() int { return len(h) }

func (h readyHeap) Less(i, j int) bool {
	a, b := h[i], h[j]
	if a.effectivePriority() != b.effectivePriority() {
		return a.effectivePriority() > b.effectivePriority()
	}
	return a.seq < b.seq
}

func (h readyHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].heapIndex = i
	h[j].heapIndex = j
}

func (h *readyHeap) Push(x interface{}) {
	j := x.(*job)
	j.heapIndex = len(*h)
	*h = append(*h, j)
}

func (h *readyHeap) Pop() interface{} {
	old := *h
	n := len(old)
	j := old[n-1]
	old[n-1] = nil
	j.heapIndex = -1
	*h = old[:n-1]
	return j
}

// Queue is a concurrency-safe priority job queue.
type Queue struct {
	mu        sync.Mutex
	clock     Clock
	lease     time.Duration
	agingStep int
	nextSeq   uint64
	jobs      map[ID]*job
	ready     readyHeap
}

// Option configures a Queue.
type Option func(*Queue)

// WithClock sets the time source (defaults to the system clock).
func WithClock(c Clock) Option {
	return func(q *Queue) { q.clock = c }
}

// WithLease sets the lease duration for claimed jobs (default 30s).
func WithLease(d time.Duration) Option {
	return func(q *Queue) { q.lease = d }
}

// WithAgingStep sets the priority boost each ready job gains per Tick
// (default 1). Aging never applies implicitly with passing time.
func WithAgingStep(step int) Option {
	return func(q *Queue) { q.agingStep = step }
}

func New(opts ...Option) *Queue {
	q := &Queue{
		clock:     realClock{},
		lease:     30 * time.Second,
		agingStep: 1,
		jobs:      make(map[ID]*job),
	}
	for _, opt := range opts {
		opt(q)
	}
	return q
}

// Enqueue adds a job and returns its ID. The assigned sequence number is
// kept for the job's whole lifetime, including across lease-expiry requeues.
func (q *Queue) Enqueue(priority int, payload interface{}) ID {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.nextSeq++
	j := &job{
		id:      ID(q.nextSeq),
		payload: payload,
		base:    priority,
		seq:     q.nextSeq,
	}
	q.jobs[j.id] = j
	heap.Push(&q.ready, j)
	return j.id
}

// Tick applies exactly one aging step to every ready job. It is the only
// way priorities age; wall-clock time alone never changes ordering.
func (q *Queue) Tick() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.requeueExpiredLocked()
	for _, j := range q.ready {
		j.aged += q.agingStep
	}
	// Uniform aging preserves relative order, so the heap stays valid.
}

// Claim atomically leases up to n ready jobs, highest effective priority
// first, ties broken by original sequence number.
func (q *Queue) Claim(n int) []Lease {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.requeueExpiredLocked()
	if n > len(q.ready) {
		n = len(q.ready)
	}
	if n <= 0 {
		return nil
	}
	now := q.clock.Now()
	leases := make([]Lease, 0, n)
	for i := 0; i < n; i++ {
		j := heap.Pop(&q.ready).(*job)
		j.state = stateLeased
		j.generation++
		j.expiresAt = now.Add(q.lease)
		leases = append(leases, Lease{
			ID:        j.id,
			Payload:   j.payload,
			Receipt:   Receipt{ID: j.id, Generation: j.generation},
			ExpiresAt: j.expiresAt,
		})
	}
	return leases
}

// Complete finishes a leased job. The receipt must match the job's current
// lease generation; expired or superseded receipts are rejected.
func (q *Queue) Complete(r Receipt) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.requeueExpiredLocked()
	j, ok := q.jobs[r.ID]
	if !ok {
		return ErrUnknownJob
	}
	if j.state != stateLeased || j.generation != r.Generation {
		return ErrStaleReceipt
	}
	delete(q.jobs, r.ID)
	return nil
}

// RequeueExpired returns expired leases to the ready queue, keeping their
// original sequence numbers. It runs implicitly before Claim, Complete,
// Tick, and Len; calling it explicitly returns how many jobs were requeued.
func (q *Queue) RequeueExpired() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.requeueExpiredLocked()
}

func (q *Queue) requeueExpiredLocked() int {
	now := q.clock.Now()
	requeued := 0
	for _, j := range q.jobs {
		if j.state == stateLeased && !now.Before(j.expiresAt) {
			j.state = stateReady
			heap.Push(&q.ready, j)
			requeued++
		}
	}
	return requeued
}

// Len reports the number of ready (unleased) jobs.
func (q *Queue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.requeueExpiredLocked()
	return len(q.ready)
}
