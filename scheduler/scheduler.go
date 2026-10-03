// Package scheduler implements multi-tenant resource isolation with a
// Deficit Round Robin (DRR) weighted fair queue. Each tenant gets an
// isolated FIFO queue, a hard quota on queued and in-flight jobs, and a
// weight that determines its share of service: on every visit a tenant's
// deficit counter is credited weight*quantum units, and jobs are dispatched
// while the head job's cost fits the deficit. A mutex guards all state, so
// the scheduler is safe for concurrent submit/dispatch/complete.
package scheduler

import (
	"errors"
	"fmt"
	"sync"
)

var (
	ErrUnknownTenant = errors.New("scheduler: unknown tenant")
	ErrQueueFull     = errors.New("scheduler: tenant queue quota exceeded")
	ErrClosed        = errors.New("scheduler: closed")
	ErrTenantExists  = errors.New("scheduler: tenant already exists")
	ErrTenantBusy    = errors.New("scheduler: tenant still has queued or in-flight jobs")
	ErrInvalidConfig = errors.New("scheduler: invalid tenant configuration")
	ErrInflightLeak  = errors.New("scheduler: jobs still in flight at close")
	ErrQueueLeak     = errors.New("scheduler: jobs still queued at close")
)

// Job is a unit of work. Cost is the number of service units the job
// consumes from its tenant's deficit (e.g. byte size of a packet).
type Job struct {
	ID      string
	Cost    uint64
	Payload interface{}
}

// Stats is a consistent snapshot of one tenant's counters.
type Stats struct {
	Queued    int
	Inflight  int
	Served    uint64 // total cost units dispatched
	Deficit   uint64
	Submitted uint64
	Rejected  uint64
}

type tenant struct {
	id        string
	weight    uint64
	maxQueue  int
	maxFlight int
	queue     []*Job
	credited  bool // received quantum during the current continuous visit
	stats     Stats
}

// Scheduler is a concurrency-safe multi-tenant DRR fair queue.
type Scheduler struct {
	mu      sync.Mutex
	quantum uint64
	tenants map[string]*tenant
	order   []string // stable round-robin order of tenant ids
	pos     int      // next tenant to visit
	closed  bool
}

// New creates a scheduler. quantum is the base credit unit; a tenant with
// weight w is credited w*quantum service units per round.
func New(quantum uint64) *Scheduler {
	if quantum == 0 {
		quantum = 1
	}
	return &Scheduler{
		quantum: quantum,
		tenants: make(map[string]*tenant),
	}
}

// AddTenant registers an isolated tenant queue.
// weight >= 1 sets the DRR share; maxQueue and maxFlight are hard quotas
// (0 means "no jobs may queue / run").
func (s *Scheduler) AddTenant(id string, weight uint64, maxQueue, maxFlight int) error {
	if id == "" || weight == 0 || maxQueue < 0 || maxFlight < 0 {
		return ErrInvalidConfig
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	if _, ok := s.tenants[id]; ok {
		return ErrTenantExists
	}
	s.tenants[id] = &tenant{id: id, weight: weight, maxQueue: maxQueue, maxFlight: maxFlight}
	s.order = append(s.order, id)
	return nil
}

// RemoveTenant detaches a tenant. It refuses while any job is queued or
// in flight, guaranteeing no work is silently dropped or leaked.
func (s *Scheduler) RemoveTenant(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tenants[id]
	if !ok {
		return ErrUnknownTenant
	}
	if len(t.queue) > 0 || t.stats.Inflight > 0 {
		return ErrTenantBusy
	}
	delete(s.tenants, id)
	for i, tid := range s.order {
		if tid == id {
			s.order = append(s.order[:i], s.order[i+1:]...)
			if i < s.pos {
				s.pos--
			}
			if s.pos >= len(s.order) {
				s.pos = 0
			}
			break
		}
	}
	return nil
}

// Submit enqueues a job for a tenant. It fails with ErrQueueFull when the
// tenant's queue quota is reached, isolating a flooding tenant from others.
func (s *Scheduler) Submit(id string, job *Job) error {
	if job == nil || job.Cost == 0 {
		return ErrInvalidConfig
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	t, ok := s.tenants[id]
	if !ok {
		return ErrUnknownTenant
	}
	if len(t.queue) >= t.maxQueue {
		t.stats.Rejected++
		return ErrQueueFull
	}
	t.queue = append(t.queue, job)
	t.stats.Queued = len(t.queue)
	t.stats.Submitted++
	return nil
}

// Next dispatches the next job according to weighted DRR. It returns
// (nil, nil) when no tenant can currently dispatch (empty queues or all
// in-flight quotas exhausted). The caller must call Complete when the job
// finishes to release its in-flight slot.
func (s *Scheduler) Next() (*Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, ErrClosed
	}
	n := len(s.order)
	for {
		// One round: visit every tenant once. Eligible tenants (queued
		// work + free in-flight slot) are re-credited once per round, so
		// their deficit grows until the head job fits.
		eligible := false
		for scanned := 0; scanned < n; scanned++ {
			if s.pos >= n {
				s.pos = 0
			}
			t := s.tenants[s.order[s.pos]]
			if len(t.queue) == 0 || t.stats.Inflight >= t.maxFlight {
				if len(t.queue) == 0 {
					t.stats.Deficit = 0 // idle tenants may not bank credit
				}
				t.credited = false
				s.pos = (s.pos + 1) % n
				continue
			}
			eligible = true
			if !t.credited {
				t.stats.Deficit += t.weight * s.quantum
				t.credited = true
			}
			head := t.queue[0]
			if head.Cost > t.stats.Deficit {
				// Deficit carries over to the next round.
				t.credited = false
				s.pos = (s.pos + 1) % n
				continue
			}
			t.stats.Deficit -= head.Cost
			t.queue = t.queue[1:]
			if len(t.queue) == 0 {
				t.stats.Deficit = 0
				t.credited = false
			}
			t.stats.Queued = len(t.queue)
			t.stats.Inflight++
			t.stats.Served += head.Cost
			// Stay on this tenant: it keeps dispatching while its
			// remaining deficit covers the head job, which is what
			// makes the service share proportional to weight.
			return head, nil
		}
		if !eligible {
			return nil, nil
		}
	}
}

// Complete releases the in-flight slot held by a dispatched job.
func (s *Scheduler) Complete(id string, job *Job) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tenants[id]
	if !ok {
		return ErrUnknownTenant
	}
	if t.stats.Inflight <= 0 {
		return fmt.Errorf("scheduler: tenant %q: complete without dispatch", id)
	}
	t.stats.Inflight--
	return nil
}

// Stats returns a snapshot of one tenant's counters.
func (s *Scheduler) Stats(id string) (Stats, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tenants[id]
	if !ok {
		return Stats{}, ErrUnknownTenant
	}
	st := t.stats
	st.Queued = len(t.queue)
	return st, nil
}

// Close shuts the scheduler down. It enforces the strict no-leak contract:
// every submitted job must have been dispatched and completed, and every
// tenant queue must be empty, otherwise an error is returned and the
// scheduler stays inspectable via Stats.
func (s *Scheduler) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range s.order {
		t := s.tenants[id]
		if t.stats.Inflight > 0 {
			return fmt.Errorf("%v: tenant %q has %d", ErrInflightLeak, id, t.stats.Inflight)
		}
		if len(t.queue) > 0 {
			return fmt.Errorf("%v: tenant %q has %d", ErrQueueLeak, id, len(t.queue))
		}
	}
	s.closed = true
	return nil
}
