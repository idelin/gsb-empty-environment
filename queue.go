// Package jobqueue implements an in-memory job queue with priority aging,
// atomic batch claiming, lease-based occupancy, and generation-fenced
// completion receipts. Time comes from an injected Clock so tests can use
// a virtual clock.
package jobqueue

import (
	"errors"
	"sort"
	"sync"
	"time"
)

var (
	ErrUnknownJob       = errors.New("jobqueue: unknown job")
	ErrDuplicateJob     = errors.New("jobqueue: duplicate job id")
	ErrAlreadyCompleted = errors.New("jobqueue: job already completed")
	ErrNotLeased        = errors.New("jobqueue: job is not leased")
	ErrStaleReceipt     = errors.New("jobqueue: receipt does not match current lease generation")
)

type jobState int

const (
	statePending jobState = iota
	stateLeased
	stateCompleted
)

// Job is the unit of work submitted to the queue.
type Job struct {
	ID       string
	Payload  string
	Priority int
}

// ClaimedJob is a job handed out by Claim, carrying the lease generation
// that a later Complete call must present.
type ClaimedJob struct {
	Job
	Generation  uint64
	LeaseExpiry time.Time
}

type record struct {
	job        Job
	boost      int // aging boost accumulated via explicit Tick calls
	seq        uint64
	state      jobState
	generation uint64
	leaseUntil time.Time
}

func (r *record) effectivePriority() int { return r.job.Priority + r.boost }

// Queue is a priority job queue. It is safe for concurrent use.
type Queue struct {
	mu        sync.Mutex
	clock     Clock
	lease     time.Duration
	agingStep int
	seq       uint64
	jobs      map[string]*record
}

// NewQueue creates a queue. lease is the occupancy duration granted by
// Claim; agingStep is the priority boost applied to every waiting job on
// each explicit Tick (0 disables aging).
func NewQueue(clock Clock, lease time.Duration, agingStep int) *Queue {
	return &Queue{
		clock:     clock,
		lease:     lease,
		agingStep: agingStep,
		jobs:      make(map[string]*record),
	}
}

// Enqueue adds a job, assigning it the next monotonically increasing
// sequence number. The sequence number never changes for the life of the
// job, including across lease-expiry requeues.
func (q *Queue) Enqueue(j Job) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if _, ok := q.jobs[j.ID]; ok {
		return ErrDuplicateJob
	}
	q.seq++
	q.jobs[j.ID] = &record{job: j, seq: q.seq}
	return nil
}

// Tick applies priority aging: every job still waiting (pending, or leased
// but already expired) gains agingStep effective priority. Aging never
// happens implicitly; only explicit Tick calls advance it.
func (q *Queue) Tick() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.expireLocked(q.clock.Now())
	for _, r := range q.jobs {
		if r.state == statePending {
			r.boost += q.agingStep
		}
	}
}

// Claim atomically leases up to n of the highest-priority waiting jobs,
// ordered by effective priority (descending) then original sequence number
// (ascending). Leases that expired by the current clock time are requeued
// first, keeping their original sequence numbers. Each claimed job's
// generation is incremented, fencing off receipts from earlier leases.
func (q *Queue) Claim(n int) []ClaimedJob {
	q.mu.Lock()
	defer q.mu.Unlock()
	now := q.clock.Now()
	q.expireLocked(now)

	avail := make([]*record, 0, len(q.jobs))
	for _, r := range q.jobs {
		if r.state == statePending {
			avail = append(avail, r)
		}
	}
	sort.Slice(avail, func(i, j int) bool {
		a, b := avail[i], avail[j]
		if a.effectivePriority() != b.effectivePriority() {
			return a.effectivePriority() > b.effectivePriority()
		}
		return a.seq < b.seq
	})
	if n > len(avail) {
		n = len(avail)
	}
	out := make([]ClaimedJob, 0, n)
	for _, r := range avail[:n] {
		r.state = stateLeased
		r.generation++
		r.leaseUntil = now.Add(q.lease)
		out = append(out, ClaimedJob{
			Job:         r.job,
			Generation:  r.generation,
			LeaseExpiry: r.leaseUntil,
		})
	}
	return out
}

// Complete acknowledges a claimed job. The receipt generation must match
// the job's current lease generation; a stale generation (from an expired
// or superseded lease) is rejected. Completing twice is an error.
func (q *Queue) Complete(jobID string, generation uint64) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	r, ok := q.jobs[jobID]
	if !ok {
		return ErrUnknownJob
	}
	switch r.state {
	case stateCompleted:
		return ErrAlreadyCompleted
	case statePending:
		return ErrNotLeased
	}
	// Lazily expire the lease against the virtual clock: a receipt that
	// arrives after its lease deadline is stale even if nothing has
	// reclaimed the job yet.
	if !q.clock.Now().Before(r.leaseUntil) {
		r.state = statePending
		return ErrStaleReceipt
	}
	if generation != r.generation {
		return ErrStaleReceipt
	}
	r.state = stateCompleted
	r.leaseUntil = time.Time{}
	return nil
}

// Pending reports how many jobs are currently waiting to be claimed,
// after requeuing any leases expired by the current clock time.
func (q *Queue) Pending() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.expireLocked(q.clock.Now())
	n := 0
	for _, r := range q.jobs {
		if r.state == statePending {
			n++
		}
	}
	return n
}

// expireLocked moves leased jobs whose deadline has passed back to
// pending. Sequence numbers and accumulated aging boosts are preserved.
func (q *Queue) expireLocked(now time.Time) {
	for _, r := range q.jobs {
		if r.state == stateLeased && !now.Before(r.leaseUntil) {
			r.state = statePending
			r.leaseUntil = time.Time{}
		}
	}
}
