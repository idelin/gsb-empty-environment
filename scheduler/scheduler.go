// Package scheduler provides multi-tenant task scheduling with
// resource isolation and Deficit Round Robin (DRR) weighted fair
// queuing. Each tenant gets an isolated FIFO queue, a weight, and
// quota limits (max queued tasks, max concurrent executions), so a
// noisy tenant can neither starve nor overwhelm the others.
package scheduler

import (
	"context"
	"errors"
	"sync"
)

var (
	// ErrUnknownTenant is returned when submitting to an unregistered tenant.
	ErrUnknownTenant = errors.New("scheduler: unknown tenant")
	// ErrQuotaExceeded is returned when a tenant's queue quota is full.
	ErrQuotaExceeded = errors.New("scheduler: tenant queue quota exceeded")
	// ErrStopped is returned when submitting to a stopped scheduler.
	ErrStopped = errors.New("scheduler: stopped")
)

// Task is a unit of work submitted by a tenant.
type Task struct {
	Tenant string
	// Cost is the deficit cost charged to the tenant per execution.
	// Values <= 0 are treated as 1.
	Cost int64
	// Run executes the task. A nil Run is a no-op. The context is
	// canceled when the scheduler stops.
	Run func(ctx context.Context)
}

// TenantConfig configures weight and quota for one tenant.
type TenantConfig struct {
	// Weight is the DRR share; a tenant with weight 3 gets roughly
	// 3x the service rate of a tenant with weight 1. Default 1.
	Weight int64
	// MaxConcurrent caps simultaneously running tasks of this tenant.
	// Default 64.
	MaxConcurrent int
	// MaxQueue caps queued (not yet running) tasks; submissions beyond
	// the cap are rejected with ErrQuotaExceeded. Default 100000.
	MaxQueue int
}

func (c *TenantConfig) withDefaults() TenantConfig {
	out := *c
	if out.Weight <= 0 {
		out.Weight = 1
	}
	if out.MaxConcurrent <= 0 {
		out.MaxConcurrent = 64
	}
	if out.MaxQueue <= 0 {
		out.MaxQueue = 100000
	}
	return out
}

// Stats is a point-in-time snapshot of a tenant's counters.
type Stats struct {
	Submitted int64
	Completed int64
	Rejected  int64
	Inflight  int
	Queued    int
}

type tenantState struct {
	cfg       TenantConfig
	queue     []Task
	deficit   int64
	inflight  int
	submitted int64
	completed int64
	rejected  int64
}

type launched struct {
	tenant string
	task   Task
}

// Scheduler dispatches tenant tasks to a fixed worker pool using DRR.
type Scheduler struct {
	quantum int64

	mu       sync.Mutex
	tenants  map[string]*tenantState
	active   []string // tenants with non-empty queues, round-robin order
	cursor   int
	stopping bool

	notify chan struct{}
	workCh chan *launched

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// New creates a scheduler with the given worker count and DRR quantum
// (base deficit credited per round, multiplied by tenant weight).
func New(workers int, quantum int64) *Scheduler {
	if workers < 1 {
		workers = 1
	}
	if quantum < 1 {
		quantum = 1
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Scheduler{
		quantum: quantum,
		tenants: make(map[string]*tenantState),
		notify:  make(chan struct{}, 1),
		workCh:  make(chan *launched, workers),
		ctx:     ctx,
		cancel:  cancel,
	}
	s.wg.Add(1)
	go s.dispatchLoop()
	for i := 0; i < workers; i++ {
		s.wg.Add(1)
		go s.worker()
	}
	return s
}

// RegisterTenant adds a tenant with the given weight and quotas.
// Re-registering replaces the configuration of an idle tenant.
func (s *Scheduler) RegisterTenant(id string, cfg TenantConfig) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tenants[id] = &tenantState{cfg: cfg.withDefaults()}
}

// Submit enqueues a task. It returns ErrUnknownTenant, ErrQuotaExceeded
// or ErrStopped on failure and never blocks on queue capacity.
func (s *Scheduler) Submit(t Task) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopping {
		return ErrStopped
	}
	ts, ok := s.tenants[t.Tenant]
	if !ok {
		return ErrUnknownTenant
	}
	if len(ts.queue) >= ts.cfg.MaxQueue {
		ts.rejected++
		return ErrQuotaExceeded
	}
	if t.Cost <= 0 {
		t.Cost = 1
	}
	if len(ts.queue) == 0 {
		s.active = append(s.active, t.Tenant)
	}
	ts.queue = append(ts.queue, t)
	ts.submitted++
	s.signalLocked()
	return nil
}

// Stats returns a snapshot of a tenant's counters.
func (s *Scheduler) Stats(id string) (Stats, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ts, ok := s.tenants[id]
	if !ok {
		return Stats{}, false
	}
	return Stats{
		Submitted: ts.submitted,
		Completed: ts.completed,
		Rejected:  ts.rejected,
		Inflight:  ts.inflight,
		Queued:    len(ts.queue),
	}, true
}

// Stop rejects new submissions, drains queued tasks, waits for all
// in-flight tasks and background goroutines to finish, then returns.
// It is safe to call Stop multiple times.
func (s *Scheduler) Stop() {
	s.mu.Lock()
	if s.stopping {
		s.mu.Unlock()
		s.wg.Wait()
		return
	}
	s.stopping = true
	s.mu.Unlock()
	s.cancel()
	s.signal()
	s.wg.Wait()
}

func (s *Scheduler) signalLocked() {
	select {
	case s.notify <- struct{}{}:
	default:
	}
}

func (s *Scheduler) signal() {
	s.mu.Lock()
	s.signalLocked()
	s.mu.Unlock()
}

func (s *Scheduler) inflightTotalLocked() int {
	n := 0
	for _, ts := range s.tenants {
		n += ts.inflight
	}
	return n
}

func (s *Scheduler) dispatchLoop() {
	defer s.wg.Done()
	for {
		s.mu.Lock()
		if s.stopping && len(s.active) == 0 && s.inflightTotalLocked() == 0 {
			s.mu.Unlock()
			close(s.workCh)
			return
		}
		jobs := s.dispatchLocked()
		s.mu.Unlock()
		for _, j := range jobs {
			s.workCh <- j
		}
		if len(jobs) == 0 {
			<-s.notify
		}
	}
}

// dispatchLocked runs exactly one DRR round: every active tenant is
// credited weight*quantum once and may dispatch while its deficit and
// its concurrency quota allow. Running a single round per call keeps
// the weight ratio intact: completions between rounds free inflight
// slots, so tenants cannot all pile up to identical concurrency caps
// within one locked pass (which would flatten weights to 1:1).
func (s *Scheduler) dispatchLocked() []*launched {
	var out []*launched
	n := len(s.active)
	for visited := 0; visited < n; visited++ {
		if s.cursor >= len(s.active) {
			s.cursor = 0
		}
		id := s.active[s.cursor]
		ts := s.tenants[id]
		ts.deficit += ts.cfg.Weight * s.quantum
		// Cap deficit so a tenant blocked on MaxConcurrent cannot
		// accumulate unbounded credit and later monopolize workers.
		if cap := ts.cfg.Weight * s.quantum * 4; ts.deficit > cap {
			ts.deficit = cap
		}
		for len(ts.queue) > 0 &&
			ts.queue[0].Cost <= ts.deficit &&
			ts.inflight < ts.cfg.MaxConcurrent {
			t := ts.queue[0]
			ts.queue = ts.queue[1:]
			ts.deficit -= t.Cost
			ts.inflight++
			out = append(out, &launched{tenant: id, task: t})
		}
		if len(ts.queue) == 0 {
			ts.deficit = 0
			s.active = append(s.active[:s.cursor], s.active[s.cursor+1:]...)
			// The next tenant shifted into the current cursor slot.
		} else {
			s.cursor++
		}
	}
	return out
}

func (s *Scheduler) worker() {
	defer s.wg.Done()
	for j := range s.workCh {
		s.runTask(j)
	}
}

func (s *Scheduler) runTask(j *launched) {
	defer func() {
		_ = recover() // a panicking tenant task must not kill the worker
		s.mu.Lock()
		if ts, ok := s.tenants[j.tenant]; ok {
			ts.inflight--
			ts.completed++
		}
		s.signalLocked()
		s.mu.Unlock()
	}()
	if j.task.Run != nil {
		j.task.Run(s.ctx)
	}
}
