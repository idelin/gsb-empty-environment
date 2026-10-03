// Package scheduler implements multi-tenant resource isolation with
// per-tenant quotas and Deficit Round Robin (DRR) weighted fair queuing.
//
// Isolation: every tenant owns an independent bounded queue and quota
// window; one tenant's load, quota exhaustion or removal never affects
// others. Fairness: dispatch follows DRR — each active tenant's deficit
// counter is topped up by weight*quantum once per round, and requests are
// dispatched while the deficit covers their cost, so long-run throughput
// converges to the weight ratio regardless of request sizes.
//
// The scheduler spawns no goroutines; all state is guarded by one mutex,
// so a drained scheduler retains no background resources.
package scheduler

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

var (
	ErrUnknownTenant = errors.New("scheduler: unknown tenant")
	ErrQuotaExceeded = errors.New("scheduler: tenant quota exceeded")
	ErrQueueFull     = errors.New("scheduler: tenant queue full")
	ErrTenantExists  = errors.New("scheduler: tenant already exists")
)

// Quota limits the cost units a tenant may submit per window.
// Limit <= 0 means unlimited.
type Quota struct {
	Limit  int64
	Window time.Duration
}

// Request is a unit of work. Cost is the dequeue cost charged against the
// tenant's deficit and quota; values <= 0 are normalized to 1.
type Request struct {
	Tenant  string
	Payload interface{}
	Cost    int64
}

func (r Request) normalizedCost() int64 {
	if r.Cost <= 0 {
		return 1
	}
	return r.Cost
}

// Config tunes a Scheduler.
type Config struct {
	Quantum  int64 // base DRR quantum; effective per-round credit = weight * Quantum
	MaxQueue int   // per-tenant queue capacity; <= 0 means a default of 1024
	Now      func() time.Time
}

const defaultMaxQueue = 1024

// TenantStats is a point-in-time snapshot of one tenant's counters.
type TenantStats struct {
	Weight    int64
	Deficit   int64
	QueueLen  int
	Enqueued  int64
	Dequeued  int64
	Rejected  int64
	QuotaUsed int64
}

type tenant struct {
	id          string
	weight      int64
	deficit     int64
	toppedUp    bool // quantum already credited in the current DRR round
	queue       []Request
	quota       Quota
	used        int64
	windowStart time.Time
	enqueued    int64
	dequeued    int64
	rejected    int64
}

// Scheduler is a concurrent-safe multi-tenant DRR fair scheduler.
type Scheduler struct {
	mu      sync.Mutex
	quantum int64
	maxQ    int
	now     func() time.Time
	tenants map[string]*tenant
	active  []*tenant // tenants with non-empty queues, in DRR round order
	rr      int       // round-robin cursor into active
}

// New creates a Scheduler. A zero Quantum defaults to 1.
func New(cfg Config) *Scheduler {
	q := cfg.Quantum
	if q <= 0 {
		q = 1
	}
	mq := cfg.MaxQueue
	if mq <= 0 {
		mq = defaultMaxQueue
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &Scheduler{
		quantum: q,
		maxQ:    mq,
		now:     now,
		tenants: make(map[string]*tenant),
	}
}

// AddTenant registers a tenant with a DRR weight and quota.
// Weight must be positive.
func (s *Scheduler) AddTenant(id string, weight int64, quota Quota) error {
	if weight <= 0 {
		return fmt.Errorf("scheduler: weight must be positive, got %d", weight)
	}
	if quota.Limit > 0 && quota.Window <= 0 {
		return fmt.Errorf("scheduler: quota window must be positive when limit is set")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.tenants[id]; ok {
		return ErrTenantExists
	}
	s.tenants[id] = &tenant{
		id:          id,
		weight:      weight,
		quota:       quota,
		windowStart: s.now(),
	}
	return nil
}

// RemoveTenant deletes a tenant and drops its queued requests. Other
// tenants are unaffected (isolation).
func (s *Scheduler) RemoveTenant(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tenants[id]
	if !ok {
		return
	}
	delete(s.tenants, id)
	s.deactivate(t)
}

// Submit enqueues a request for a tenant, charging its cost against the
// tenant's quota window. It fails with ErrUnknownTenant, ErrQuotaExceeded
// or ErrQueueFull without affecting other tenants.
func (s *Scheduler) Submit(tenantID string, r Request) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tenants[tenantID]
	if !ok {
		return ErrUnknownTenant
	}
	cost := r.normalizedCost()
	s.refreshWindow(t)
	if t.quota.Limit > 0 && t.used+cost > t.quota.Limit {
		t.rejected++
		return ErrQuotaExceeded
	}
	if len(t.queue) >= s.maxQ {
		t.rejected++
		return ErrQueueFull
	}
	r.Tenant = tenantID
	r.Cost = cost
	if len(t.queue) == 0 {
		s.active = append(s.active, t)
	}
	t.queue = append(t.queue, r)
	t.used += cost
	t.enqueued++
	return nil
}

// Next dispatches the next request according to DRR weighted fairness.
// It returns false when no tenant has queued work.
func (s *Scheduler) Next() (Request, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for len(s.active) > 0 {
		if s.rr >= len(s.active) {
			s.rr = 0
		}
		t := s.active[s.rr]
		if !t.toppedUp {
			t.deficit += t.weight * s.quantum
			t.toppedUp = true
		}
		head := t.queue[0]
		if t.deficit >= head.Cost {
			t.deficit -= head.Cost
			t.queue = t.queue[1:]
			t.dequeued++
			if len(t.queue) == 0 {
				t.deficit = 0 // no hoarding: idle tenants bank nothing
				s.deactivate(t)
			}
			return head, true
		}
		// Deficit cannot cover the head request: end this tenant's turn
		// for the round. The deficit persists, so oversized requests are
		// served once enough rounds accumulate.
		t.toppedUp = false
		s.rr = (s.rr + 1) % len(s.active)
	}
	return Request{}, false
}

// Drain dispatches every queued request in DRR order and returns them.
func (s *Scheduler) Drain() []Request {
	var out []Request
	for {
		r, ok := s.Next()
		if !ok {
			return out
		}
		out = append(out, r)
	}
}

// Stats returns a snapshot of a tenant's counters.
func (s *Scheduler) Stats(tenantID string) (TenantStats, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tenants[tenantID]
	if !ok {
		return TenantStats{}, false
	}
	s.refreshWindow(t)
	return TenantStats{
		Weight:    t.weight,
		Deficit:   t.deficit,
		QueueLen:  len(t.queue),
		Enqueued:  t.enqueued,
		Dequeued:  t.dequeued,
		Rejected:  t.rejected,
		QuotaUsed: t.used,
	}, true
}

// Tenants returns the number of registered tenants.
func (s *Scheduler) Tenants() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.tenants)
}

// Pending returns the total number of queued requests across all tenants.
func (s *Scheduler) Pending() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, t := range s.tenants {
		n += len(t.queue)
	}
	return n
}

// deactivate removes t from the active list, keeping the rr cursor valid.
// Callers must hold s.mu.
func (s *Scheduler) deactivate(t *tenant) {
	for i, a := range s.active {
		if a == t {
			s.active = append(s.active[:i], s.active[i+1:]...)
			if s.rr > i {
				s.rr--
			}
			if s.rr >= len(s.active) {
				s.rr = 0
			}
			return
		}
	}
}

// refreshWindow lazily rolls the tenant's quota window forward.
// Callers must hold s.mu.
func (s *Scheduler) refreshWindow(t *tenant) {
	if t.quota.Limit <= 0 {
		return
	}
	if s.now().Sub(t.windowStart) >= t.quota.Window {
		t.used = 0
		t.windowStart = s.now()
	}
}
