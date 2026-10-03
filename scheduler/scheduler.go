// Package scheduler implements a multi-tenant, resource-isolating,
// weighted-fair job scheduler based on Deficit Round Robin (DRR).
//
// Isolation: every tenant has a bounded queue and a quota of in-flight
// jobs; a tenant that exhausts its quota is skipped until it finishes
// work, so no tenant can starve or flood the others.
//
// Fairness: tenants are visited round-robin; on each visit a tenant's
// deficit counter is credited with weight*quantum and jobs are dispatched
// while the head job's cost fits the deficit. Deficits are capped to
// bound burstiness.
//
// All methods are safe for concurrent use.
package scheduler

import (
	"errors"
	"sync"
)

// ErrUnknownTenant is returned when referencing an unregistered tenant.
var ErrUnknownTenant = errors.New("scheduler: unknown tenant")

// ErrQueueFull is returned when a tenant's bounded queue is at capacity.
var ErrQueueFull = errors.New("scheduler: tenant queue full")

// ErrInvalidConfig is returned for non-positive weight/quota/queue sizes.
var ErrInvalidConfig = errors.New("scheduler: invalid tenant config")

// ErrNotInFlight is returned by Done when the tenant has no in-flight jobs.
var ErrNotInFlight = errors.New("scheduler: tenant has no in-flight jobs")

// deficitCapRounds bounds how many rounds of credit a tenant can bank,
// limiting burstiness after idle periods.
const deficitCapRounds = 8

// Job is a unit of work. Cost is charged against the tenant's deficit;
// values <= 0 are treated as 1.
type Job struct {
	ID      int64
	Cost    int64
	Payload interface{}
}

func (j Job) cost() int64 {
	if j.Cost <= 0 {
		return 1
	}
	return j.Cost
}

type tenant struct {
	weight   int64
	quota    int
	maxQueue int
	queue    []Job
	deficit  int64
	inflight int
}

// Stats is a point-in-time snapshot of scheduler counters.
type Stats struct {
	Dispatched int64
	Completed  int64
	InFlight   int64
	Queued     int64
}

// Scheduler is a DRR weighted-fair, quota-isolating multi-tenant scheduler.
type Scheduler struct {
	mu         sync.Mutex
	quantum    int64
	tenants    map[string]*tenant
	order      []string
	cur        int  // tenant currently being served
	credited   bool // whether cur has been credited this round
	dispatched int64
	completed  int64
}

// New creates a scheduler with the given per-round quantum. The credit a
// tenant receives per round is weight*quantum.
func New(quantum int64) *Scheduler {
	if quantum <= 0 {
		quantum = 1
	}
	return &Scheduler{
		quantum: quantum,
		tenants: make(map[string]*tenant),
	}
}

// AddTenant registers a tenant with a DRR weight, an in-flight quota and a
// bounded queue length. Re-adding an existing tenant is an error.
func (s *Scheduler) AddTenant(name string, weight int64, quota, maxQueue int) error {
	if weight <= 0 || quota <= 0 || maxQueue <= 0 {
		return ErrInvalidConfig
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.tenants[name]; ok {
		return ErrInvalidConfig
	}
	s.tenants[name] = &tenant{weight: weight, quota: quota, maxQueue: maxQueue}
	s.order = append(s.order, name)
	return nil
}

// RemoveTenant unregisters a tenant. It fails while the tenant still has
// queued or in-flight work, so removal never leaks jobs.
func (s *Scheduler) RemoveTenant(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tenants[name]
	if !ok {
		return ErrUnknownTenant
	}
	if len(t.queue) != 0 || t.inflight != 0 {
		return ErrQueueFull // busy: reuse a plain error rather than leak
	}
	delete(s.tenants, name)
	for i, n := range s.order {
		if n == name {
			s.order = append(s.order[:i], s.order[i+1:]...)
			if i < s.cur {
				s.cur--
			}
			if s.cur >= len(s.order) {
				s.cur = 0
			}
			s.credited = false
			break
		}
	}
	return nil
}

// Submit enqueues a job for a tenant. It fails with ErrQueueFull when the
// tenant's bounded queue is full, providing backpressure isolation.
func (s *Scheduler) Submit(name string, job Job) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tenants[name]
	if !ok {
		return ErrUnknownTenant
	}
	if len(t.queue) >= t.maxQueue {
		return ErrQueueFull
	}
	t.queue = append(t.queue, job)
	return nil
}

// Dispatch selects the next job according to DRR weighted fairness while
// respecting per-tenant in-flight quotas. The third return value is false
// when no tenant currently has dispatchable work.
func (s *Scheduler) Dispatch() (string, Job, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := len(s.order)
	for {
		active := 0
		for scanned := 0; scanned < n; scanned++ {
			name := s.order[s.cur]
			t := s.tenants[name]
			if len(t.queue) == 0 || t.inflight >= t.quota {
				// Idle or quota-exhausted: move to the next tenant.
				s.cur = (s.cur + 1) % n
				s.credited = false
				continue
			}
			active++
			if !s.credited {
				// The cap is raised to the head cost so an expensive
				// job can always eventually be afforded.
				cap := t.weight * s.quantum * deficitCapRounds
				if head := t.queue[0]; head.cost() > cap {
					cap = head.cost()
				}
				t.deficit += t.weight * s.quantum
				if t.deficit > cap {
					t.deficit = cap
				}
				s.credited = true
			}
			head := t.queue[0]
			if head.cost() > t.deficit {
				// Cannot afford the head job: bank the deficit, move on.
				s.cur = (s.cur + 1) % n
				s.credited = false
				continue
			}
			t.queue = t.queue[1:]
			t.deficit -= head.cost()
			t.inflight++
			s.dispatched++
			// Stay on this tenant: it may dispatch more while its
			// deficit lasts (classic DRR per-round burst).
			return name, head, true
		}
		if active == 0 {
			return "", Job{}, false
		}
		// A full round credited every active tenant without a dispatch;
		// run another round so deficits can accumulate.
	}
}

// Done marks one in-flight job of the tenant as completed, releasing quota.
func (s *Scheduler) Done(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tenants[name]
	if !ok {
		return ErrUnknownTenant
	}
	if t.inflight <= 0 {
		return ErrNotInFlight
	}
	t.inflight--
	s.completed++
	return nil
}

// Stats returns a consistent snapshot of scheduler counters.
func (s *Scheduler) Stats() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	var inflight, queued int64
	for _, t := range s.tenants {
		inflight += int64(t.inflight)
		queued += int64(len(t.queue))
	}
	return Stats{
		Dispatched: s.dispatched,
		Completed:  s.completed,
		InFlight:   inflight,
		Queued:     queued,
	}
}

// TenantStats returns the queued and in-flight counts of one tenant.
func (s *Scheduler) TenantStats(name string) (queued, inflight int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tenants[name]
	if !ok {
		return 0, 0, ErrUnknownTenant
	}
	return len(t.queue), t.inflight, nil
}
