// Package scheduler implements multi-tenant task scheduling with per-tenant
// quotas for isolation and weighted Deficit Round Robin (DRR) fair queuing.
package scheduler

import (
	"context"
	"errors"
	"sync"
)

var (
	ErrTenantExists = errors.New("scheduler: tenant already registered")
	ErrNoSuchTenant = errors.New("scheduler: no such tenant")
	ErrTenantBusy   = errors.New("scheduler: tenant has queued or in-flight tasks")
	ErrQueueFull    = errors.New("scheduler: tenant queue quota exceeded")
	ErrClosed       = errors.New("scheduler: scheduler is shut down")
	ErrInvalidTask  = errors.New("scheduler: invalid task")
	ErrShutDown     = errors.New("scheduler: task dropped by shutdown")
)

// maxDeficitRounds caps how many rounds of credit a tenant can accumulate
// while its head task is too expensive, limiting burstiness.
const maxDeficitRounds = 8

// Quota describes per-tenant resource limits and scheduling weight.
type Quota struct {
	// Weight is the DRR weight; values < 1 are treated as 1.
	Weight uint64
	// MaxQueued bounds pending tasks; 0 rejects every submission.
	MaxQueued int
	// MaxInflight bounds concurrently executing tasks; values < 1 are
	// treated as 1.
	MaxInflight int
}

// Task is a unit of work submitted by a tenant.
type Task struct {
	TenantID string
	// Cost is the work units charged against the tenant's DRR deficit.
	// Zero is normalized to 1.
	Cost uint64
	Run  func(ctx context.Context) error

	done chan error
}

// TenantStats is a point-in-time view of a tenant's scheduler state.
type TenantStats struct {
	Queued    int
	Inflight  int
	Deficit   uint64
	Submitted uint64
	Completed uint64
	Rejected  uint64
}

type tenant struct {
	quota     Quota
	queue     []*Task
	inflight  int
	deficit   uint64
	submitted uint64
	completed uint64
	rejected  uint64
}

// Config configures a Scheduler.
type Config struct {
	// Workers is the number of goroutines executing tasks.
	Workers int
	// Quantum is the base DRR quantum; each round a tenant earns
	// Weight*Quantum deficit credit. Zero is treated as 1.
	Quantum uint64
}

// Scheduler dispatches tenant tasks to a worker pool using weighted DRR.
type Scheduler struct {
	mu      sync.Mutex
	cond    *sync.Cond
	tenants map[string]*tenant
	order   []string
	rr      int
	ready   []*Task
	quantum uint64
	closed  bool
	wg      sync.WaitGroup
	ctx     context.Context
	cancel  context.CancelFunc
}

// New creates a Scheduler and starts its worker pool.
func New(cfg Config) *Scheduler {
	quantum := cfg.Quantum
	if quantum == 0 {
		quantum = 1
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Scheduler{
		tenants: make(map[string]*tenant),
		quantum: quantum,
		ctx:     ctx,
		cancel:  cancel,
	}
	s.cond = sync.NewCond(&s.mu)
	for i := 0; i < cfg.Workers; i++ {
		s.wg.Add(1)
		go s.worker()
	}
	return s
}

// RegisterTenant adds a tenant with the given quota.
func (s *Scheduler) RegisterTenant(id string, q Quota) error {
	if id == "" {
		return ErrInvalidTask
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	if _, ok := s.tenants[id]; ok {
		return ErrTenantExists
	}
	if q.Weight == 0 {
		q.Weight = 1
	}
	if q.MaxInflight < 1 {
		q.MaxInflight = 1
	}
	if q.MaxQueued < 0 {
		q.MaxQueued = 0
	}
	s.tenants[id] = &tenant{quota: q}
	s.order = append(s.order, id)
	return nil
}

// UnregisterTenant removes a tenant. It fails with ErrTenantBusy while the
// tenant still has queued or in-flight tasks.
func (s *Scheduler) UnregisterTenant(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tn, ok := s.tenants[id]
	if !ok {
		return ErrNoSuchTenant
	}
	if len(tn.queue) > 0 || tn.inflight > 0 {
		return ErrTenantBusy
	}
	delete(s.tenants, id)
	for i, oid := range s.order {
		if oid == id {
			s.order = append(s.order[:i], s.order[i+1:]...)
			if len(s.order) == 0 {
				s.rr = 0
			} else if s.rr > i {
				s.rr--
			} else if s.rr >= len(s.order) {
				s.rr = 0
			}
			break
		}
	}
	return nil
}

// Submit enqueues a task, enforcing the tenant's queue quota. On success it
// returns a channel that receives exactly one result when the task finishes.
func (s *Scheduler) Submit(t *Task) (<-chan error, error) {
	if t == nil || t.TenantID == "" {
		return nil, ErrInvalidTask
	}
	if t.Cost == 0 {
		t.Cost = 1
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, ErrClosed
	}
	tn := s.tenants[t.TenantID]
	if tn == nil {
		return nil, ErrNoSuchTenant
	}
	if len(tn.queue) >= tn.quota.MaxQueued {
		tn.rejected++
		return nil, ErrQueueFull
	}
	done := make(chan error, 1)
	t.done = done
	tn.queue = append(tn.queue, t)
	tn.submitted++
	s.cond.Signal()
	return done, nil
}

// Stats returns the tenant's current scheduling statistics.
func (s *Scheduler) Stats(id string) (TenantStats, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tn, ok := s.tenants[id]
	if !ok {
		return TenantStats{}, ErrNoSuchTenant
	}
	return TenantStats{
		Queued:    len(tn.queue),
		Inflight:  tn.inflight,
		Deficit:   tn.deficit,
		Submitted: tn.submitted,
		Completed: tn.completed,
		Rejected:  tn.rejected,
	}, nil
}

// Tenants lists registered tenant IDs in round-robin order.
func (s *Scheduler) Tenants() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.order...)
}

// Shutdown stops accepting new tasks, drains queued tasks, waits for
// in-flight tasks, and stops all workers. Tasks that can never be drained
// (only possible with zero workers) fail with ErrShutDown.
func (s *Scheduler) Shutdown() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		s.wg.Wait()
		return
	}
	s.closed = true
	s.cancel()
	s.cond.Broadcast()
	s.mu.Unlock()
	s.wg.Wait()
	s.mu.Lock()
	for _, tn := range s.tenants {
		for _, t := range tn.queue {
			t.done <- ErrShutDown
		}
		tn.queue = nil
	}
	s.mu.Unlock()
}

func (s *Scheduler) worker() {
	defer s.wg.Done()
	for {
		t := s.next()
		if t == nil {
			return
		}
		var err error
		if t.Run != nil {
			err = t.Run(s.ctx)
		}
		s.finish(t)
		t.done <- err
	}
}

func (s *Scheduler) finish(t *Task) {
	s.mu.Lock()
	if tn := s.tenants[t.TenantID]; tn != nil {
		tn.inflight--
		tn.completed++
	}
	s.cond.Broadcast()
	s.mu.Unlock()
}

// next blocks until a task is dispatchable or the scheduler is closed and
// drained.
func (s *Scheduler) next() *Task {
	s.mu.Lock()
	defer s.mu.Unlock()
	for {
		if t := s.dispatchLocked(); t != nil {
			return t
		}
		if s.closed {
			return nil
		}
		s.cond.Wait()
	}
}

// dispatchLocked runs weighted-DRR rounds and returns the next task to
// execute, or nil if no tenant can currently dispatch. Deficit accumulation
// is purely logical: if every queued head task costs more than the current
// deficit, further rounds are applied within the same call until some tenant
// can dispatch, so a large task never waits for external activity.
func (s *Scheduler) dispatchLocked() *Task {
	if len(s.ready) > 0 {
		t := s.ready[0]
		s.ready = s.ready[1:]
		return t
	}
	n := len(s.order)
	for {
		progressable := false
		for scanned := 0; scanned < n; scanned++ {
			id := s.order[s.rr]
			s.rr = (s.rr + 1) % n
			tn := s.tenants[id]
			if tn == nil {
				continue
			}
			if len(tn.queue) == 0 {
				tn.deficit = 0
				continue
			}
			if tn.inflight >= tn.quota.MaxInflight {
				continue
			}
			progressable = true
			tn.deficit += tn.quota.Weight * s.quantum
			cap := maxDeficitRounds * tn.quota.Weight * s.quantum
			if headCost := tn.queue[0].Cost; headCost > cap {
				cap = headCost
			}
			if tn.deficit > cap {
				tn.deficit = cap
			}
			for len(tn.queue) > 0 && tn.inflight < tn.quota.MaxInflight && tn.queue[0].Cost <= tn.deficit {
				head := tn.queue[0]
				tn.queue = tn.queue[1:]
				tn.deficit -= head.Cost
				tn.inflight++
				s.ready = append(s.ready, head)
			}
			if len(s.ready) > 0 {
				t := s.ready[0]
				s.ready = s.ready[1:]
				return t
			}
		}
		if !progressable {
			return nil
		}
	}
}
